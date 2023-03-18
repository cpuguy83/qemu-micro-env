package build

import (
	"fmt"
	gofs "io/fs"

	"dagger.io/dagger"
	"github.com/cpuguy83/qemu-micro-env/cmd"
)

type GoModuleBuildFn func(*dagger.Client) (*dagger.File, error)

func Modules() map[string]GoModuleBuildFn {
	return map[string]GoModuleBuildFn{
		initMod:       InitModule,
		entrypointMod: EntrypointModule,
	}
}

const (
	initMod       = "init"
	entrypointMod = "entrypoint"
)

func rewriteGoMod(v string) string {
	switch v {
	case "_go.mod":
		return "go.mod"
	case "_go.sum":
		return "go.sum"
	}
	return v
}

func BuildMod(client *dagger.Client, dir *dagger.Directory, name, p string) (*dagger.File, error) {
	return client.Container().
		From("golang:1.20").
		WithMountedDirectory("/opt/build", dir).
		WithWorkdir("/opt/build").
		WithMountedCache("/root/.cache/go-build", client.CacheVolume("go-build-cache")).
		WithMountedCache("/go/pkg/mod", client.CacheVolume("go-mod-cache")).
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-o", name, p}).
		File("/opt/build/" + name), nil
}

// InitModule builds the "init" binary which is used as the VM init
func InitModule(client *dagger.Client) (*dagger.File, error) {
	dir, err := gofs.Sub(cmd.Source(), "init")
	if err != nil {
		return nil, err
	}
	modDir, err := GoFSToDagger(dir, client.Directory(), rewriteGoMod)
	if err != nil {
		return nil, fmt.Errorf("could not convert directory to dagger directory: %w", err)
	}
	return BuildMod(client.Pipeline(initMod), modDir, initMod, ".")
}

// EntrypointModule builds the "entrypoint" binary which is used as the container entrypoint
func EntrypointModule(client *dagger.Client) (*dagger.File, error) {
	dir, err := gofs.Sub(cmd.Source(), "entrypoint")
	if err != nil {
		return nil, err
	}
	modDir, err := GoFSToDagger(dir, client.Directory(), rewriteGoMod)
	if err != nil {
		return nil, fmt.Errorf("could not convert directory to dagger directory: %w", err)
	}
	return BuildMod(client.Pipeline(entrypointMod), modDir, entrypointMod, "./cmd")
}
