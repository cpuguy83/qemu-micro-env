package build

import (
	"github.com/moby/buildkit/client/llb"
)

var JammyRef = "ubuntu:jammy"

func JammySpec() DiskImageSpec {
	st := llb.Image(JammyRef).
		Run(llb.Args([]string{
			"/bin/sh", "-c",
			"apt-get update && apt-get install -y iptables ssh linux-image-kvm",
		})).
		Run(llb.Args([]string{"/usr/bin/update-alternatives", "--set", "iptables", "/usr/sbin/iptables-legacy"})).
		Run(llb.Args([]string{"/bin/sh", "-c", `set -e; kern="$(readlink /boot/vmlinuz)"; initrd="$(readlink /boot/initrd.img)"; rm /boot/vmlinuz; rm /boot/initrd.img; mv "/boot/${kern}" /boot/vmlinuz; mv "/boot/${initrd}" /boot/initrd.img`}))

	kern := llb.Scratch().File(llb.Copy(st, "/boot/vmlinuz", "/boot/vmlinuz"))
	initrd := llb.Scratch().File(llb.Copy(st, "/boot/initrd.img", "/boot/initrd.img"))
	return DiskImageSpec{
		Kernel: Kernel{
			Kernel: NewFile(kern, "/boot/vmlinuz"),
			Initrd: NewFile(initrd, "/boot/initrd.img"),
		},
		Rootfs: st.Root(),
	}
}
