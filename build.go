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
	defaultQcowSize = "10GB"
	entrypointPath  = "/usr/local/bin/docker-entrypoint"
	initPath        = "/sbin/custom-init"
)

func mkImage(ctx context.Context, spec *build.DiskImageSpec) (llb.State, error) {
	entrypoint, err := EntrypointModule(WithOutputPath(entrypointPath))
	if err != nil {
		return llb.Scratch(), err
	}

	return llb.Merge([]llb.State{build.QemuBase(), entrypoint, spec.Build().State(), spec.Kernel.Kernel.State(), spec.Kernel.Initrd.State()}), nil
}

func specFromFlags(ctx context.Context, cfg vmImageConfig) (*build.DiskImageSpec, error) {
	var spec build.DiskImageSpec

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
		spec.Rootfs = llb.Merge([]llb.State{build.JammySpec().Rootfs, initMod, mobySt})
	}

	spec.Kernel.Kernel = kernelSpecToFile(ctx, cfg.kernel, spec.Rootfs, "/boot/vmlinuz")
	spec.Kernel.Initrd = kernelSpecToFile(ctx, cfg.initrd, spec.Rootfs, "/boot/initrd.img")

	var err error
	spec.Size, err = units.FromHumanSize(cfg.size)
	if err != nil {
		return nil, fmt.Errorf("error parsing qcow size: %w", err)
	}

	return &spec, nil
}

func kernelSpecToFile(ctx context.Context, spec kernelSpecFlag, rootfs llb.State, path string) build.File {
	switch spec.scheme {
	// case "file":
	// 	return client.Host().Directory(filepath.Dir(spec.ref)).File(filepath.Base(spec.ref))
	case "rootfs":
		return build.NewFile(rootfs, spec.ref)
	case "docker-image":
		return build.NewFile(llb.Image(spec.ref), path)
	case "":
		return build.NewFile(rootfs, path)
	default:
		panic("unknown scheme")
	}
}
