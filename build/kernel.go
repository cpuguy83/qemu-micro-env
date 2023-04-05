package build

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/util/system"
)

type KernelVersion struct {
	Major int
	Minor int
	Patch int
	RC    int
	IsRC  bool
}

var errKernelVersionInvalid = fmt.Errorf("invalid kernel version")

func ParseKernelVersion(version string) (KernelVersion, error) {
	split := strings.Split(version, ".")
	if len(split) < 2 {
		return KernelVersion{}, fmt.Errorf("%w: %s: %s", errKernelVersionInvalid, split, version)
	}

	major, err := strconv.Atoi(split[0])
	if err != nil {
		return KernelVersion{}, fmt.Errorf("%w: error parsing major version: %s", errKernelVersionInvalid, split[0])
	}

	var minor int
	if len(split) == 3 {
		minor, err = strconv.Atoi(split[1])
		if err != nil {
			return KernelVersion{}, fmt.Errorf("%w: error parsing minor version: %s", errKernelVersionInvalid, split[1])
		}

		patch, err := strconv.Atoi(split[2])
		if err != nil {
			return KernelVersion{}, fmt.Errorf("%w: error parsing patch version: %s", errKernelVersionInvalid, split[2])
		}
		return KernelVersion{
			Major: major,
			Minor: minor,
			Patch: patch,
		}, nil
	}

	split = strings.Split(split[1], "-")
	if len(split) == 1 {
		return KernelVersion{}, fmt.Errorf("invalid kernel version: %s", version)
	}
	minor, err = strconv.Atoi(split[0])
	if err != nil {
		return KernelVersion{}, fmt.Errorf("invalid kernel version: %s", version)
	}
	if !strings.HasPrefix(split[1], "rc") {
		return KernelVersion{}, fmt.Errorf("invalid kernel version: %s", version)
	}

	if len(split[1]) < 3 {
		return KernelVersion{}, fmt.Errorf("invalid kernel version: %s", version)
	}

	rc, err := strconv.Atoi(split[1][2:])
	if err != nil {
		return KernelVersion{}, fmt.Errorf("invalid kernel version: %s", version)
	}

	return KernelVersion{
		Major: major,
		Minor: minor,
		RC:    rc,
		IsRC:  true,
	}, nil
}

func GetKernelSource(version string) (File, error) {
	ver, err := ParseKernelVersion(version)
	if err != nil {
		return File{}, err
	}

	const (
		rcPattern = "https://git.kernel.org/torvalds/t/linux-%d.%d-rc%d.tar.gz"
		gaPattern = "https://cdn.kernel.org/pub/linux/kernel/v%d.x/linux-%s.tar.gz"
	)
	var url string
	if ver.IsRC {
		url = fmt.Sprintf(rcPattern, ver.Major, ver.Minor, ver.RC)
	} else {
		url = fmt.Sprintf(gaPattern, ver.Major, version)
	}
	return NewFile(llb.HTTP(url, llb.Filename("kernel.tar.gz")), "/kernel.tar.gz"), nil
}

