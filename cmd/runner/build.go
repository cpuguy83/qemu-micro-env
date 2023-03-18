package main

import (
	"context"

	"dagger.io/dagger"
	"github.com/cpuguy83/qemu-micro-env/build"
	"github.com/cpuguy83/qemu-micro-env/cmd/entrypoint/vmconfig"
)

func WithInit(client *dagger.Client, rootfs *dagger.Directory, path string) (*dagger.Directory, error) {
	if path == "" {
		path = "/sbin/init"
	}

	f, err := build.InitModule(client)
	if err != nil {
		return nil, err
	}
	return rootfs.WithFile(path, f), nil
}

func RunnerImg(ctx context.Context, client *dagger.Client, cfg vmconfig.VMConfig) (*dagger.Container, error) {
	spec, err := build.JammySpec(ctx, client)
	if err != nil {
		return nil, err
	}

	spec.Size = 10 * 1024 * 1024 * 1024
	spec.Rootfs, err = WithInit(client, spec.Rootfs, "/sbin/custom-init")
	if err != nil {
		return nil, err
	}
	spec.Rootfs = client.Container().
		WithRootfs(spec.Rootfs).
		WithExec([]string{"/bin/sh", "-c", "apt-get update && apt-get install -y docker.io"}).
		Rootfs()
	qcow := spec.Build(ctx, client)

	entrypoint, err := build.EntrypointModule(client)
	if err != nil {
		return nil, err
	}

	args := []string{
		"/usr/local/bin/docker-entrypoint",
	}
	args = append(args, cfg.AsFlags()...)

	return build.QemuBase(client).
		WithFile("/usr/local/bin/docker-entrypoint", entrypoint).
		WithEntrypoint(args).
		WithFile("/tmp/rootfs.qcow2", qcow).
		WithFile("/boot/vmlinuz", spec.Kernel.Kernel).
		WithFile("/boot/initrd.img", spec.Kernel.Initrd), nil
}
