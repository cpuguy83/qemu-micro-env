package build

import (
	"strings"

	"github.com/moby/buildkit/client/llb"
)

var UseMergeOp = true

type File struct {
	st llb.State
	p  string
}

func (f File) Path() string {
	return f.p
}

func (f File) State() llb.State {
	return llb.Scratch().File(llb.Copy(f.st, f.p, f.p, createParentsCopyOption{}, copyFollowSymlink{}, copyWithGlob{f.p}))
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
	return llb.Scratch().File(llb.Copy(d.st, d.p, d.p, createParentsCopyOption{}, copyDirContentsOnly{}, copyFollowSymlink{}))
}

func (d Directory) IsEmpty() bool {
	return d.p == ""
}

type Kernel struct {
	Initrd  File
	Kernel  File
	Modules Directory
	Config  File
}

type DiskImageSpec struct {
	Kernel Kernel
	Rootfs llb.State
	Size   int64
}

func (s *DiskImageSpec) Build() File {
	st := s.Rootfs.File(llb.Copy(s.Kernel.Kernel.State(), s.Kernel.Kernel.Path(), "/boot/vmlinuz", createParentsCopyOption{}))

	if !s.Kernel.Initrd.IsEmpty() {
		st = st.File(llb.Copy(s.Kernel.Initrd.State(), s.Kernel.Initrd.Path(), "/boot/initrd.img", createParentsCopyOption{}))
	}

	if !s.Kernel.Modules.IsEmpty() {
		st = st.File(llb.Copy(s.Kernel.Modules.State(), s.Kernel.Modules.Path(), "/lib/modules", createParentsCopyOption{}, copyDirContentsOnly{}, copyFollowSymlink{}))
	}

	if !s.Kernel.Config.IsEmpty() {
		st = st.File(llb.Copy(s.Kernel.Config.State(), s.Kernel.Config.Path(), "/boot/config", createParentsCopyOption{}))
	}

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

type copyWithGlob struct {
	p string
}

func (c copyWithGlob) SetCopyOption(opt *llb.CopyInfo) {
	opt.AllowWildcard = strings.Contains(c.p, "*")
	opt.AllowEmptyWildcard = true
}
