package build

import (
	"github.com/moby/buildkit/client/llb"
)

type File struct {
	st llb.State
	p  string
}

func (f File) Path() string {
	return f.p
}

func (f File) State() llb.State {
	return f.st
}

func NewFile(st llb.State, p string) File {
	return File{st: st, p: p}
}

type Kernel struct {
	Initrd File
	Kernel File
}

type DiskImageSpec struct {
	Kernel Kernel
	Rootfs llb.State
	Size   int64
}

func (s *DiskImageSpec) Build() File {
	states := []llb.State{s.Rootfs, s.Kernel.Kernel.State()}
	if s.Kernel.Initrd.Path() != "" {
		states = append(states, s.Kernel.Initrd.State())
	}
	st := llb.Merge(states)
	return QcowFrom(st, s.Size)
}
