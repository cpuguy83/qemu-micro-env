package build

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	env "github.com/cpuguy83/qemu-micro-env"
	"github.com/cpuguy83/qemu-micro-env/flags"
)

var (
	client *dagger.Client
)

func TestMain(m *testing.M) {
	var (
		cleanup func()
		err     error
	)

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	defaultPath := filepath.Join(cwd, "_output/test-cache")
	cacheMountFl := flags.NewMountSpec(&mount.Mount{Type: mount.TypeBind, Source: defaultPath})
	flags.AddMountSpecFlag(flag.CommandLine, cacheMountFl, "cache")

	flag.Parse()

	cacheMount := cacheMountFl.AsMount()
	var mnt *mount.Mount
	if cacheMount.IsSome() {
		m := cacheMount.Unwrap()
		if m.Source == defaultPath {
			if err := os.MkdirAll(defaultPath, 0750); err != nil {
				panic(err)
			}
		}
		mnt = &m
	}

	client, cleanup, err = env.EnsureDagger(context.Background(), mnt)
	if err != nil {
		panic(err)
	}

	ret := m.Run()
	cleanup()
	os.Exit(ret)
}
