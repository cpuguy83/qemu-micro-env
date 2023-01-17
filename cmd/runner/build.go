package main

import (
	"strconv"

	"dagger.io/dagger"
)

type Kernel struct {
	Initrd *dagger.File
	Kernel *dagger.File
}

func JammyKernelKVM(client *dagger.Client) Kernel {
	jammy := client.Container().From("ubuntu:jammy").
		WithExec([]string{"/bin/sh", "-c", "apt-get update && apt-get install -y linux-image-kvm"})
	return Kernel{
		Kernel: jammy.File("/boot/vmlinuz"),
		Initrd: jammy.File("/boot/initrd.img"),
	}
}

func AlpineRootfs(client *dagger.Client) *dagger.Directory {
	return client.Container().From("alpine:latest").
		WithExec([]string{"/bin/sh", "-c", "apk add --no-cache docker openssh"}).
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

	f := client.Container().From("golang:1.19").
		WithMountedDirectory("/opt/init", client.Host().Directory("cmd/init")).
		WithEnvVariable("CGO_ENABLED", "0").
		WithMountedCache("/go/pkg/mod", client.CacheVolume("go-pkg")).
		WithMountedCache("/root/.cache/go-build", client.CacheVolume("go-build")).
		WithExec([]string{"/bin/sh", "-c", "cd /opt/init && go build -o /tmp/init"}).
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
	return client.Container().From("golang:1.19").
		WithWorkdir("/opt/project").
		WithMountedDirectory("/opt/project", client.Host().Directory(".")).
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"/usr/local/go/bin/go", "build", "-o", "/tmp/runner", "./cmd/runner"}).
		File("/tmp/runner")
}
