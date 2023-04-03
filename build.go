package main

import (
	"context"
	"fmt"

	_ "embed"

	"github.com/cpuguy83/qemu-micro-env/build"
	"github.com/docker/go-units"
	"github.com/moby/buildkit/client/llb"
)

const (
	defaultQcowSize   = "10GB"
	entrypointPath    = "/usr/local/bin/docker-entrypoint"
	initPath          = "/sbin/custom-init"
	hostKernelContext = "host-kernel"
)

func mkImage(ctx context.Context, spec *build.DiskImageSpec) (llb.State, error) {
	entrypoint, err := EntrypointModule(WithOutputPath(entrypointPath))
	if err != nil {
		return llb.Scratch(), fmt.Errorf("error generating entrypoint module LLB: %w", err)
	}

	states := []llb.State{
		build.QemuBase(),
		entrypoint,
		spec.Build().State(),
		spec.Kernel.Kernel.State(),
		spec.Kernel.Initrd.State(),
	}
	return llb.Merge(states), nil
}

func specFromFlags(ctx context.Context, cfg vmImageConfig) (*build.DiskImageSpec, error) {
	var (
		spec build.DiskImageSpec
	)

	if cfg.rootfs != "" {
		spec.Rootfs = llb.Image(cfg.rootfs)
	} else {
		initMod, err := InitModule(WithOutputPath("/sbin/custom-init"))
		if err != nil {
			return nil, err
		}

		mobySt, err := build.GetMoby("")
		if err != nil {
			return nil, err
		}
		spec.Rootfs = llb.Merge([]llb.State{build.JammyRootfs(), initMod, mobySt})
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
	llb.Args([]string{
		"/bin/sh", "-c", "apt-get update && apt-get install -y linux-image-kvm",
	}),
).Root()

func getKernel(cfg vmImageConfig) (build.Kernel, error) {
	var k build.Kernel

	if cfg.kernel.isEmpty() {
		k.Kernel = build.NewFile(defaultKernelSt, "/boot/vmlinuz")
	} else {
		switch cfg.kernel.scheme {
		case "rootfs":
			k.Kernel = build.NewFile(llb.Local(hostKernelContext, llb.IncludePatterns([]string{cfg.kernel.ref}), llb.FollowPaths([]string{cfg.kernel.ref})), cfg.kernel.ref)
		case "source":
			src, err := build.GetKernelSource(cfg.kernel.ref)
			if err != nil {
				return k, fmt.Errorf("error getting kernel source: %w", err)
			}
			k.Kernel = build.BuildKernel(build.KernelBuildBase(), src, nil)
		case "docker-image":
			k.Kernel = build.NewFile(llb.Image(cfg.kernel.ref), "/boot/vmlinuz")
		default:
			return k, fmt.Errorf("unsupported scheme for kernel: %s", cfg.kernel.scheme)
		}
	}

	if cfg.initrd.isEmpty() {
		k.Initrd = build.NewFile(defaultKernelSt, "/boot/initrd.img")
	} else {
		switch cfg.initrd.scheme {
		case "rootfs":
			k.Kernel = build.NewFile(llb.Local(hostKernelContext, llb.IncludePatterns([]string{cfg.initrd.ref}), llb.FollowPaths([]string{cfg.initrd.ref})), cfg.initrd.ref)
		case "docker-image":
			k.Kernel = build.NewFile(llb.Image(cfg.kernel.ref), "/boot/initrd.img")
		default:
			return k, fmt.Errorf("unsupported scheme for kernel: %s", cfg.kernel.scheme)
		}
	}

	return k, nil
}
