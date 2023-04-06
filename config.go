package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	kernelImageContext = "kernel-image"
	initrdImageContext = "initrd-image"
	modulesContext     = "kernel-modules"
)

type specFlag struct {
	scheme string
	ref    string
}

type vmImageConfig struct {
	kernel  specFlag
	initrd  specFlag
	modules specFlag
	rootfs  string
	size    string
}

func (f *specFlag) Set(s string) error {
	if s == "" {
		return nil
	}
	scheme, ref, ok := strings.Cut(s, "://")
	if !ok {
		if _, err := os.Stat(s); err == nil {
			scheme = "local"
			ref = s
			ok = true
		}
		if !ok {
			return fmt.Errorf("invalid format, must be <scheme>://<ref>: %s", s)
		}
	}

	f.scheme = scheme
	f.ref = ref
	return nil
}

func (f *specFlag) isEmpty() bool {
	return f.scheme == "" && f.ref == ""
}

func (f *specFlag) String() string {
	if f.scheme == "" && f.ref == "" {
		return ""
	}
	return f.scheme + "://" + f.ref
}

func getLocalContexts(cfg config) map[string]string {
	var contexts map[string]string
	get := func() map[string]string {
		if contexts != nil {
			return contexts
		}
		contexts = make(map[string]string)
		return contexts
	}

	if cfg.ImageConfig.initrd.scheme == "local" {
		get()[initrdImageContext] = filepath.Dir(cfg.ImageConfig.initrd.ref)
	}
	if cfg.ImageConfig.kernel.scheme == "local" {
		get()[kernelImageContext] = filepath.Dir(cfg.ImageConfig.kernel.ref)
	}
	if cfg.ImageConfig.modules.scheme == "local" {
		get()[modulesContext] = filepath.Dir(cfg.ImageConfig.modules.ref)
	}

	return contexts
}
