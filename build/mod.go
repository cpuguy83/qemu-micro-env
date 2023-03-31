package build

import (
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
)

var GoImageRef = "golang:1.20"

func Mod(modSource llb.State, name, p, target string) llb.State {
	buildOut := filepath.Join("/tmp/output", target)

	res := llb.Image(GoImageRef).
		File(llb.Mkdir("/opt/build", 0755, llb.WithParents(true))).
		Run(
			llb.AddMount("/root/.cache/go-build", llb.Scratch(), llb.AsPersistentCacheDir("go-build-cache", llb.CacheMountShared)),
			llb.AddMount("/go/pkg/mod", llb.Scratch(), llb.AsPersistentCacheDir("go-mod-cache", llb.CacheMountShared)),
			llb.AddMount("/opt/build", modSource),
			llb.AddEnv("CGO_ENABLED", "0"),
			llb.Dir("/opt/build"),
			llb.Args([]string{"/bin/sh", "-c", "/usr/local/go/bin/go build -o " + buildOut + " " + p}),
		).Root()

	return llb.Scratch().File(llb.Copy(res, buildOut, target, createParentsCopyOption{}))
}
