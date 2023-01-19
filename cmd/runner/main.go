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
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger"
	dockerclient "github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	"github.com/cpuguy83/go-docker/image"
	"github.com/cpuguy83/go-mod-copies/platforms"
	"github.com/cpuguy83/pipes"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer cancel()
	if err := do(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
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

func do(ctx context.Context) error {
	defaultArch := getDefaultCPUArch()
	cgVerFl := flag.Int("cgroup-version", 2, "cgroup version to use")
	noKvmFl := flag.Bool("no-kvm", false, "disable KVM")
	numCPUFl := flag.Int("num-cpus", 2, "number of CPUs to use")
	vmMemFl := flag.String("memory", "4G", "memory to use for the VM")
	socketDirFl := flag.String("socket-dir", "build/", "directory to use for the socket")
	cpuArchFl := flag.String("cpu-arch", defaultArch, "CPU architecture to use for the VM")

	portForwards := intListFlag{}
	flag.Var(&portForwards, "vm-port-forward", "port forwards to set up from the VM")

	flag.Parse()

	switch flag.Arg(0) {
	case "checkvmx":
		if canUseHostCPU() {
			fmt.Fprintln(os.Stdout, "true")
			return nil
		}
		fmt.Fprintln(os.Stdout, "false")
		return nil
	case "ssh":
		return doSSH(ctx, flag.Arg(1), "22")
	case "exec":
		return doExec(ctx, flag.Args()[1:])
	case "":
	default:
		return fmt.Errorf("unknown command: %q", flag.Arg(0))
	}

	cpuArch := *cpuArchFl

	switch *cgVerFl {
	case 1, 2:
	default:
		return fmt.Errorf("invalid cgroup version: %d", *cgVerFl)
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
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

	portForwards = append([]int{22}, portForwards...)

	img := QemuImg(client).
		WithFile("/tmp/rootfs.qcow2", qcow).
		WithFile("/boot/vmlinuz", kernel.Kernel).
		WithFile("/boot/initrd.img", kernel.Initrd)

	if _, err := img.Export(ctx, "build/qemu-img.tar"); err != nil {
		return err
	}

	noKvm := *noKvmFl
	if !noKvm {
		if cpuArch != defaultArch {
			switch {
			case cpuArch == "arm" && defaultArch == "arm64":
			default:
				noKvm = true
			}
		}

		if !noKvm {
			noKvm = !canUseHostCPU()
		}
	}

	var (
		kvmOpts     []string
		microvmOpts string
	)
	if !noKvm {
		kvmOpts = []string{"-enable-kvm", "-cpu", "host"}
		microvmOpts = ",x-option-roms=off,isa-serial=off,rtc=off"
	}

	qemuExec := []string{
		"/usr/bin/qemu-system-" + *cpuArchFl,
		"-M", "microvm" + microvmOpts,
		"-m", *vmMemFl,
		"-smp", strconv.Itoa(*numCPUFl),
		"-no-reboot",
		"-no-acpi",
		"-nodefaults",
		"-no-user-config",
		"-nographic",

		"-device", "virtio-serial-device",
		"-chardev", "stdio,id=virtiocon0",
		"-device", "virtconsole,chardev=virtiocon0",

		"-drive", "id=root,file=/tmp/rootfs.qcow2,format=qcow2,if=none",
		"-device", "virtio-blk-device,drive=root",

		"-device", "virtio-rng-device",

		"-kernel", "/boot/vmlinuz",
		"-initrd", "/boot/initrd.img",
		"-append", "console=hvc0 root=/dev/vda rw acpi=off reboot=t panic=-1 quiet init=/sbin/custom-init - --cgroup-version " + strconv.Itoa(*cgVerFl),

		"-netdev", "user,id=net0,net=192.168.76.0/24,dhcpstart=192.168.76.9,hostfwd=" + portForwardsToQemuFlag(portForwards),
		"-device", "virtio-net-device,netdev=net0",

		"-chardev", "pipe,id=ssh_keys,path=/tmp/sockets/authorized_keys",
		"-device", "virtio-serial-device",
		"-device", "virtserialport,chardev=ssh_keys,name=authorized_keys",
	}

	if kvmOpts != nil {
		qemuExec = append(qemuExec, kvmOpts...)
	}

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

		cfg.Spec.Entrypoint = qemuExec

		if !noKvm {
			cfg.Spec.HostConfig.Devices = append(cfg.Spec.HostConfig.Devices, containerapi.DeviceMapping{
				PathOnHost:        "/dev/kvm",
				PathInContainer:   "/dev/kvm",
				CgroupPermissions: "rwm",
			})
		}
	})
	if err != nil {
		return fmt.Errorf("error creating container: %w", err)
	}

	defer c.Kill(context.Background())

	ctxWait, cancel := context.WithCancel(ctx)
	defer cancel()

	ws, err := c.Wait(ctxWait, container.WithWaitCondition(container.WaitConditionNextExit))
	if err != nil {
		return err
	}

	if err := attachPipes(ctx, c); err != nil {
		return err
	}

	if err := c.Start(ctx); err != nil {
		return err
	}

	sshErr := make(chan error, 1)

	go func() {
		i, err := c.Inspect(ctx)
		if err != nil {
			sshErr <- err
			return
		}

		sshErr <- doSSH(ctx, sockDir, i.NetworkSettings.Ports["22/tcp"][0].HostPort)
	}()

	ch := make(chan error)
	go func() {
		_, err := ws.ExitCode()
		if err != nil {
			ch <- err
		}
		close(ch)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-ch:
		if err != nil {
			return err
		}
	}
	return nil
}

