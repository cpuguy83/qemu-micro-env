package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	"github.com/cpuguy83/go-docker/image"
	"github.com/cpuguy83/qemu-micro-env/cmd/entrypoint/vmconfig"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

type logFormatter struct {
	base *nested.Formatter
}

func (f *logFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	entry.Data["component"] = "runner"
	return f.base.Format(entry)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer cancel()
	if err := do(ctx); err != nil {
		logrus.Fatalf("%+v", err)
	}
}

func do(ctx context.Context) error {
	var cfg vmconfig.VMConfig
	vmconfig.AddVMFlags(flag.CommandLine, &cfg)

	debug := flag.Bool("debug", false, "enable debug logging")
	stateDirFl := flag.String("state-dir", "_output/", "directory to use for state files (socket, image, etc)")

	flag.Parse()

	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}})
	if *debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	switch cfg.CgroupVersion {
	case 1, 2:
	default:
		return fmt.Errorf("invalid cgroup version: %d", cfg.CgroupVersion)
	}

	imgTar := flag.Arg(1)
	var f *os.File
	switch imgTar {
	case "", "-":
		f = os.Stdin
	default:
		var err error
		f, err = os.Open(imgTar)
		if err != nil {
			return fmt.Errorf("failed to open image tar: %w", err)
		}
	}
	defer f.Close()

	docker := docker.NewClient()

	l := logrus.New()
	l.SetFormatter(&nested.Formatter{})
	l.SetOutput(os.Stderr)
	l.SetLevel(logrus.StandardLogger().Level)

	var imageID string
	l.Info("Loading image into docker...")
	if err := docker.ImageService().Load(ctx, f, func(cfg *image.LoadConfig) error {
		cfg.ConsumeProgress = func(ctx context.Context, rdr io.Reader) error {
			dec := json.NewDecoder(rdr)
			var res struct {
				Stream string `json:"stream"`
			}

			for {
				err := dec.Decode(&res)
				if err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}

				res.Stream = strings.TrimSuffix(res.Stream, "\n")
				_, id, found := strings.Cut(res.Stream, " ID: ")
				if !found {
					continue
				}
				imageID = id
				io.Copy(io.Discard, rdr)
				return nil
			}
		}
		return nil
	}); err != nil {
		return err
	}

	stateDir := *stateDirFl
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return err
	}

	if !cfg.UseVsock {
		// add the ssh port forward if we're not using vsock
		cfg.PortForwards = append([]int{22}, cfg.PortForwards...)
	}

	if !filepath.IsAbs(stateDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		stateDir = filepath.Join(cwd, stateDir)
	}

	portForwards := cfg.PortForwards
	noKVM := cfg.NoKVM
	useVosck := cfg.UseVsock
	args := append([]string{"/usr/local/bin/docker-entrypoint"}, cfg.AsFlags()...)
	c, err := docker.ContainerService().Create(ctx, imageID, func(cfg *container.CreateConfig) {
		cfg.Spec.OpenStdin = true
		cfg.Spec.AttachStdin = true
		cfg.Spec.AttachStdout = true
		cfg.Spec.AttachStderr = true
		cfg.Spec.HostConfig.AutoRemove = true

		init := true
		cfg.Spec.HostConfig.Init = &init

		cfg.Spec.ExposedPorts = map[string]struct{}{}
		for _, port := range portForwards {
			cfg.Spec.ExposedPorts[fmt.Sprintf("%d/tcp", port)] = struct{}{}
		}

		cfg.Spec.HostConfig.PublishAllPorts = true

		if useVosck {
			// TODO: make custom seccomp profile
			// Docker's profile rejects AF_VSOCK sockets by default
			cfg.Spec.HostConfig.SecurityOpt = []string{"seccomp=unconfined"}
		}

		cfg.Spec.Env = append(cfg.Spec.Env, "SSH_AUTH_SOCK=/tmp/sockets/agent.sock")

		cfg.Spec.HostConfig.Mounts = append(cfg.Spec.HostConfig.Mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: stateDir,
			Target: "/tmp/sockets",
		})
		cfg.Spec.Entrypoint = args

		if _, err := os.Stat("/dev/vhost-net"); err == nil {
			cfg.Spec.HostConfig.Devices = append(cfg.Spec.HostConfig.Devices, containerapi.DeviceMapping{
				PathOnHost:        "/dev/vhost-net",
				PathInContainer:   "/dev/vhost-net",
				CgroupPermissions: "rwm",
			})
		}

		if useVosck {
			cfg.Spec.HostConfig.Devices = append(cfg.Spec.HostConfig.Devices, containerapi.DeviceMapping{
				PathOnHost:        "/dev/vhost-vsock",
				PathInContainer:   "/dev/vhost-vsock",
				CgroupPermissions: "rwm",
			})
		}

		if !noKVM {
			cfg.Spec.HostConfig.Devices = append(cfg.Spec.HostConfig.Devices, containerapi.DeviceMapping{
				PathOnHost:        "/dev/kvm",
				PathInContainer:   "/dev/kvm",
				CgroupPermissions: "rwm",
			})
		}

		cfg.Spec.HostConfig.Devices = append(cfg.Spec.HostConfig.Devices, containerapi.DeviceMapping{
			PathOnHost:        "/dev/net/tun",
			PathInContainer:   "/dev/net/tun",
			CgroupPermissions: "rwm",
		})
	})
	if err != nil {
		return fmt.Errorf("error creating container: %w", err)
	}

	defer c.Kill(context.Background())

	ctxWait, cancel := context.WithCancel(ctx)
	defer cancel()

	ws, err := c.Wait(ctxWait, container.WithWaitCondition(container.WaitConditionNextExit))
	if err != nil {
		return fmt.Errorf("error waiting for container: %w", err)
	}

	if err := attachPipes(ctx, c); err != nil {
		return err
	}

	if err := c.Start(ctx); err != nil {
		return fmt.Errorf("error starting container: %w", err)
	}

	sshErr := make(chan error, 1)
	ch := make(chan struct {
		code int
		err  error
	})
	go func() {
		code, err := ws.ExitCode()
		ch <- struct {
			code int
			err  error
		}{code, err}
		close(ch)
	}()

	logrus.Info("Waiting for container to exit...")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-sshErr:
		select {
		case status := <-ch:
			if status.err != nil {
				return status.err
			}
			if status.code != 0 {
				return fmt.Errorf("container exited with code %d", status.code)
			}
		default:
		}
		if err != nil {
			return err
		}
	case status := <-ch:
		if status.err != nil {
			return status.err
		}
		if status.code != 0 {
			return fmt.Errorf("container exited with code %d", status.code)
		}
	}

	logrus.Info("Container exited")
	return nil
}

func attachPipes(ctx context.Context, c *container.Container) error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		stdout, err := c.StdoutPipe(ctx)
		if err != nil {
			return err
		}
		go func() {
			io.Copy(os.Stdout, stdout)
			stdout.Close()
		}()
		return nil
	})

	eg.Go(func() error {
		stderr, err := c.StderrPipe(ctx)
		if err != nil {
			return err
		}
		go func() {
			io.Copy(os.Stderr, stderr)
			stderr.Close()
		}()
		return nil
	})

	eg.Go(func() error {
		stdin, err := c.StdinPipe(ctx)
		if err != nil {
			return err
		}
		go func() {
			io.Copy(stdin, os.Stdin)
			stdin.Close()
		}()
		return nil
	})

	return eg.Wait()
}
