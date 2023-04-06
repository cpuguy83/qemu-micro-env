package build

import (
	"strings"

	"github.com/moby/buildkit/client/llb"
)

var UseMergeOp = true

type File struct {
	st     llb.State
	p      string
	target string
}

func (f File) Path() string {
	return f.p
}

func (f File) Target() string {
	if f.target == "" {
		return f.p
	}
	return f.target
}

func (f File) State() llb.State {
	return f.CopyTo(llb.Scratch())
}

func (f File) CopyTo(st llb.State) llb.State {
	return st.File(llb.Copy(f.st, f.Path(), f.Target(), createParentsCopyOption{}, copyFollowSymlink{}, copyWithGlob{f.p}))
}

func (f File) IsEmpty() bool {
	return f.p == ""
}

func (f File) WithTarget(target string) File {
	return File{st: f.st, p: f.p, target: target}
}

func NewFile(st llb.State, p string) File {
	return File{st: st, p: p}
}

type Directory struct {
	st     llb.State
	p      string
	target string
}

func NewDirectory(st llb.State, p string) Directory {
	return Directory{st: st, p: p}
}

func (d Directory) Path() string {
	return d.p
}

func (d Directory) State() llb.State {
	return d.CopyTo(llb.Scratch())
}

func (d Directory) CopyTo(st llb.State) llb.State {
	return st.File(llb.Copy(d.st, d.Path(), d.Target(), createParentsCopyOption{}, copyDirContentsOnly{}, copyFollowSymlink{}))
}

func (d Directory) IsEmpty() bool {
	return d.p == ""
}

func (d Directory) WithTarget(target string) Directory {
	return Directory{st: d.st, p: d.p, target: target}
}

func (d Directory) Target() string {
	if d.target == "" {
		return d.p
	}
	return d.target
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
	st := s.Rootfs
	// TODO: It'd be great to not have to bake this into the qcow image because this adds a lot of overhead anytime the modules change.
	if !s.Kernel.Modules.IsEmpty() {
		st = s.Kernel.Modules.CopyTo(st)
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
