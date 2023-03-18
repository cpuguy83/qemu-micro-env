package build

import (
	"fmt"
	"strconv"
	"strings"

	"dagger.io/dagger"
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

func GetKernelSource(client *dagger.Client, version string) (*dagger.File, error) {
	ver, err := ParseKernelVersion(version)
	if err != nil {
		return nil, err
	}

	const (
		rcPattern = "https://git.kernel.org/torvalds/t/linux-%d.%d-rc%d.tar.gz"
		gaPattern = "https://cdn.kernel.org/pub/linux/kernel/v%d.x/linux-%s.tar.gz"
	)
	if ver.IsRC {
		return client.HTTP(fmt.Sprintf(rcPattern, ver.Major, ver.Minor, ver.RC)), nil
	}
	return client.HTTP(fmt.Sprintf(gaPattern, ver.Major, version)), nil
}

func BuildKernel(container *dagger.Container, source, config *dagger.File) *dagger.File {
	version := `
.PHONY: printversion
printversion:
	@echo $(KERNELVERSION)
`
	ctr := container.
		WithMountedFile("/opt/src/kernel/kernel.tar.gz", source).
		WithWorkdir("/opt/src/kernel").
		WithExec([]string{"/bin/sh", "-c", "mkdir src && tar -C src --strip-components=1 -xzf kernel.tar.gz"}).
		WithWorkdir("/opt/src/kernel/src").
		WithExec([]string{"/bin/sh", "-c", "cat - >> Makefile"}, dagger.ContainerWithExecOpts{Stdin: version})

	if config == nil {
		ctr = ctr.WithExec([]string{"/bin/sh", "-c", "make tinyconfig"})
	} else {
		ctr = ctr.WithMountedFile("/opt/src/kernel/src/.config", config)
	}

	return ctr.WithExec([]string{"/bin/sh", "-c", "make -j$(nproc) && make install"}).
		WithExec([]string{"/bin/sh", "-c", `mv /boot/vmlinuz-"$(make printversion)" /boot/vmlinuz`}).
		File("/boot/vmlinuz")
}
