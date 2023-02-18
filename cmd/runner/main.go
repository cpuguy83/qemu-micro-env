package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger"
	nested "github.com/antonfisher/nested-logrus-formatter"
	dockerclient "github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	"github.com/cpuguy83/go-docker/image"
	"github.com/cpuguy83/go-mod-copies/platforms"
	"github.com/cpuguy83/pipes"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
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
		logrus.Error(err)
		os.Exit(1)
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

func getDefaultCPUArch() string {
	p := platforms.DefaultSpec()
	return archStringToQemu(p.Architecture)
}

func archStringToQemu(arch string) string {
	switch arch {
	case "amd64", "x86_64":
		return "x86_64"
	case "arm64", "aarch64":
		return "aarch64"
	case "arm":
		return "arm"
	default:
		panic("unsupported architecture")
	}
}

type VMConfig struct {
	CPUArch       string
	NumCPU        int
	PortForwards  intListFlag
	NoKVM         bool
	NoMicro       bool
	CgroupVersion int
	Memory        string
	DebugConsole  bool
	UseVsock      bool
	Uid           int
	Gid           int
}

func (c VMConfig) AsFlags() []string {
	flags := []string{
		"--cgroup-version=" + strconv.Itoa(c.CgroupVersion),
		"--no-kvm=" + strconv.FormatBool(c.NoKVM),
		"--num-cpus=" + strconv.Itoa(c.NumCPU),
		"--memory=" + c.Memory,
		"--cpu-arch=" + archStringToQemu(c.CPUArch),
		"--no-micro=" + strconv.FormatBool(c.NoMicro),
		"--debug-console=" + strconv.FormatBool(c.DebugConsole),
		"--vsock=" + strconv.FormatBool(c.UseVsock),
		"--uid=" + strconv.Itoa(c.Uid),
		"--gid=" + strconv.Itoa(c.Gid),
	}
	if len(c.PortForwards) > 0 {
		flags = append(flags, "--vm-port-forward="+strings.Join(convertPortForwards(c.PortForwards), ","))
	}
	return flags
}

func addVMFlags(set *flag.FlagSet, cfg *VMConfig) {
	set.IntVar(&cfg.CgroupVersion, "cgroup-version", 2, "cgroup version to use")
	set.BoolVar(&cfg.NoKVM, "no-kvm", false, "disable KVM")
	set.IntVar(&cfg.NumCPU, "num-cpus", 2, "number of CPUs to use")
	set.StringVar(&cfg.Memory, "memory", "4G", "memory to use for the VM")
	set.StringVar(&cfg.CPUArch, "cpu-arch", getDefaultCPUArch(), "CPU architecture to use for the VM")
	set.BoolVar(&cfg.NoMicro, "no-micro", false, "disable microVMs - useful for allowing the VM to have access to PCI devices")
	set.Var(&cfg.PortForwards, "vm-port-forward", "port forwards to set up from the VM")
	set.BoolVar(&cfg.DebugConsole, "debug-console", false, "enable debug console")
	set.BoolVar(&cfg.UseVsock, "vsock", false, "use vsock for communication")
	set.IntVar(&cfg.Uid, "uid", os.Getuid(), "uid to use for the VM")
	set.IntVar(&cfg.Gid, "gid", os.Getgid(), "gid to use for the VM")
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

	docker := dockerclient.NewClient()
	f, err := os.Open("build/qemu-img.tar")
	if err != nil {
		return err
	}
	defer f.Close()

	var imageID string
	if err := docker.ImageService().Load(ctx, f, func(cfg *image.LoadConfig) error {
		cfg.ConsumeProgress = func(ctx context.Context, rdr io.Reader) error {
			dec := json.NewDecoder(rdr)
			var res struct {
				Stream string `json:"stream"`
			}

			for {
				err := dec.Decode(&res)
				if err != nil {
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

func portForwardsToQemuFlag(forwards []int) string {
	var out []string
	for _, f := range forwards {
		out = append(out, fmt.Sprintf("tcp::%d-:%d", f, f))
	}
	return strings.Join(out, ",")
}

func generateKeys(sockDir string) ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("error generating private key: %w", err)
	}

	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	pem := pem.EncodeToMemory(privateKeyPEM)

	pubK, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return nil, nil, fmt.Errorf("error creating public key: %w", err)
	}
	pub := ssh.MarshalAuthorizedKey(pubK)

	return pub, pem, nil
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

func doSSH(ctx context.Context, sockDir string, port string, uid, gid int) error {
	logrus.Debug("Preparing SSH")
	fifoPath := filepath.Join(sockDir, "authorized_keys")

	if err := os.MkdirAll(sockDir, 0700); err != nil {
		return fmt.Errorf("error creating socket directory: %w", err)
	}

	ch, err := pipes.AsyncOpenFifo(fifoPath, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("error opening fifo: %s: %w", fifoPath, err)
	}

	logrus.Debug("Generating keys")
	pub, priv, err := generateKeys(sockDir)
	if err != nil {
		return err
	}

	logrus.Debug("Writing authorized keys")
	chAuth := make(chan error, 1)
	go func() {
		defer close(chAuth)
		chAuth <- func() error {
			logrus.Info("Waiting for fifo to be ready...")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case result := <-ch:
				if result.Err != nil {
					return fmt.Errorf("error opening fifo: %w", result.Err)
				}
				defer result.W.Close()
				if _, err := result.W.Write(append(pub, '\n')); err != nil {
					return fmt.Errorf("error writing public key to authorized_keys: %w", err)
				}
				logrus.Debug("Public key written to authorized_keys fifo")
			}
			return nil
		}()
	}()

	select {
	case <-ctx.Done():
	case err := <-chAuth:
		if err != nil {
			return err
		}
	}

	out, err := exec.CommandContext(ctx, "ssh-agent").CombinedOutput()
	if err != nil {
		return fmt.Errorf("error starting ssh-agent: %s: %w", out, err)
	}
	cmd := exec.Command("/bin/sh", "-c", "eval \""+string(out)+"\" && ssh-add -")

	cmd.Stdin = bytes.NewReader(priv)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("error adding private key to ssh-agent: %s: %w", out, err)
	}

	sock := filepath.Join(sockDir, "docker.sock")
	unix.Unlink(sock)
	logrus.Debug(string(out))

	sockKV, _, found := strings.Cut(string(out), ";")
	if !found {
		return fmt.Errorf("error parsing ssh-agent output: %s", out)
	}

	for i := 0; ; i++ {
		cmd = exec.Command(
			"/usr/bin/ssh",
			"-f",
			"-nNT",
			"-o", "BatchMode=yes",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ExitOnForwardFailure=yes",
			"-L", sock+":/run/docker.sock",
			"127.0.0.1", "-p", port,
		)
		cmd.Env = append(cmd.Env, sockKV)
		if out, err := cmd.CombinedOutput(); err != nil {
			if strings.Contains(string(out), "Connection refused") || strings.Contains(string(out), "Connection reset by peer") {
				if i == 20 {
					logrus.WithError(err).Warn(string(out))
					i = 0
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return fmt.Errorf("error setting up ssh tunnel: %w: %s", err, string(out))
		}
		break
	}

	if err := os.Chown(sock, uid, gid); err != nil {
		return fmt.Errorf("error setting ownership on proxied docker socket: %w", err)
	}

	return nil
}

func convertPortForwards(ls []int) []string {
	var result []string
	for _, l := range ls {
		result = append(result, strconv.Itoa(l))
	}
	return result
}
