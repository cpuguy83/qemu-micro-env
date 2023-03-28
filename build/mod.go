package build

import (
	"fmt"
	gofs "io/fs"
	"path/filepath"

	"github.com/cpuguy83/qemu-micro-env/cmd"
	"github.com/moby/buildkit/client/llb"
)

var GoImageRef = "golang:1.20"

type GoModuleBuildFn func(...BuildModOption) (llb.State, error)

func Modules() map[string]GoModuleBuildFn {
	return map[string]GoModuleBuildFn{
		initMod:       InitModule,
		entrypointMod: EntrypointModule,
	}
}

const (
	initMod       = "init"
	entrypointMod = "entrypoint"
	runnerMod     = "runner"
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

type BuildModConfig struct {
	OutputPath string
}

func WithOutputPath(p string) BuildModOption {
	return func(cfg *BuildModConfig) {
		cfg.OutputPath = p
	}
}

type BuildModOption func(*BuildModConfig)

func buildMod(modSource llb.State, name, p, target string) (llb.State, error) {
	img := llb.Image(GoImageRef).File(llb.Mkdir("/opt/build", 0755, llb.WithParents(true)))

	st := img.Run(
		llb.AddMount("/root/.cache/go-build", llb.Scratch(), llb.AsPersistentCacheDir("go-build-cache", llb.CacheMountShared)),
		llb.AddMount("/go/pkg/mod", llb.Scratch(), llb.AsPersistentCacheDir("go-mod-cache", llb.CacheMountShared)),
		llb.AddMount("/opt/build", modSource),
		llb.AddEnv("CGO_ENABLED", "0"),
		llb.Args([]string{"/bin/sh", "-c", "cd /opt/build && /usr/local/go/bin/go build -o /tmp/" + name + " " + p}),
	).Root()

	return llb.Scratch().File(llb.Copy(st, "/tmp/"+name, filepath.Join("/", p))), nil
}

// InitModule builds the "init" binary which is used as the VM init
func InitModule(opts ...BuildModOption) (llb.State, error) {
	var cfg BuildModConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.OutputPath == "" {
		cfg.OutputPath = "/sbin/init"
	}

	dir, err := gofs.Sub(cmd.Source(), initMod)
	if err != nil {
		return llb.State{}, err
	}

	modDir, err := GoFSToLLB(dir, rewriteGoMod)
	if err != nil {
		return llb.State{}, fmt.Errorf("could not convert in-memory fs to llb: %w", err)
	}

	return buildMod(modDir, initMod, ".", cfg.OutputPath)
}

// EntrypointModule builds the "entrypoint" binary which is used as the container entrypoint
func EntrypointModule(opts ...BuildModOption) (llb.State, error) {
	var cfg BuildModConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.OutputPath == "" {
		cfg.OutputPath = "/entrypoint"
	}

	dir, err := gofs.Sub(cmd.Source(), entrypointMod)
	if err != nil {
		return llb.State{}, err
	}

	modDir, err := GoFSToLLB(dir, rewriteGoMod)
	if err != nil {
		return llb.State{}, fmt.Errorf("could not convert in-memory fs to llb: %w", err)
	}

	return buildMod(modDir, entrypointMod, "./cmd", cfg.OutputPath)
}

func RunnerModule(opts ...BuildModOption) (llb.State, error) {
	var cfg BuildModConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.OutputPath == "" {
		cfg.OutputPath = "/runner"
	}

	dir, err := gofs.Sub(cmd.Source(), runnerMod)
	if err != nil {
		return llb.State{}, err
	}

	modDir, err := GoFSToLLB(dir, rewriteGoMod)
	if err != nil {
		return llb.State{}, fmt.Errorf("could not convert in-memory fs to llb: %w", err)
	}

	return buildMod(modDir, runnerMod, ".", cfg.OutputPath)
}
