package build

import (
	"github.com/moby/buildkit/client/llb"
)

var JammyRef = "ubuntu:jammy"

func JammyRootfs() llb.State {
	return llb.Image(JammyRef).
		Run(llb.Args([]string{
			"/bin/sh", "-c",
			"apt-get update && apt-get install -y iptables ssh kmod systemd",
		}),
			llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
		).
		Run(llb.Args([]string{"/usr/bin/update-alternatives", "--set", "iptables", "/usr/sbin/iptables-legacy"})).
		Root()
}
