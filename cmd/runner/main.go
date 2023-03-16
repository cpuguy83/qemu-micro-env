package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"dagger.io/dagger"
	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	"github.com/cpuguy83/go-docker/image"
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

var vmxRegexp = regexp.MustCompile(`flags.*:.*(vmx|svm)`)

func canUseHostCPU() bool {
	_, err := os.Stat("/dev/kvm")
	if err != nil {
		if err := unix.Mknod("/dev/kvm", unix.S_IFCHR|0666, int(unix.Mkdev(10, 232))); err != nil {
			return false
		}
	}

	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		if vmxRegexp.MatchString(scanner.Text()) {
			return true
		}
	}

	return false
}

type intListFlag []int

func (f *intListFlag) String() string {
	return fmt.Sprint(*f)
}

func (f *intListFlag) Set(s string) error {
	for _, v := range strings.Split(s, ",") {
		i, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		*f = append(*f, i)
	}
	return nil
}

func do(ctx context.Context) error {

	var cfg VMConfig
	addVMFlags(flag.CommandLine, &cfg)

	debug := flag.Bool("debug", false, "enable debug logging")
	socketDirFl := flag.String("socket-dir", "build/", "directory to use for the socket")

	flag.Parse()

	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}})
	if *debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	switch flag.Arg(0) {
	case "checkvmx":
		if canUseHostCPU() {
			fmt.Fprintln(os.Stdout, "true")
			return nil
		}
		fmt.Fprintln(os.Stdout, "false")
		return nil
	case "exec":
		return doExec(ctx, os.Args[2:])
	case "":
	default:
		return fmt.Errorf("unknown command: %q", flag.Arg(0))
	}

	switch cfg.CgroupVersion {
	case 1, 2:
	default:
		return fmt.Errorf("invalid cgroup version: %d", cfg.CgroupVersion)
	}

	if cfg.UseVsock {
		// microvm is incompatible with vsock as vsock requires a pci device
		cfg.NoMicro = true
	}

	l := logrus.New()
	l.SetFormatter(&nested.Formatter{})
	l.SetOutput(os.Stderr)
	l.SetLevel(logrus.StandardLogger().Level)

	docker := docker.NewClient()

	daggerImgRef := image.Remote{Host: "registry.dagger.io", Locator: "engine", Tag: "v0.4.0"}
	if err := docker.ImageService().Pull(ctx, daggerImgRef); err != nil {
		return fmt.Errorf("error pulling engine image: %w", err)
	}

	daggerCtr, err := docker.ContainerService().Create(ctx, daggerImgRef.String(),
		container.WithCreateHostConfig(
			containerapi.HostConfig{
				Privileged: true,
				Mounts: []mount.Mount{
					{Type: "volume", Source: "qemu-micro-env-dagger", Target: "/var/lib/dagger"},
				},
			},
		),
	)
	if err != nil {
		return fmt.Errorf("error creating dagger container: %w", err)
	}
	defer func() {
		docker.ContainerService().Remove(context.Background(), daggerCtr.ID(), container.WithRemoveForce)
	}()

	if l.GetLevel() >= logrus.DebugLevel {
		stderr, err := daggerCtr.StderrPipe(ctx)
		if err != nil {
			l.WithError(err).Warn("could not get engine stderr pipe")
		} else {
			defer stderr.Close()
			go func() {
				io.Copy(l.WithField("component", "engine").WriterLevel(logrus.DebugLevel), stderr)
			}()
		}
	}
	if err := daggerCtr.Start(ctx); err != nil {
		return fmt.Errorf("error starting dagger container: %w", err)
	}

	os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-container://"+daggerCtr.ID())
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(l.WithField("component", "builder").WriterLevel(logrus.DebugLevel)))
	if err != nil {
		return err
	}
	defer client.Close()

	qcow := MakeQcow(client, WithInit(client, JammyRootfs(client), "/sbin/custom-init"), 10*1024*1024*1024)
	kernel, err := JammyKernelKVM(ctx, client)
	if err != nil {
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

	img := QemuImg(client).
		WithFile("/tmp/rootfs.qcow2", qcow).
		WithFile("/boot/vmlinuz", kernel.Kernel).
		WithFile("/boot/initrd.img", kernel.Initrd)

	l.Info("Building qemu image")
	if _, err := img.Export(ctx, "build/qemu-img.tar"); err != nil {
		return err
	}

	defaultArch := getDefaultCPUArch()
	if !cfg.NoKVM {
		if cfg.CPUArch != defaultArch {
			switch {
			case cfg.CPUArch == "arm" && defaultArch == "arm64":
			default:
				cfg.NoKVM = true
				cfg.NoMicro = true
			}
		}
		if !cfg.NoKVM {
			cfg.NoKVM = !canUseHostCPU()
		}
	}

	args := append([]string{"/tmp/runner", "exec"}, cfg.AsFlags()...)

	f, err := os.Open("build/qemu-img.tar")
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

	if !filepath.IsAbs(sockDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		sockDir = filepath.Join(cwd, sockDir)
	}

	var runnerBin string
	if cgoEnabled {
		runnerBin = filepath.Join(sockDir, "runner")
		if _, err := Self(client).Export(ctx, runnerBin); err != nil {
			return err
		}
	} else {
		runnerBin, err = filepath.EvalSymlinks("/proc/self/exe")
		if err != nil {
			return err
		}
		if runnerBin == "" {
			return errors.New("failed to resolve runner binary path")
		}
	}

	portForwards := cfg.PortForwards
	noKVM := cfg.NoKVM
	useVosck := cfg.UseVsock
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
		cfg.Spec.HostConfig.Mounts = append(cfg.Spec.HostConfig.Mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: runnerBin,
			Target: "/tmp/runner",
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
	ch := make(chan error)
	go func() {
		_, err := ws.ExitCode()
		if err != nil {
			ch <- err
		}
		close(ch)
	}()

	logrus.Info("Waiting for container to exit...")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-sshErr:
		select {
		case err2 := <-ch:
			if err2 != nil {
				return err2
			}
		default:
		}
		if err != nil {
			return err
		}
	case err := <-ch:
		if err != nil {
			return err
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
