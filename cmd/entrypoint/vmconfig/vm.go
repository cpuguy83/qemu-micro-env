package vmconfig

import (
	"flag"
	"os"
	"strconv"
	"strings"

	"github.com/cpuguy83/go-mod-copies/platforms"
)

func GetDefaultCPUArch() string {
	p := platforms.DefaultSpec()
	return ArchStringToQemu(p.Architecture)
}

func ArchStringToQemu(arch string) string {
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
		"--cpu-arch=" + ArchStringToQemu(c.CPUArch),
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

func AddVMFlags(set *flag.FlagSet, cfg *VMConfig) {
	set.IntVar(&cfg.CgroupVersion, "cgroup-version", 2, "cgroup version to use")
	set.BoolVar(&cfg.NoKVM, "no-kvm", false, "disable KVM")
	set.IntVar(&cfg.NumCPU, "num-cpus", 2, "number of CPUs to use")
	set.StringVar(&cfg.Memory, "memory", "4G", "memory to use for the VM")
	set.StringVar(&cfg.CPUArch, "cpu-arch", GetDefaultCPUArch(), "CPU architecture to use for the VM")
	set.BoolVar(&cfg.NoMicro, "no-micro", false, "disable microVMs - useful for allowing the VM to have access to PCI devices")
	set.Var(&cfg.PortForwards, "vm-port-forward", "port forwards to set up from the VM")
	set.BoolVar(&cfg.DebugConsole, "debug-console", false, "enable debug console")
	set.BoolVar(&cfg.UseVsock, "vsock", false, "use vsock for communication")
	set.IntVar(&cfg.Uid, "uid", os.Getuid(), "uid to use for the VM")
	set.IntVar(&cfg.Gid, "gid", os.Getgid(), "gid to use for the VM")
}
