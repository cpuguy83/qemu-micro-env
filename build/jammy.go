package build

import (
	"context"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
)

func JammySpec(ctx context.Context, client *dagger.Client) (VMSpec, error) {
	kern, err := JammyKernelKVM(ctx, client)
	if err != nil {
		return VMSpec{}, err
	}
	return VMSpec{
		Kernel: kern,
		Rootfs: JammyRootfs(client),
	}, nil
}

func JammyKernelKVM(ctx context.Context, client *dagger.Client) (Kernel, error) {
	jammy := client.Container().From("ubuntu:jammy").
		WithExec([]string{"/bin/sh", "-c", "apt-get update && apt-get install -y linux-image-kvm"})

	kern, err := jammy.WithExec([]string{"/usr/bin/readlink", "/boot/vmlinuz"}).Stdout(ctx)
	if err != nil {
		return Kernel{}, err
	}

	initrd, err := jammy.WithExec([]string{"/usr/bin/readlink", "/boot/initrd.img"}).Stdout(ctx)
	if err != nil {
		return Kernel{}, err
	}

	return Kernel{
		Kernel: jammy.File(filepath.Join("/boot", strings.TrimSpace(kern))),
		Initrd: jammy.File(filepath.Join("/boot", strings.TrimSpace(initrd))),
	}, nil
}

func JammyRootfs(client *dagger.Client) *dagger.Directory {
	return client.Container().From("ubuntu:jammy").
		WithExec([]string{"/bin/sh", "-c", "apt-get update && apt-get install -y iptables ssh linux-image-kvm"}).
		WithExec([]string{"/usr/bin/update-alternatives", "--set", "iptables", "/usr/sbin/iptables-legacy"}).
		Rootfs()
}
