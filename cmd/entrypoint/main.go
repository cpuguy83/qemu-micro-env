package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/qemu-micro-env/build/vmconfig"
	"github.com/moby/sys/signal"
	"github.com/sirupsen/logrus"
)

type logFormatter struct {
	base *nested.Formatter
}

func (f *logFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	entry.Data["component"] = "qemu-exec"
	return f.base.Format(entry)
}

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}})

	if err := doExec(context.Background(), os.Args[1:]); err != nil {
		logrus.Fatal(err)
	}
}

func doExec(ctx context.Context, args []string) error {
	var cfg vmconfig.VMConfig

	flags := flag.CommandLine
	vmconfig.AddVMFlags(flags, &cfg)
	debug := flag.Bool("debug", false, "enable debug logging")

	flags.Parse(args)

	if *debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	logrus.Debugf("%+v", cfg)
	logrus.Debug(args)

	return execVM(ctx, cfg)
}

func execVM(ctx context.Context, cfg vmconfig.VMConfig) error {
	if !cfg.NoKVM {
		cfg.NoKVM = !vmconfig.CanUseHostCPU(cfg.CPUArch)
	}
	if cfg.NoKVM && cfg.RequireKVM {
		return fmt.Errorf("kvm is required by user but not available on this system for arch %s", cfg.CPUArch)
	}

	if cfg.UseVsock {
		// microvm is incompatible with vsock as vsock requires a pci device
		cfg.NoMicro = true
	}

	if !cfg.NoMicro {
		out, err := exec.Command("qemu-system-"+cfg.CPUArch, "-M", "help").CombinedOutput()
		if err != nil {
			return fmt.Errorf("error getting machine types: %w: %s", err, string(out))
		}

		if !strings.Contains(string(out), "microvm") {
			logrus.Debug("Qemu machine type 'microvm' not supported on this system, falling back to 'virt'")
			cfg.NoMicro = true
		}
	}

	var (
		deviceSuffix string
		machineType  string
		kvmOpts      []string
		microvmOpts  string
		disableACPI  string
		debugArg     string
	)

	kernelArgs := []string{
		"root=/dev/vda",
		"rw",
		"reboot=t",
		"panic=-1",
		"ip=dhcp",
		"init=/sbin/init",
		"console=hvc0",
	}

	if cfg.DebugConsole {
		debugArg = " --debug-console "
	}

	if logrus.GetLevel() >= logrus.DebugLevel {
		kernelArgs = append(kernelArgs, "earlyprintk=ttyS0")
		debugArg += " --debug "
	} else {
		kernelArgs = append(kernelArgs, "quiet")
	}

	if !cfg.NoKVM {
		kvmOpts = []string{"-enable-kvm", "-cpu", "host"}
		microvmOpts = ",x-option-roms=off,isa-serial=off,rtc=off"
	}

	if cfg.NoMicro {
		machineType = "virt"
	} else {
		disableACPI = "-no-acpi"
		kernelArgs = append(kernelArgs, "acpi=off")
		deviceSuffix = "-device"
		machineType = "microvm" + microvmOpts
	}

	device := func(name string, opts ...string) string {
		out := name + deviceSuffix
		if len(opts) > 0 {
			out += "," + strings.Join(opts, ",")
		}
		return out
	}

	var vsockArg string
	if cfg.UseVsock {
		vsockArg = " --vsock "
	}

	initArgs := []string{
		"--cgroup-version", strconv.Itoa(cfg.CgroupVersion),
		debugArg,
		vsockArg,
		cfg.InitCmd,
	}

	kernelArgs = append(kernelArgs, append([]string{"-"}, initArgs...)...)

	args := []string{
		"/usr/bin/qemu-system-" + cfg.CPUArch,
		"-m", cfg.Memory,
		"-smp", strconv.Itoa(cfg.NumCPU),
		"-no-reboot",
		"-nodefaults",
		"-no-user-config",
		"-nographic",
		"-device", device("virtio-serial"),
		"-chardev", "stdio,id=virtiocon0",
		"-device", "virtconsole,chardev=virtiocon0",
		"-M", machineType,
		"-drive", "id=root,file=/tmp/rootfs.qcow2,format=qcow2,if=none",
		"-device", device("virtio-blk", "drive=root"),

		"-kernel", "/boot/vmlinuz",
		"-initrd", "/boot/initrd.img",

		// pass through the host's rng device to the guest
		"-object", "rng-random,id=rng0,filename=/dev/urandom",
		"-device", device("virtio-rng", "rng=rng0"),
		"-append", strings.Join(kernelArgs, " "),
	}

	if disableACPI != "" {
		args = append(args, disableACPI)
	}

	args = append(args, cfg.QemuExtraArgs...)

	netAddr := "user,id=net0,net=192.168.76.0/24,dhcpstart=192.168.76.9"
	var localPorts []int
	if len(cfg.PortForwards) > 0 {
		var err error
		localPorts, err = vmconfig.GetLocalPorts(cfg.PortForwards)
		if err != nil {
			return fmt.Errorf("error getting local ports: %w", err)
		}
		netAddr += "," + vmconfig.PortForwardsToQemuFlag(localPorts, cfg.PortForwards)
	}
	args = append(args, []string{
		"-netdev", netAddr,
		"-device", device("virtio-net", "netdev=net0"),
	}...)

	if cfg.UseVsock {
		args = append(args, []string{"-device", "vhost-vsock-pci,guest-cid=10"}...)
		if err := vmconfig.DoVsock(10, cfg.Uid, cfg.Gid); err != nil {
			return fmt.Errorf("error setting up vsock: %w", err)
		}
	} else {
		// pipes to send ssh keys to the guest
		args = append(args, []string{
			"-chardev", "pipe,id=ssh_keys,path=/tmp/sockets/authorized_keys",
			"-device", device("virtio-serial"),
			"-device", "virtserialport,chardev=ssh_keys,name=authorized_keys",
		}...)
	}

	args = append(args, []string{"-runas", strconv.Itoa(cfg.Uid) + ":" + strconv.Itoa(cfg.Gid)}...)

	if kvmOpts != nil {
		args = append(args, kvmOpts...)
	}

	logrus.WithField("args", args).Debug("executing qemu")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	var sshPort string
	// For some reason qemu user mode networking doesn't work with docker port forwarding (connections just hang).
	// So... we'll forward the ports ourselves and use an ephemeral port for the qemu hostfwd spec.
	for i, port := range localPorts {
		if cfg.PortForwards[i] == 22 {
			sshPort = strconv.Itoa(port)
		}
		if err := vmconfig.ForwardPort(cfg.PortForwards[i], port); err != nil {
			return fmt.Errorf("error forwarding port: %w", err)
		}
	}

	go func() {
		if err := doSSH(ctx, "/tmp/sockets", sshPort, cfg.Uid, cfg.Gid, cfg.SocketForwards); err != nil {
			logrus.WithError(err).Error("ssh failed")
			cancel()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.CatchAll(sigCh)
	defer signal.StopCatch(sigCh)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error starting qemu: %w", err)
	}

	go func() {
		for sig := range sigCh {
			if err := cmd.Process.Signal(sig); err != nil {
				logrus.WithError(err).Warn("Failed to forward signal to qemu")
			}
		}
	}()

	return cmd.Wait()
}
