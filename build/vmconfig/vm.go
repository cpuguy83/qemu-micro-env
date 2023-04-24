package vmconfig

import (
	"bufio"
	"flag"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/cpuguy83/go-mod-copies/platforms"
	"golang.org/x/sys/unix"
)

func GetDefaultCPUArch() string {
	p := platforms.DefaultSpec()
	return ArchStringToQemu(p.Architecture)
}

const (
	goAmd64 = "amd64"
	amd64   = "x86_64"
	goArm64 = "arm64"
	arm64   = "aarch64"
	arm     = "arm"
)

func ArchStringToQemu(arch string) string {
	// TODO: This is pretty terrible since it is extremely limited and doesn't support all the possible values.
	arch = strings.ToLower(arch)
	switch arch {
	case goAmd64, amd64:
		return amd64
	case goArm64, arm64:
		return arm64
	case arm:
		return arm
	default:
		panic("unsupported architecture")
	}
}

type VMConfig struct {
	CPUArch       string
	NumCPU        int
	PortForwards  intListFlag
	NoKVM         bool
	RequireKVM    bool
	NoMicro       bool
	CgroupVersion int
	Memory        string
	DebugConsole  bool
	Uid           int
	Gid           int
	InitCmd       string

	// The code around this was remove so it really doesn't do anything right now.
	// Keeping for now as fully removing means trashing code that may still be useful.
	UseVsock       bool
	SocketForwards socketListFlag
}

func (c VMConfig) AsFlags() []string {
	flags := []string{
		"--cgroup-version=" + strconv.Itoa(c.CgroupVersion),
		"--no-kvm=" + strconv.FormatBool(c.NoKVM),
		"--num-cpus=" + strconv.Itoa(c.NumCPU),
		"--memory=" + c.Memory,
		"--cpu-arch=" + ArchStringToQemu(c.CPUArch),
		"--no-micro=" + strconv.FormatBool(c.NoMicro),
		"--debug-console=" + strconv.FormatBool(c.DebugConsole),
		"--uid=" + strconv.Itoa(c.Uid),
		"--gid=" + strconv.Itoa(c.Gid),
		"--require-kvm=" + strconv.FormatBool(c.RequireKVM),
		"--init-cmd", c.InitCmd,
	}
	if len(c.PortForwards) > 0 {
		flags = append(flags, "--vm-port-forward="+strings.Join(convertPortForwards(c.PortForwards), ","))
	}
	if len(c.SocketForwards) > 0 {
		flags = append(flags, "--vm-socket-forward="+strings.Join(c.SocketForwards, ","))
	}
	return flags
}

func AddVMFlags(set *flag.FlagSet, cfg *VMConfig) {
	if set.Lookup("cgroup-version") != nil {
		return
	}
	set.IntVar(&cfg.CgroupVersion, "cgroup-version", 2, "cgroup version to use")
	set.BoolVar(&cfg.NoKVM, "no-kvm", false, "disable KVM")
	set.IntVar(&cfg.NumCPU, "num-cpus", 2, "number of CPUs to use")
	set.StringVar(&cfg.Memory, "memory", "4G", "memory to use for the VM")
	set.StringVar(&cfg.CPUArch, "cpu-arch", GetDefaultCPUArch(), "CPU architecture to use for the VM")
	set.BoolVar(&cfg.NoMicro, "no-micro", false, "disable microVMs - useful for allowing the VM to have access to PCI devices")
	set.Var(&cfg.PortForwards, "vm-port-forward", "port forwards to set up from the VM")
	set.BoolVar(&cfg.DebugConsole, "debug-console", false, "enable debug console")
	set.IntVar(&cfg.Uid, "uid", os.Getuid(), "uid to use for the VM")
	set.IntVar(&cfg.Gid, "gid", os.Getgid(), "gid to use for the VM")
	set.BoolVar(&cfg.RequireKVM, "require-kvm", false, "require KVM to be available (will fail if not available)")
	set.StringVar(&cfg.InitCmd, "init-cmd", "/usr/local/bin/dockerd-init", "command to run in the VM (after pid 1)")
	set.Var(&cfg.SocketForwards, "vm-socket-forward", "socket forwards to set up from the VM (--vm-socket-foroward=<guest path>)")
}

var vmxRegexp = regexp.MustCompile(`flags.*:.*(vmx|svm)`)

func canVirtualize(arch string) bool {
	arch = ArchStringToQemu(arch)
	host := GetDefaultCPUArch()
	if arch == host {
		return true
	}

	// TODO: CPU feature check to ensure the arm64 cpu supports arm instructions natively.
	// While this is normally the case, it is not guaranteed nor is it encoded in the arm64 spec.
	return host == arm64 && arch == arm
}

func CanUseHostCPU(arch string) bool {
	if !canVirtualize(arch) {
		return false
	}

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
