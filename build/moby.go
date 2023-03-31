package build

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/client/llb"
)

var MobyRef = "docker:23-dind"

const getCmdPaths = `
mkdir -p /tmp/output
commands="docker dockerd containerd containerd-shim-runc-v2 runc"
for i in ${commands}; do
	cmd="$(command -v ${i})"
	if [ $? -ne 0 ]; then
		[ -f "/${i}" ] || exit 1
		cmd = "/${i}"
	fi
	mv "${cmd}" "/tmp/output/${i}"
done
`

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
