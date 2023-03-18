package build

import (
	"strconv"

	"dagger.io/dagger"
)

func QcowFrom(client *dagger.Client, rootfs *dagger.Directory, size int) *dagger.File {
	return QemuBase(client).
		WithMountedDirectory("/tmp/rootfs", rootfs).
		WithExec([]string{"/usr/bin/truncate", "-s", strconv.Itoa(size), "/tmp/rootfs.img"}).
		WithExec([]string{"/sbin/mkfs.ext4", "-d", "/tmp/rootfs", "/tmp/rootfs.img"}).
		WithExec([]string{"/usr/bin/qemu-img", "convert", "/tmp/rootfs.img", "-O", "qcow2", "/tmp/rootfs.qcow2"}).
		File("/tmp/rootfs.qcow2")
}

func QcowDiff(client *dagger.Client, qcow *dagger.File) *dagger.File {
	return QemuBase(client).
		WithMountedFile("/tmp/rootfs/rootfs.qcow2", qcow).
		WithWorkdir("/tmp/rootfs").
		WithExec(
			[]string{"/usr/bin/qemu-img", "create", "-f", "qcow2", "-b", "rootfs.qcow2", "-F", "qcow2", "rootfs-diff.qcow2"},
		).
		File("/tmp/rootfs/rootfs-diff.qcow2")
}

func QemuBase(client *dagger.Client) *dagger.Container {
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
