package main

import (
	"fmt"
	"strings"

	"github.com/cpuguy83/qemu-micro-env/build"
)

type kernelSpecFlag struct {
	scheme string
	ref    string
}

type vmImageConfig struct {
	kernel kernelSpecFlag
	initrd kernelSpecFlag
	rootfs string
	size   string
}

func (f *kernelSpecFlag) Set(s string) error {
	if s == "" {
		return nil
	}
	scheme, ref, ok := strings.Cut("://", s)
	if !ok {
		return fmt.Errorf("invalid format, must be <scheme>://<ref>")
	}
	switch scheme {
	case "rootfs", "file", "docker-image", "qcow", "":
	case "source":
		_, _, ok := strings.Cut("://", ref)
		if !ok {
			if _, err := build.ParseKernelVersion(ref); err != nil {
				return err
			}
		}
	}
	f.scheme = scheme
	f.ref = ref
	return nil
}

func (f *kernelSpecFlag) isEmpty() bool {
	return f.scheme == "" && f.ref == ""
}

func (f *kernelSpecFlag) String() string {
	if f.scheme == "" && f.ref == "" {
		return ""
	}
	return f.scheme + "://" + f.ref
}