func portForwardsToQemuFlag(forwards []int) string {
	var out []string
	for _, f := range forwards {
		out = append(out, fmt.Sprintf("tcp::%d-:%d", f, f))
	}
	return strings.Join(out, ",")
}

func tunnelDocker(ctx context.Context, client *ssh.Client, l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()

			sshConn, err := client.Dial("unix", "/run/docker.sock")
			if err != nil {
				return
			}
			defer sshConn.Close()

			go func() {
				io.Copy(sshConn, conn)
				conn.Close()
				sshConn.Close()
			}()

			io.Copy(conn, sshConn)
			conn.Close()
			sshConn.Close()
		}()
	}
}

func generateKeys(sockDir string) ([]byte, ssh.AuthMethod, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("error generating private key: %w", err)
	}

	encodedBuf := &bytes.Buffer{}

	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	if err := pem.Encode(encodedBuf, privateKeyPEM); err != nil {
		return nil, nil, fmt.Errorf("error pem encoding private key: %w", err)
	}
	pubK, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating public key: %w", err)
	}
	pub := ssh.MarshalAuthorizedKey(pubK)

	signer, err := ssh.ParsePrivateKey(encodedBuf.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("error parsing private key: %w", err)
	}

	return pub, ssh.PublicKeys(signer), nil
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

func doSSH(ctx context.Context, sockDir string, port string) error {
	fifoPath := filepath.Join(sockDir, "authorized_keys")
	ch, err := pipes.AsyncOpenFifo(fifoPath, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("error opening fifo: %s: %w", fifoPath, err)
	}

	pub, auth, err := generateKeys(sockDir)
	if err != nil {
		return err
	}

	chAuth := make(chan error, 1)
	go func() {
		defer close(chAuth)
		chAuth <- func() error {
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
			}
			return nil
		}()
	}()

	var sshClient *ssh.Client
	sshConfig := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{auth},
		// TODO: host-key verification
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second,
	}

	fmt.Fprintln(os.Stderr, "waiting for ssh to be available...")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-chAuth:
			if err != nil {
				return err
			}
		}

		sshClient, err = ssh.Dial("tcp", "127.0.0.1:"+port, sshConfig)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error dialing ssh:", err)
			time.Sleep(time.Second)
			continue
		}
		break
	}
	defer sshClient.Close()

	fmt.Fprintln(os.Stderr, "tunnelling docker socket")
	l, err := net.Listen("unix", filepath.Join(sockDir, "docker.sock"))
	if err != nil {
		return fmt.Errorf("error listening on docker socket: %w", err)
	}
	defer l.Close()
	tunnelDocker(ctx, sshClient, l)

	return nil
}
