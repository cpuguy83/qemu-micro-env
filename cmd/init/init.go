package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"time"

	nested "github.com/antonfisher/nested-logrus-formatter"
	dhcp "github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func main() {
	cgVerP := flag.Int("cgroup-version", 2, "cgroup version to use (1 or 2)")
	debugConsole := flag.Bool("debug", false, "Get shell before init is run")
	authorizedKeysPipe := flag.String("authorized-keys-pipe", "/dev/virtio-ports/authorized_keys", "Pipe to read authorized keys from")

	// remove "-" from begining of args passed by the kernel
	if len(os.Args) > 1 {
		if os.Args[1] == "-" && len(os.Args) > 2 {
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}

	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&nested.Formatter{})

	flag.Parse()

	switch flag.Arg(0) {
	case "_check":
		set := flag.NewFlagSet("_check", flag.ExitOnError)
		checkFl := set.String("text", "yes this is it!", "text to print back")

		if len(flag.Args()) > 1 {
			if err := set.Parse(flag.Args()[1:]); err != nil {
				panic(err)
			}
		}
		fmt.Println(*checkFl)
		return
	}

	os.Setenv("PATH", "/bin:/sbin:/usr/bin:/usr/sbin")
	os.Setenv("HOME", "/root")
	pwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	os.Setenv("PWD", pwd)

	logrus.Info("init: " + strings.Join(os.Args, " "))

	if *debugConsole {
		cmd := exec.Command("/bin/bash")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}

	cgVer := *cgVerP
	logrus.WithField("cgroup version", cgVer).Info("starting init")
	switch cgVer {
	case 1:
		mountCgroupV1()
	case 2:
		mountCgroupV2()
	default:
		panic("invalid value for cgroup-version")
	}

	setupNetwork()

	logrus.Info(os.Environ())

	cmd := exec.Command("/usr/bin/dockerd")
	cmd.Env = append(cmd.Env, "PATH=/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin")
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin

	l := logrus.New()
	l.SetOutput(os.Stderr)
	l.SetFormatter(&nested.Formatter{})
	cmd.Stderr = l.WithField("component", "dockerd").Writer()

	go reap()
	ssh()
	if err := setupSSHKeys(*authorizedKeysPipe); err != nil {
		panic(err)
	}

	fmt.Fprintln(os.Stderr, "Welcome to the vm!")

	if err := cmd.Run(); err != nil {
		panic(err)
	}
}

func setupNetwork() {
	link, err := netlink.LinkByName("eth0")
	if err != nil {
		panic(err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		panic(err)
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		panic(err)
	}
	netlink.LinkSetUp(lo)

	client, err := dhcp.New("eth0")
	if err != nil {
		panic(err)
	}
	defer client.Close()

	lease, err := client.Request(context.Background())
	if err != nil {
		panic(err)
	}
	defer client.Release(lease)

	err = netlink.AddrAdd(link, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   lease.ACK.YourIPAddr,
			Mask: lease.ACK.SubnetMask(),
		},
		Label:     "eth0",
		Flags:     int(lease.ACK.Flags),
		Broadcast: lease.ACK.BroadcastAddress(),
	})
	if err != nil {
		panic(err)
	}

	if len(lease.ACK.DNS()) > 0 {
		b := strings.Builder{}
		for _, addr := range lease.ACK.DNS() {
			b.WriteString("nameserver " + addr.String() + "\n")
		}
		if err := os.WriteFile("/etc/resolv.conf", []byte(b.String()), 0644); err != nil {
			panic(err)
		}
	}

	if err := netlink.RouteAdd(&netlink.Route{
		Gw: lease.ACK.ServerIPAddr,
	}); err != nil {
		panic(err)
	}
}

func mountCgroupV1() {
	if err := mount("tmpfs", "/sys/fs/cgroup", "tmpfs", 0, ""); err != nil {
		panic(err)
	}

	f, err := os.Open("/proc/cgroups")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		split := strings.Fields(scanner.Text())
		if strings.HasPrefix(split[0], "#") {
			// skip header
			continue
		}

		cg := split[0]
		enabled := split[len(split)-1]
		ok, err := strconv.ParseBool(enabled)
		if err != nil {
			panic(err)
		}
		if !ok {
			continue
		}

		if err := mount("cgroup", "/sys/fs/cgroup/"+cg, "cgroup", 0, cg); err != nil {
			panic(err)
		}
	}
}

func mountCgroupV2() {
	if err := mount("cgroup2", "/sys/fs/cgroup", "cgroup2", 0, ""); err != nil {
		panic(err)
	}
}

func mount(source, target, fs string, flags uintptr, data string) error {
	if err := unix.Mount(source, target, fs, flags, data); err != nil {
		if !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("error mounting %s: %w", target, err)
		}
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		if err := unix.Mount(source, target, fs, flags, data); err != nil {
			return err
		}
	}
	return nil
}

func reap() {
	var status unix.WaitStatus
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, unix.SIGCHLD)

	for range ch {
		pid, err := unix.Wait4(-1, &status, 0, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "INIT: error calling wait4:", err)
			continue
		}
		if pid == 1 {
			unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
		}
	}
}

func ssh() {
	cmd := exec.Command("/usr/sbin/sshd", "-D")
	cmd.Stdout = os.Stdout

	if err := os.Mkdir("/run/sshd", 0600); err != nil {
		panic(err)
	}

	cmd.Stderr = logrus.WithField("component", "sshd").Writer()

	if err := cmd.Start(); err != nil {
		panic(err)
	}
}

func setupSSHKeys(pipe string) error {
	f, err := os.OpenFile(pipe, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("error opening ssh key: %w", err)
	}

	rdr := bufio.NewReader(f)
	var (
		line []byte
	)

	for {
		logrus.Info("waiting for ssh key")
		line, err = rdr.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return fmt.Errorf("error reading ssh key: %w", err)
		}
		break
	}

	if err := os.MkdirAll("/root/.ssh", 0700); err != nil {
		return fmt.Errorf("error creating /root/.ssh directory: %w", err)
	}

	if err := os.WriteFile("/root/.ssh/authorized_keys", line, 0600); err != nil {
		return fmt.Errorf("error writing authorized_keys: %w", err)
	}
	logrus.Info("wrote authorized_keys")
	return nil
}
