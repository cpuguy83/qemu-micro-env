package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"dagger.io/dagger"
)

type Kernel struct {
	Initrd *dagger.File
	Kernel *dagger.File
}

func JammyKernelKVM(ctx context.Context, client *dagger.Client) (Kernel, error) {
	jammy := client.Container().From("ubuntu:jammy").
		WithExec([]string{"/bin/sh", "-c", "apt-get update && apt-get install -y linux-image-kvm"})

	kern, err := jammy.WithExec([]string{"/usr/bin/readlink", "/boot/vmlinuz"}).Stdout(ctx)
	if err != nil {
		return Kernel{}, err
	}

	initrd, err := jammy.WithExec([]string{"/usr/bin/readlink", "/boot/initrd.img"}).Stdout(ctx)
	if err != nil {
		return Kernel{}, err
	}

	return Kernel{
		Kernel: jammy.File(filepath.Join("/boot", strings.TrimSpace(kern))),
		Initrd: jammy.File(filepath.Join("/boot", strings.TrimSpace(initrd))),
	}, nil
}

func AlpineRootfs(client *dagger.Client) *dagger.Directory {
	return client.Container().From("alpine:latest").
		WithMountedCache("/var/cache/apk", client.CacheVolume("var-cache-apk")).
		WithExec([]string{"/sbin/apk", "add", "docker", "openssh", "iptables"}).
		WithExec([]string{"/bin/mkdir", "/lib/modules"}).
		Rootfs()
}

func JammyRootfs(client *dagger.Client) *dagger.Directory {
	return client.Container().From("ubuntu:jammy").
		WithExec([]string{"/bin/sh", "-c", "apt-get update && apt-get install -y docker.io iptables ssh linux-image-kvm"}).
		WithExec([]string{"/usr/bin/update-alternatives", "--set", "iptables", "/usr/sbin/iptables-legacy"}).
		Rootfs()
}

type InitBuildConfig struct {
	GoBuildCache *dagger.CacheVolume
	GoModCache   *dagger.CacheVolume
}

func WithInit(client *dagger.Client, rootfs *dagger.Directory, path string) *dagger.Directory {
	if path == "" {
		path = "/sbin/init"
	}

	initDir := client.Host().Directory("cmd/init")

	f := client.Container().From("golang:1.20").
		WithMountedCache("/go/pkg/mod", client.CacheVolume("go-pkg")).
		WithMountedCache("/root/.cache/go-build", client.CacheVolume("go-build")).
		WithMountedFile("/tmp/build/go.mod", initDir.File("go.mod")).
		WithMountedFile("/tmp/build/go.sum", initDir.File("go.sum")).
		WithWorkdir("/tmp/build").
		WithExec([]string{"/usr/local/go/bin/go", "mod", "download"}).
		WithMountedDirectory("/opt/init", initDir).
		WithEnvVariable("CGO_ENABLED", "0").
		WithWorkdir("/opt/init").
		WithExec([]string{"/usr/local/go/bin/go", "build", "-o", "/tmp/init"}).
		File("/tmp/init")
	return rootfs.WithFile(path, f)
}

func QemuImg(client *dagger.Client) *dagger.Container {
	return client.Container().From("alpine:3.17").
		WithMountedCache("/var/cache/apk", client.CacheVolume("var-cache-apk")).
		WithExec(
			[]string{"/sbin/apk", "add",
				"qemu",
				"qemu-tools",
				"qemu-img",
				"qemu-system-x86_64",
				"qemu-system-aarch64",
				"qemu-system-arm",
				"bash",
				"openssh-client",
				"socat",
				"e2fsprogs",
			},
		)
}

func MakeQcow(client *dagger.Client, rootfs *dagger.Directory, size int) *dagger.File {
	return QemuImg(client).
		WithMountedDirectory("/tmp/rootfs", rootfs).
		WithExec([]string{"/usr/bin/truncate", "-s", strconv.Itoa(size), "/tmp/rootfs.img"}).
		WithExec([]string{"/sbin/mkfs.ext4", "-d", "/tmp/rootfs", "/tmp/rootfs.img"}).
		WithExec([]string{"/usr/bin/qemu-img", "convert", "/tmp/rootfs.img", "-O", "qcow2", "/tmp/rootfs.qcow2"}).
		File("/tmp/rootfs.qcow2")
}

func MakeQcowDiff(client *dagger.Client, qcow *dagger.File) *dagger.File {
	return QemuImg(client).
		WithMountedFile("/tmp/rootfs/rootfs.qcow2", qcow).
		WithWorkdir("/tmp/rootfs").
		WithExec(
			[]string{"/usr/bin/qemu-img", "create", "-f", "qcow2", "-b", "rootfs.qcow2", "-F", "qcow2", "rootfs-diff.qcow2"},
		).
		File("/tmp/rootfs/rootfs-diff.qcow2")
}

func Self(client *dagger.Client) *dagger.File {
	dir := client.Host().Directory("cmd/runner")
	return client.Container().From("golang:1.20").
		WithMountedCache("/go/pkg/mod", client.CacheVolume("go-pkg")).
		WithMountedCache("/root/.cache/go-build", client.CacheVolume("go-build")).
		WithMountedFile("/tmp/build/go.mod", dir.File("go.mod")).
		WithMountedFile("/tmp/build/go.sum", dir.File("go.sum")).
		WithWorkdir("/tmp/build").
		WithExec([]string{"/usr/local/go/bin/go", "mod", "download"}).
		WithMountedDirectory("/opt/project", dir).
		WithEnvVariable("CGO_ENABLED", "0").
		WithWorkdir("/opt/project").
		WithExec([]string{"/usr/local/go/bin/go", "build", "-o", "/tmp/runner", "."}).
		File("/tmp/runner")
}
