package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/sirupsen/logrus"
)

func doExec(ctx context.Context, args []string) error {
	var cfg VMConfig

	flags := flag.NewFlagSet("exec", flag.ExitOnError)
	addVMFlags(flags, &cfg)

	flags.Parse(args)

	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}})

	logrus.Debugf("%+v", cfg)
	logrus.Debug(args)

	return execVM(ctx, cfg)
}

func execVM(ctx context.Context, cfg VMConfig) error {
	var (
		kvmOpts     []string
		microvmOpts string
	)
	if !cfg.NoKVM {
		kvmOpts = []string{"-enable-kvm", "-cpu", "host"}
		microvmOpts = ",x-option-roms=off,isa-serial=off,rtc=off"
	}

	var (
		deviceSuffix string
		machineType  []string
	)

	if !cfg.NoMicro {
		deviceSuffix = "-device"
		machineType = []string{"-M", "microvm" + microvmOpts}
	} else if cfg.NoKVM && cfg.CPUArch != "x86_64" {
		machineType = []string{"-M", "virt"}
	}

	device := func(name string, opts ...string) string {
		out := name + deviceSuffix
		if len(opts) > 0 {
			out += "," + strings.Join(opts, ",")
		}
		return out
	}

	var debugArg string
	if cfg.DebugConsole {
		debugArg = " --debug"
	}

	var vsockArg string
	if cfg.UseVsock {
		vsockArg = " --vsock"
	}

	args := []string{
		"/usr/bin/qemu-system-" + cfg.CPUArch,
		"-m", cfg.Memory,
		"-smp", strconv.Itoa(cfg.NumCPU),
		"-no-reboot",
		"-no-acpi",
		"-nodefaults",
		"-no-user-config",
		"-nographic",

		"-device", device("virtio-serial"),
		"-chardev", "stdio,id=virtiocon0",
		"-device", "virtconsole,chardev=virtiocon0",

		"-drive", "id=root,file=/tmp/rootfs.qcow2,format=qcow2,if=none",
		"-device", device("virtio-blk", "drive=root"),

		"-kernel", "/boot/vmlinuz",
		"-initrd", "/boot/initrd.img",
		"-append", "console=hvc0 root=/dev/vda rw acpi=off reboot=t panic=-1 ip=dhcp quiet init=/sbin/custom-init - --cgroup-version " + strconv.Itoa(cfg.CgroupVersion) + debugArg + vsockArg,

		// pass through the host's rng device to the guest
		"-object", "rng-random,id=rng0,filename=/dev/urandom",
		"-device", device("virtio-rng", "rng=rng0"),
	}

	args = append(args, machineType...)

	netAddr := "user,id=net0,net=192.168.76.0/24,dhcpstart=192.168.76.9"
	var localPorts []int
	if len(cfg.PortForwards) > 0 {
		var err error
		localPorts, err = getLocalPorts(cfg.PortForwards)
		if err != nil {
			return fmt.Errorf("error getting local ports: %w", err)
		}
		netAddr += "," + portForwardsToQemuFlag(localPorts, cfg.PortForwards)
	}
	args = append(args, []string{
		"-netdev", netAddr,
		"-device", device("virtio-net", "netdev=net0"),
	}...)

	if cfg.UseVsock {
		args = append(args, []string{"-device", "vhost-vsock-pci,guest-cid=10"}...)
		if err := doVsock(10, cfg.Uid, cfg.Gid); err != nil {
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

	var sshPort string
	// For some reason qemu user mode networking doesn't work with docker port forwarding (connections just hang).
	// So... we'll forward the ports ourselves and use an ephemeral port for the qemu hostfwd spec.
	for i, port := range localPorts {
		if cfg.PortForwards[i] == 22 {
			sshPort = strconv.Itoa(port)
		}
		if err := forwardPort(cfg.PortForwards[i], port); err != nil {
			return fmt.Errorf("error forwarding port: %w", err)
		}
	}

	if !cfg.UseVsock {
		go func() {
			if err := doSSH(ctx, "/tmp/sockets", sshPort, cfg.Uid, cfg.Gid); err != nil {
				logrus.WithError(err).Error("ssh failed")
				cancel()
			}
		}()
	}

	return cmd.Run()
}
