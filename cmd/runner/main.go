package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/cpuguy83/go-mod-copies/platforms"
	"golang.org/x/sys/unix"
)

func main() {
	ctx := context.Background()
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

	qcow := MakeQcow(client, WithInit(client, AlpineRootfs(client), "/sbin/init"), 10*1024*1024*1024)
	kernel := JammyKernelKVM(client)

	if err := os.MkdirAll(*socketDirFl, 0750); err != nil {
		return err
	}

	sockDir := *socketDirFl
	if err := os.MkdirAll(sockDir, 0750); err != nil {
		return fmt.Errorf("could not create sockets dir: %w", err)
	}

	portForwards = append([]int{22}, portForwards...)

	QemuImg(client)

	selfBin := Self(client)

	img := QemuImg(client).
		WithMountedFile("/tmp/rootfs.qcow2", qcow).
		WithMountedFile("/boot/vmlinuz", kernel.Kernel).
		WithMountedFile("/boot/initrd.img", kernel.Initrd).
		WithMountedFile("/tmp/runner", selfBin)

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
			checkVmxStr, err := img.
				WithMountedFile("/tmp/runner", Self(client)).
				WithExec([]string{
					"/tmp/runner", "checkvmx",
				}).Stdout(ctx)
			if err != nil {
				return fmt.Errorf("error checking if kvm is supported: %w", err)
			}
			ok, err := strconv.ParseBool(strings.TrimSpace(checkVmxStr))
			if err != nil {
				return err
			}
			if !ok {
				noKvm = true
			}
		}
	}

	var (
		kvmOpts     []string
		microvmOpts string
	)
	if !noKvm {
		kvmOpts = []string{"-enable-kvm", "-cpu host"}
		microvmOpts = ",x-option-roms=off,isa-serial=off,rtc=off"
	}

	qemuExec := []string{
		"/tmp/runner", "exec",
		"--docker-socket-path", "/run/docker.sock",
		"/usr/bin/qemu-system-" + *cpuArchFl,
		"-no-reboot",
		"-M", "microvm" + microvmOpts,
		"-kernel", "/boot/vmlinuz",
		"-initrd", "/boot/initrd.img",
		"-append", "console=hvc0 root=/dev/vda rw acpi=off reboot=t panic=-1 quiet - --cgroup-version " + strconv.Itoa(*cgVerFl),
		"-smp", strconv.Itoa(*numCPUFl),
		"-m", *vmMemFl,
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
		"-device", "virtio-net-device,netdev=net0",
		"-netdev", "user,id=net0," + portForwardsToQemuFlag(portForwards) + ",net=192.168.76.0/24,dhcpstart=192.168.76.9",
		"-device", " virtio-net-device,netdev=net0",
		"-add-fd", "fd=3,set=2",
		"-drive", "file=/dev/fdset/2,index=0,media=disk",
		// "-chardev", "socket,path=/tmp/qga/qga.sock,server=on,wait=off,id=qga0",
		// "-device", "virtio-serial-device",
		// "-device", "virtserialport,chardev=qga0,name=org.qemu.guest_agent.0",
	}

	if kvmOpts != nil {
		qemuExec = append(qemuExec, kvmOpts...)
	}

	sockPath := filepath.Join(sockDir, "docker.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{
		Name: sockPath,
		Net:  "unix",
	})
	if err != nil {
		return fmt.Errorf("error setting up forwarded docker socket listener: %w", err)
	}
	defer l.Close()

	_, err = img.WithExec(qemuExec).
		WithUnixSocket("/run/docker.sock", client.Host().UnixSocket(sockPath)).
		ExitCode(ctx)

	return err
}

func portForwardsToQemuFlag(forwards []int) string {
	var out []string
	for _, f := range forwards {
		out = append(out, fmt.Sprintf("tcp::%d-:%d", f, f))
	}
	return strings.Join(out, ",")
}
