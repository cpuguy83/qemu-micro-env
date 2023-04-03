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
	return llb.Scratch().File(llb.Copy(f.st, f.p, f.p, createParentsCopyOption{}, copyFollowSymlink{}))
}

func (f File) IsEmpty() bool {
	return f.p == ""
}

func NewFile(st llb.State, p string) File {
	return File{st: st, p: p}
}

type Directory struct {
	st llb.State
	p  string
}

func NewDirectory(st llb.State, p string) Directory {
	return Directory{st: st, p: p}
}

func (d Directory) Path() string {
	return d.p
}

func (d Directory) State() llb.State {
	return llb.Scratch().File(llb.Copy(d.st, d.p, d.p, createParentsCopyOption{}, copyDirContentsOnly{}))
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
	if !s.Kernel.Initrd.IsEmpty() {
		states = append(states, s.Kernel.Initrd.State())
	}
	st := llb.Merge(states)
	return QcowFrom(st, s.Size)
}

type createParentsCopyOption struct{}

func (createParentsCopyOption) SetCopyOption(opt *llb.CopyInfo) {
	opt.CreateDestPath = true
}

type copyDirContentsOnly struct{}

func (copyDirContentsOnly) SetCopyOption(opt *llb.CopyInfo) {
	opt.CopyDirContentsOnly = true
}

type copyFollowSymlink struct{}

func (copyFollowSymlink) SetCopyOption(opt *llb.CopyInfo) {
	opt.FollowSymlinks = true
}
