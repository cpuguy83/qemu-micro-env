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

	"dagger.io/dagger"
	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	"github.com/cpuguy83/go-docker/image"
	"github.com/cpuguy83/qemu-micro-env/cmd/entrypoint/vmconfig"
	"github.com/cpuguy83/qemu-micro-env/flags"
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
		logrus.Fatal(err)
	}
}

func do(ctx context.Context) error {
	var cfg vmconfig.VMConfig
	vmconfig.AddVMFlags(flag.CommandLine, &cfg)

	debug := flag.Bool("debug", false, "enable debug logging")
	socketDirFl := flag.String("socket-dir", "_output/", "directory to use for the socket")

	mountSpecFl := flags.NewMountSpec(&mount.Mount{Type: mount.TypeVolume, Source: "qemu-micro-env-dagger-state"})
	flags.AddMountSpecFlag(flag.CommandLine, mountSpecFl, "dagger-cache-mount")

	flag.Parse()

	cacheMount := mountSpecFl.AsMount()

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

	l := logrus.New()
	l.SetFormatter(&nested.Formatter{})
	l.SetOutput(os.Stderr)
	l.SetLevel(logrus.StandardLogger().Level)

	docker := docker.NewClient()

	client, cleanup, err := getDaggerClient(ctx, docker, l, cacheMount)
	if err != nil {
		return err
	}
	defer cleanup()

	img, err := RunnerImg(ctx, client, cfg)
	if err != nil {
		return err
	}

	imgTar := filepath.Join(*socketDirFl, "qemu-img.tar")
	if _, err := img.Export(ctx, imgTar); err != nil {
		return err
	}

	f, err := os.Open(imgTar)
	if err != nil {
		return err
	}
	defer f.Close()

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

	if err := os.MkdirAll(*socketDirFl, 0750); err != nil {
		return err
	}

	sockDir := *socketDirFl
	if err := os.MkdirAll(sockDir, 0750); err != nil {
		return fmt.Errorf("could not create sockets dir: %w", err)
	}

	if !cfg.UseVsock {
		// add the ssh port forward if we're not using vsock
		cfg.PortForwards = append([]int{22}, cfg.PortForwards...)
	}

	if !filepath.IsAbs(sockDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		sockDir = filepath.Join(cwd, sockDir)
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
			Source: sockDir,
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

func getDaggerClient(ctx context.Context, docker *docker.Client, l *logrus.Logger, cacheMount flags.Optional[mount.Mount]) (_ *dagger.Client, cleanup func(), retErr error) {
	logOpt := dagger.WithLogOutput(l.WithField("component", "builder").WriterLevel(logrus.DebugLevel))
	cleanup = func() {}
	if docker == nil || os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST") != "" {
		client, err := dagger.Connect(ctx, logOpt)
		return client, cleanup, err
	}

	daggerImgRef := image.Remote{Host: "registry.dagger.io", Locator: "engine", Tag: "v0.4.0"}
	if err := docker.ImageService().Pull(ctx, daggerImgRef); err != nil {
		return nil, nil, fmt.Errorf("error pulling engine image: %w", err)
	}

	var mounts []mount.Mount
	if cacheMount.IsSome() {
		mnt := cacheMount.Unwrap()
		if mnt.Target == "" {
			mnt.Target = "/var/lib/dagger"
		}
		mounts = append(mounts, mnt)
	}

	ctr, err := docker.ContainerService().Create(ctx, daggerImgRef.String(),
		container.WithCreateHostConfig(
			containerapi.HostConfig{
				Privileged: true,
				Mounts:     mounts,
			},
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating dagger container: %w", err)
	}

	cleanup = func() {
		if err := docker.ContainerService().Remove(context.Background(), ctr.ID(), container.WithRemoveForce); err != nil {
			l.WithError(err).Warn("could not remove dagger container")
		}
	}
	defer func() {
		if retErr != nil {
			docker.ImageService().Remove(context.Background(), daggerImgRef.String())
			cleanup()
		}
	}()

	if l.GetLevel() >= logrus.DebugLevel {
		stderr, err := ctr.StderrPipe(ctx)
		if err != nil {
			l.WithError(err).Warn("could not get engine stderr pipe")
		} else {
			defer stderr.Close()
			go func() {
				io.Copy(l.WithField("component", "engine").WriterLevel(logrus.DebugLevel), stderr)
			}()
		}
	}
	if err := ctr.Start(ctx); err != nil {
		return nil, cleanup, fmt.Errorf("error starting dagger container: %w", err)
	}

	os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-container://"+ctr.ID())
	client, err := dagger.Connect(ctx)
	if err != nil {
		return nil, cleanup, err
	}

	return client, cleanup, nil
}
