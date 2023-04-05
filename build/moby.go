package build

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/moby/buildkit/client/llb"
)

var MobyRef = "docker:23-dind"

const getCmdPaths = `
mkdir -p /tmp/output
commands="docker dockerd docker-init docker-proxy containerd containerd-shim-runc-v2 runc"
for i in ${commands}; do
	cmd="$(command -v ${i})"
	if [ $? -ne 0 ]; then
		[ -f "/${i}" ] || exit 1
		cmd = "/${i}"
	fi
	mv "${cmd}" "/tmp/output/${i}"
done
`

var MobyKernelMods = []string{
	"br_netfilter",
	"ip_conntrack",
	"ip_tables",
	"ipt_conntrack",
	"ipt_MASQUERADE",
	"ipt_REJECT",
	"ipt_state",
	"iptable_filter",
	"iptable_nat",
	"overlay",
	"nf_conntrack",
	"nf_conntrack_netlink",
	"xt_addrtype",
	"xt_u32",
	"veth",
}

var DockerdInitScriptName = "/usr/local/bin/dockerd-init"

func DockerdInitScript() File {
	b := bytes.NewBuffer(nil)
	b.WriteString("#!/bin/sh\n\n")

	// Don't error out just because the module load fails
	// Dockerd will either fallback or error out on its own
	b.WriteString("modprobe -a " + strings.Join(MobyKernelMods, " ") + "\n\n")

	b.WriteString("echo 1 > /proc/sys/net/ipv4/ip_forward\n")
	b.WriteString("exec dockerd ${@}\n")

	return NewFile(llb.Scratch().
		File(llb.Mkdir("/usr/local/bin", 0755, llb.WithParents(true))).
		File(llb.Mkfile(DockerdInitScriptName, 0777, b.Bytes())), DockerdInitScriptName)
}

func GetMoby(ref string) (llb.State, error) {
	if ref == "" {
		ref = MobyRef
	}

	scheme, parsedRef, ok := strings.Cut("://", ref)
	if !ok {
		parsedRef = ref
		scheme = "docker-image"
	}

	switch scheme {
	case "docker-image":
		// supports docker binaries in either $PATH or in /
		st := llb.Image(parsedRef).
			Run(llb.Args([]string{"/bin/sh", "-ec", getCmdPaths})).Root()
		return llb.Scratch().File(llb.Copy(st, "/tmp/output/", "/usr/local/bin/", createParentsCopyOption{}, copyDirContentsOnly{})), nil
	default:
		return llb.Scratch(), fmt.Errorf("invalid scheme %q", scheme)
	}
}
