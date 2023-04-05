package build

import (
	"strconv"

	"github.com/moby/buildkit/client/llb"
)

func QcowFrom(rootfs llb.State, size int64) File {
	sizeStr := strconv.FormatInt(size, 10)
	rootfsMount := llb.AddMount("/tmp/rootfs", rootfs)
	return NewFile(QemuBase().
		Run(rootfsMount,
			llb.Args([]string{"/bin/sh", "-ec", ` 
			truncate -s` + sizeStr + ` /tmp/rootfs.img
			mkfs.ext4 -d /tmp/rootfs /tmp/rootfs.img
			qemu-img convert /tmp/rootfs.img -O qcow2 /tmp/rootfs.qcow2
			rm /tmp/rootfs.img
		`})).Root(),
		"/tmp/rootfs.qcow2")
}

func QcowDiff(qcow File) File {
	return NewFile(
		QemuBase().
			Run(
				llb.AddMount("/tmp/rootfs", qcow.State(), llb.SourcePath(qcow.Path())),
				llb.Args([]string{
					"/usr/bin/qemu-img",
					"create",
					"-f", "qcow2",
					"-b", "/tmp/rootfs.qcow2",
					"-F", "qcow2",
					"/tmp/rootfs-diff.qcow2",
				}),
			).Root(), "/tmp/rootfs-diff.qcow2")
}

func QemuBase() llb.State {
	return llb.Image(JammyRef).
		Run(llb.Args([]string{
			"/bin/sh", "-c",
			`apt-get update && apt-get install -y \
				qemu \
				qemu-system \
				bash \
				openssh-client \
				socat \
				e2fsprogs \
		`})).Root()
}
