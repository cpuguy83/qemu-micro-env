package build

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
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

func BuildKernel(container llb.State, source File, config *File) File {
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
		Run(llb.Args([]string{"/bin/sh", "-c", "cat /tmp/version.mk >> /opt/src/kernel/Makefile"}))

	var opts []llb.RunOption
	if config == nil {
		ctr = ctr.Run(llb.Args([]string{"/bin/sh", "-c", "make defconfig"})).
			Run(llb.Args([]string{"/bin/sh", "-c", "make kvm_guest.config"})).
			Run(llb.Args([]string{"/bin/sh", "-c", "make olddefconfig"}))
	} else {
		opts = append(opts, llb.AddMount("/opt/src/kernel/src/.config", config.State(), llb.Readonly, llb.SourcePath(config.Path())))
	}

	opts = append(opts, llb.Args([]string{
		"/bin/sh", "-c",
		`make -j$(nproc) && make install`,
	}))

	opts = append(opts, llb.AddMount("/root/.cache/ccache", llb.Scratch(), llb.AsPersistentCacheDir("kernel-ccache", llb.CacheMountShared)))
	opts = append(opts, llb.AddEnv("CC", "ccache gcc"))
	ctr = ctr.Run(opts...).
		Run(llb.Args([]string{"/bin/sh", "-c", "ln -s /boot/vmlinuz-$(make printversion) /boot/vmlinuz"}))

	return NewFile(ctr.Root(), "/boot/vmlinuz")
}

func KernelBuildBase() llb.State {
	return llb.Image(JammyRef).
		AddEnv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin").
		AddEnv("DEBIAN_FRONTEND", "noninteractive").
		Run(
			llb.Args([]string{
				"/bin/sh", "-c",
				"apt-get update && apt-get install -y build-essential bc libncurses-dev bison flex libssl-dev libelf-dev ccache",
			}),
		).Root()
}
