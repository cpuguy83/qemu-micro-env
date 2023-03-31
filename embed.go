package main

import (
	"embed"
	"sync"

	"github.com/cpuguy83/qemu-micro-env/build"
	"github.com/moby/buildkit/client/llb"
)

//go:embed go.mod go.sum all:cmd/init all:cmd/entrypoint all:build/vmconfig
var src embed.FS

var (
	fsMu  sync.Mutex
	fsSet bool
	fsLLB llb.State
)

func getEmbedLLB() (llb.State, error) {
	fsMu.Lock()
	defer fsMu.Unlock()

	if fsSet {
		return fsLLB, nil
	}

	st, err := build.GoFSToLLB(src, nil)
	if err != nil {
		return llb.Scratch(), err
	}

	fsLLB = st
	fsSet = true

	return fsLLB, nil
}

func Source() embed.FS {
	return src
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
)

// InitModule builds the "init" binary which is used as the VM init
func InitModule(opts ...BuildModOption) (llb.State, error) {
	var cfg BuildModConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.OutputPath == "" {
		cfg.OutputPath = "/sbin/init"
	}

	st, err := getEmbedLLB()
	if err != nil {
		return llb.Scratch(), err
	}

	return build.Mod(st, initMod, "./cmd/init", cfg.OutputPath), nil
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

	st, err := getEmbedLLB()
	if err != nil {
		return llb.Scratch(), err
	}

	return build.Mod(st, entrypointMod, "./cmd/entrypoint", cfg.OutputPath), nil
}
