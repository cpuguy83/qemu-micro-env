package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/cpuguy83/qemu-micro-env/build/vmconfig"
	"github.com/moby/term"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

func runnerFlags(set *flag.FlagSet, cfg *config) {
	set.StringVar(&cfg.StateDir, "state-dir", "_output/", "directory to use for state files (socket, image, etc)")
	vmconfig.AddVMFlags(set, &cfg.VM)
}

func doRunner(ctx context.Context, cfg config, tr transport.Doer) error {
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}, "runner"})

	switch cfg.VM.CgroupVersion {
	case 1, 2:
	default:
		return fmt.Errorf("invalid cgroup version: %d", cfg.VM.CgroupVersion)
	}

	docker := docker.NewClient(docker.WithTransport(tr))

	stateDir := cfg.StateDir
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return err
	}

	if !cfg.VM.UseVsock {
		// add the ssh port forward if we're not using vsock
		cfg.VM.PortForwards = append([]int{22}, cfg.VM.PortForwards...)
	}

	if !filepath.IsAbs(stateDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		stateDir = filepath.Join(cwd, stateDir)
	}

	portForwards := cfg.VM.PortForwards
	noKVM := cfg.VM.NoKVM
	useVosck := cfg.VM.UseVsock
	args := append([]string{entrypointPath}, cfg.VM.AsFlags()...)
	if cfg.Debug {
		args = append(args, "--debug")
	}

	needsTTY := term.IsTerminal(os.Stdin.Fd())
	c, err := docker.ContainerService().Create(ctx, cfg.ImageRef, func(cfg *container.CreateConfig) {
		cfg.Spec.OpenStdin = true
		cfg.Spec.AttachStdin = true
		cfg.Spec.AttachStdout = true
		cfg.Spec.AttachStderr = true
		cfg.Spec.HostConfig.AutoRemove = true
		cfg.Spec.Tty = needsTTY

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

	if err := attachPipes(ctx, c, needsTTY); err != nil {
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

func attachPipes(ctx context.Context, c *container.Container, tty bool) error {
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

	if !tty {
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
	}

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
