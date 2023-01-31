package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-vsock"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func doExec(ctx context.Context, args []string) error {
	var cfg VMConfig

	flags := flag.NewFlagSet("exec", flag.ExitOnError)
	addVMFlags(flags, &cfg)

	flags.Parse(args)

	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}})

	logrus.Infof("%+v", cfg)
	logrus.Info(args)

	canUseHostCPU()

	if !cfg.UseVsock {
		go func() {
			if err := doSSH(ctx, "/tmp/sockets", "22", cfg.Uid, cfg.Gid); err != nil {
				logrus.WithError(err).Error("ssh failed")
			}
		}()
	}

	return execVM(cfg)
}

func execVM(cfg VMConfig) error {
	var (
		kvmOpts     []string
		microvmOpts string
	)
	if !cfg.NoKVM {
		kvmOpts = []string{"-enable-kvm", "-cpu", "host"}
		microvmOpts = ",x-option-roms=off,isa-serial=off,rtc=off"
	}

	var (
		deviceSuffix   string
		microVmOptList []string
	)
	if !cfg.NoMicro {
		deviceSuffix = "-device"
		microVmOptList = []string{"-M", "microvm" + microvmOpts}
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

	args = append(args, microVmOptList...)

	netAddr := "user,id=net0,net=192.168.76.0/24,dhcpstart=192.168.76.9"
	if len(cfg.PortForwards) > 0 {
		netAddr += ",hostfwd=" + portForwardsToQemuFlag(cfg.PortForwards)
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

	logrus.WithField("args", args).Info("executing qemu")

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd.Run()
}

func doVsock(cid uint32, uid, gid int) error {
	sock := "/tmp/sockets/docker.sock"
	l, err := net.Listen("unix", sock)
	if err != nil {
		if !errors.Is(err, unix.EADDRINUSE) {
			return err
		}
		if err := unix.Unlink(sock); err != nil {
			logrus.WithError(err).Error("unlink failed")
		}
		l, err = net.Listen("unix", "/tmp/sockets/docker.sock")
		if err != nil {
			return err
		}
	}

	if err := os.Chown(sock, uid, gid); err != nil {
		return fmt.Errorf("error setting ownership on proxied docker socket: %w", err)
	}

	go func() {
		defer l.Close()

		for {
			conn, err := l.Accept()
			if err != nil {
				logrus.WithError(err).Error("accept failed")
				return
			}
			go func() {
				defer conn.Close()
				var vsConn net.Conn

				for i := 0; ; i++ {
					vsConn, err = vsock.DialVsock(cid, 2375)
					if err != nil {
						if i == 10 {
							logrus.WithError(err).Error("vsock dial failing, retrying...")
							i = 0
						}
						time.Sleep(250 * time.Millisecond)
						continue
					}
					break
				}
				defer vsConn.Close()

				go io.Copy(vsConn, conn)
				io.Copy(conn, vsConn)
			}()
		}
	}()

	return nil
}
