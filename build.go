package main

import (
	"context"
	"fmt"
	"path/filepath"

	_ "embed"

	"github.com/cpuguy83/qemu-micro-env/build"
	"github.com/docker/go-units"
	"github.com/moby/buildkit/client/llb"
)

const (
	defaultQcowSize   = "10GB"
	entrypointPath    = "/usr/local/bin/docker-entrypoint"
	initPath          = "/sbin/init"
	hostKernelContext = "host-kernel"
)

func mkImage(ctx context.Context, spec *build.DiskImageSpec) (llb.State, error) {
	entrypoint, err := EntrypointModule(WithOutputPath(entrypointPath))
	if err != nil {
		return llb.Scratch(), fmt.Errorf("error generating entrypoint module LLB: %w", err)
	}

	if build.UseMergeOp {
		states := []llb.State{
			build.QemuBase(),
			entrypoint,
			spec.Build().State(),
			spec.Kernel.Kernel.State(),
			spec.Kernel.Initrd.State(),
		}
		return llb.Merge(states), nil
	}

	specFile := spec.Build()
	return build.QemuBase().
		File(llb.Copy(entrypoint, entrypointPath, entrypointPath)).
		File(llb.Copy(specFile.State(), specFile.Path(), specFile.Path())).
		File(llb.Copy(spec.Kernel.Kernel.State(), spec.Kernel.Kernel.Path(), spec.Kernel.Kernel.Path())).
		File(llb.Copy(spec.Kernel.Initrd.State(), spec.Kernel.Initrd.Path(), spec.Kernel.Initrd.Path())), nil
}

func specFromFlags(ctx context.Context, cfg vmImageConfig) (*build.DiskImageSpec, error) {
	var (
		spec build.DiskImageSpec
	)

	if cfg.rootfs != "" {
		spec.Rootfs = llb.Image(cfg.rootfs)
	} else {
		initMod, err := InitModule()
		if err != nil {
			return nil, err
		}

		mobySt, err := build.GetMoby("")
		if err != nil {
			return nil, err
		}
		if build.UseMergeOp {
			spec.Rootfs = llb.Merge([]llb.State{build.JammyRootfs(), initMod, mobySt, build.DockerdInitScript().State()})
		} else {
			script := build.DockerdInitScript()
			spec.Rootfs = build.JammyRootfs().
				File(llb.Copy(initMod, initPath, initPath)).
				File(llb.Copy(mobySt, "/", "/")).
				File(llb.Copy(build.DockerdInitScript().State(), script.Path(), script.Path()))
		}
	}

	var err error
	spec.Kernel, err = getKernel(cfg)
	if err != nil {
		return nil, err
	}

	spec.Size, err = units.FromHumanSize(cfg.size)
	if err != nil {
		return nil, fmt.Errorf("error parsing qcow size: %w", err)
	}

	return &spec, nil
}

var defaultKernelSt = build.JammyRootfs().Run(
	llb.AddEnv("DEBIAN_FRONTEND", "noninteractive"),
	llb.Args([]string{
		"/bin/sh", "-c", "apt-get update && apt-get install -y linux-image-kvm",
	}),
).Root()

func getKernel(cfg vmImageConfig) (build.Kernel, error) {
	var k build.Kernel

	if cfg.kernel.isEmpty() {
		k.Kernel = build.NewFile(defaultKernelSt, "/boot/vmlinuz")
		k.Modules = build.NewDirectory(defaultKernelSt, "/lib/modules")
		k.Config = build.NewFile(defaultKernelSt, "/boot/config-*")
	} else {
		switch cfg.kernel.scheme {
		case "version":
			src, err := build.GetKernelSource(cfg.kernel.ref)
			if err != nil {
				return k, fmt.Errorf("error getting kernel source: %w", err)
			}
			k.Config, k.Kernel, k.Modules = build.BuildKernel(build.KernelBuildBase(), src, nil)
		case "docker-image":
			k.Kernel = build.NewFile(llb.Image(cfg.kernel.ref), "/boot/vmlinuz")
		case "local":
			st := llb.Local(kernelImageContext, llb.FollowPaths([]string{filepath.Base(cfg.kernel.ref)}), llb.IncludePatterns([]string{filepath.Base(cfg.kernel.ref)}))
			k.Kernel = build.NewFile(st, filepath.Base(cfg.kernel.ref)).WithTarget("/boot/vmlinuz")
		default:
			return k, fmt.Errorf("unsupported scheme for kernel: %s", cfg.kernel.scheme)
		}
	}

	if cfg.initrd.isEmpty() {
		k.Initrd = build.NewFile(defaultKernelSt, "/boot/initrd.img")
	} else {
		switch cfg.initrd.scheme {
		case "docker-image":
			k.Kernel = build.NewFile(llb.Image(cfg.initrd.ref), "/boot/initrd.img")
		case "local":
			st := llb.Local(initrdImageContext, llb.FollowPaths([]string{filepath.Base(cfg.initrd.ref)}), llb.IncludePatterns([]string{filepath.Base(cfg.initrd.ref)}))
			k.Initrd = build.NewFile(st, filepath.Base(cfg.initrd.ref)).WithTarget("/boot/initrd.img")
		default:
			return k, fmt.Errorf("unsupported scheme for kernel: %s", cfg.initrd.scheme)
		}
	}

	if cfg.modules.isEmpty() {

	} else {
		switch cfg.modules.scheme {
		case "docker-image":
			k.Modules = build.NewDirectory(llb.Image(cfg.modules.ref), "/lib/modules")
		case "local":
			st := llb.Local(modulesContext, llb.FollowPaths([]string{filepath.Base(cfg.modules.ref)}), llb.IncludePatterns([]string{filepath.Base(cfg.modules.ref)}))
			k.Modules = build.NewDirectory(st, filepath.Base(cfg.modules.ref)).WithTarget("/lib/modules")
		default:
			return k, fmt.Errorf("unsupported scheme for kernel: %s", cfg.modules.scheme)
		}
	}

	return k, nil
}