// BaseKernelOptions are used when building the kernel w/o a custom config.
// We take the minimal `tinyconfig` and add these on top.
var BaseKernelOptions = map[string]string{
	"CONFIG_BINFMT_ELF":                   "y",
	"CONFIG_BLOCK":                        "y",
	"CONFIG_BLK_DEV":                      "y",
	"CONFIG_BRIDGE":                       "y",
	"CONFIG_BRIDGE_NETFILTER":             "y",
	"CONFIG_BRIDGE_VLAN_FILTERING":        "y",
	"CONFIG_CPUSETS":                      "y",
	"CONFIG_CGROUPS":                      "y",
	"CONFIG_CGROUP_BPF":                   "y",
	"CONFIG_CGROUP_CPUACCT":               "y",
	"CONFIG_CGROUP_DEVICE":                "y",
	"CONFIG_CGROUP_FREEZER":               "y",
	"CONFIG_CGROUP_PIDS":                  "y",
	"CONFIG_CGROUP_SCHED":                 "y",
	"CONFIG_INET_ESP":                     "m",
	"CONFIG_IPC_NS":                       "y",
	"CONFIG_IP_NF_FILTER":                 "y",
	"CONFIG_IP_NF_NAT":                    "y",
	"CONFIG_IP_NF_TARGET_MASQUERADE":      "y",
	"CONFIG_IP_VALN":                      "m",
	"CONFIG_IP_VS":                        "m",
	"CONFIG_IP_VS_RR":                     "y",
	"CONFIG_KEYS":                         "y",
	"CONFIG_MACVLAN":                      "m",
	"CONFIG_MEMCG":                        "y",
	"CONFIG_MEMCG_SWAP":                   "y",
	"CONFIG_MODULES":                      "y",
	"CONFIG_NAMESPACES":                   "y",
	"CONFIG_NET":                          "y",
	"CONFIG_NETFILTER_XT_MATCH_ADDRTYPE":  "y",
	"CONFIG_NETFILTER_XT_MATCH_CONNTRACK": "y",
	"CONFIG_NETFILTER_XT_MATCH_IPVS":      "y",
	"CONFIG_NETFILTER_XT_MARK":            "y",
	"CONFIG_NETDEVICES":                   "y",
	"CONFIG_NET_CORE":                     "y",
	"CONFIG_NET_NS":                       "y",
	"CONFIG_OVERLAY_FS":                   "m",
	"CONFIG_PID_NS":                       "y",
	"CONFIG_POSIX_MQUEUE":                 "y",
	"CONFIG_TTY":                          "y",
	"CONFIG_UTS_NS":                       "y",
	"CONFIG_USER_NS":                      "y",
	"CONFIG_VETH":                         "y",
	"CONFIG_VXLAN":                        "m",
	"CONFIG_XFRM":                         "y",
	"CONFIG_9P_FS":                        "y",
	"CONFIG_DEBUG_KERNEL":                 "y",
	"CONFIG_DRM_VIRTIO_GPU":               "y",
	"CONFIG_HYPERVISOR_GUEST":             "y",
	"CONFIG_INET":                         "y",
	"CONFIG_IP_PNP":                       "y",
	"CONFIG_IP_PNP_DHCP":                  "y",
	"CONFIG_KVM_GUEST":                    "y",
	"CONFIG_NET_9P":                       "y",
	"CONFIG_NET_9P_VIRTIO":                "y",
	"CONFIG_NETWORK_FILESYSTEMS":          "y",
	"CONFIG_PCI":                          "y",
	"CONFIG_PCI_MSI":                      "y",
	"CONFIG_PARAVIRT":                     "y",
	"CONFIG_S390_GUEST":                   "y",
	"CONFIG_SCSCI_LOWLEVEL":               "y",
	"CONFIG_SCSCI_VIRTIO":                 "y",
	"CONFIG_SERIAL_8250":                  "y",
	"CONFIG_SERIAL_8250_CONSOLE":          "y",
	"CONFIG_VIRTUALIZATION":               "y",
	"CONFIG_VIRTIO":                       "y",
	"CONFIG_VIRTIO_BLK":                   "y",
	"CONFIG_VIRTIO_CONSOLE":               "y",
	"CONFIG_VIRTIO_INPUT":                 "y",
	"CONFIG_VIRTIO_MENU":                  "y",
	"CONFIG_VIRTIO_NET":                   "y",
}

func BuildKernel(container llb.State, source File, config *File) (kernelCfg File, vmlinuz File, modules Directory) {
	const version = `
.PHONY: printversion
printversion:
	@echo $(KERNELVERSION)
`

	ctr := container.
		AddEnv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin").
		Run(llb.Shlex("mkdir -p /opt/src/kernel")).
		Dir("/opt/src/kernel").
		Run(
			llb.AddMount("/opt/src/kernel.tar.gz", source.State(), llb.Readonly, llb.SourcePath("/kernel.tar.gz")),
			llb.Shlex("tar -C /opt/src/kernel --strip-components=1 -xzf /opt/src/kernel.tar.gz"),
		).
		File(llb.Mkfile("/tmp/version.mk", 0644, []byte(version))).
		Run(llb.Args([]string{"/bin/sh", "-c", "cat /tmp/version.mk >> /opt/src/kernel/Makefile"})).Root()

	if config == nil {
		s := &strings.Builder{}
		for k, v := range BaseKernelOptions {
			s.WriteString("echo " + k + "=" + v + " >> .config\n")
		}
		ctr = ctr.Run(llb.Args([]string{"/bin/sh", "-c", "make tinyconfig"})).
			Run(llb.Args([]string{"/bin/sh", "-c", s.String()})).
			Run(llb.Args([]string{"/bin/sh", "-c", "make olddefconfig"})).Root()

	} else {
		ctr = ctr.File(llb.Copy(config.State(), config.Path(), "/opt/src/kernel/src/.config"))
	}

	ctr = ctr.Run(
		llb.AddMount("/root/.cache/ccache", llb.Scratch(), llb.AsPersistentCacheDir("kernel-ccache", llb.CacheMountShared)),
		llb.AddEnv("CC", "ccache gcc"),
		llb.AddEnv("PATH", system.DefaultPathEnvUnix),
		llb.Args([]string{"/bin/sh", "-c", "make -j$(nproc) && make install"}),
	).
		Run(llb.Args([]string{"/bin/sh", "-c", "ln -s /boot/vmlinuz-$(make printversion) /boot/vmlinuz"})).Root()

	f := NewFile(ctr, "/boot/vmlinuz")
	kernelCfg = NewFile(ctr, "/opt/src/kernel/.config")

	// Install the modules if they exist
	mods := ctr.Run(llb.Args([]string{"/bin/sh", "-c", "make modules_install || mkdir /lib/modules"})).Root()

	dir := NewDirectory(mods, "/lib/modules")

	return kernelCfg, f, dir
}

func KernelBuildBase() llb.State {
	return llb.Image(JammyRef).
		AddEnv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin").
		AddEnv("DEBIAN_FRONTEND", "noninteractive").
		Run(
			llb.Args([]string{
				"/bin/sh", "-c",
				"apt-get update && apt-get install -y build-essential bc libncurses-dev bison flex libssl-dev libelf-dev ccache kmod rsync",
			}),
		).Root()
}
