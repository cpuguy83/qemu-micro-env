package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/creack/pty"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type logFormatter struct {
	base *nested.Formatter
}

func (f *logFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	entry.Data["component"] = "init"
	return f.base.Format(entry)
}

func main() {
	cgVerP := flag.Int("cgroup-version", 2, "cgroup version to use (1 or 2)")
	debugConsole := flag.Bool("debug-console", false, "Get shell before init is run")
	debug := flag.Bool("debug", false, "Get shell before init is run")
	authorizedKeysPipe := flag.String("authorized-keys-pipe", "/dev/virtio-ports/authorized_keys", "Pipe to read authorized keys from")

	// remove "-" from begining of args passed by the kernel
	if len(os.Args) > 1 {
		if os.Args[1] == "-" && len(os.Args) > 2 {
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}

	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}})

	flag.Parse()

	if *debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

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

	if data, err := os.ReadFile("/etc/resolv.conf"); err != nil || len(data) == 0 {
		if err := os.WriteFile("/etc/resolv.conf", []byte("nameserver 1.1.1.1"), 0644); err != nil {
			panic(err)
		}
	}

	os.Setenv("PATH", "/bin:/sbin:/usr/bin:/usr/sbin:/usr/local/bin:/usr/local/sbin")
	os.Setenv("HOME", "/root")
	pwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	os.Setenv("PWD", pwd)

	logrus.Info("init: " + strings.Join(os.Args, " "))

	if *debugConsole {
		cmd := exec.Command("/bin/bash")
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
		cmd.Env = append(cmd.Env, "HOME=/root")
		pty, err := pty.Start(cmd)
		if err != nil {
			panic(err)
		}
		defer pty.Close()

		go io.Copy(pty, os.Stdin)
		go io.Copy(os.Stdout, pty)
		if err := cmd.Wait(); err != nil {
			panic(err)
		}
		pty.Close()
	}

	if err := initializeDevRan(); err != nil {
		panic(err)
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
	logrus.Info("mounted cgroups")

	exe := flag.Arg(0)
	var args []string
	if len(flag.Args()) > 1 {
		args = flag.Args()[1:]
	}

	if err := setupNetwork(); err != nil {
		panic(err)
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin

	l := logrus.New()
	l.SetOutput(os.Stderr)
	l.SetFormatter(&nested.Formatter{})
	cmd.Stderr = l.WithField("component", "vm:"+filepath.Base(exe)).Writer()

	go reap()
	ssh()
	if err := setupSSHKeys(*authorizedKeysPipe); err != nil {
		panic(err)
	}

	fmt.Fprintln(os.Stderr, "Welcome to the vm!")

	logrus.Debug("starting command")

	if err := cmd.Run(); err != nil {
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
	logrus.Debug("starting ssh")
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

func initializeDevRan() error {
	os.Setenv("UROOT_NOHWRNG", "1")
	if _, err := os.Stat("/dev/random"); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		logrus.Debug("creating /dev/random")
		if err := unix.Mknod("/dev/random", unix.S_IFCHR|0444, int(unix.Mkdev(1, 8))); err != nil {
			panic(err)
		}
	}
	if _, err := os.Stat("/dev/urandom"); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		logrus.Debug("creating /dev/urandom")
		if err := unix.Mknod("/dev/urandom", unix.S_IFCHR|0444, int(unix.Mkdev(1, 9))); err != nil {
			panic(err)
		}
	}
	return nil
}
