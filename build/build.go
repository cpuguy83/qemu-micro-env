package build

import (
	"context"

	"dagger.io/dagger"
)

type Kernel struct {
	Initrd *dagger.File
	Kernel *dagger.File
}

type VMSpec struct {
	Kernel Kernel
	Rootfs *dagger.Directory
	Size   int64
}

func (s *VMSpec) Build(ctx context.Context, client *dagger.Client) *dagger.File {
	return QcowFrom(client, client.Container().WithRootfs(s.Rootfs).
		WithFile("/boot/vmlinuz", s.Kernel.Kernel).
		WithFile("/boot/initrd.img", s.Kernel.Initrd).
		Rootfs(),
		int(s.Size),
	)
}
