package build

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	env "github.com/cpuguy83/qemu-micro-env"
)

var (
	client *dagger.Client
)

type mountSpec mount.Mount

func (m *mountSpec) Set(s string) error {
	split := strings.Split(s, ",")
	for _, s := range split {
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("invalid mount spec: %s", s)
		}
		switch k {
		case "type":
			m.Type = mount.Type(v)
		case "source":
			m.Source = v
		default:
			return fmt.Errorf("unknown mount spec key: %s", k)
		}
	}
	if m.Type == "" || m.Source == "" {
		return fmt.Errorf("invalid mount spec, both type and source keys are required: %s", s)
	}
	return nil
}

func (m *mountSpec) String() string {
	return fmt.Sprintf("type=%s,source=%s", m.Type, m.Source)
}

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
	cacheMountFl := &mountSpec{
		Type:   "bind",
		Source: defaultPath,
	}
	flag.Var(cacheMountFl, "cache", "Cache volume for dagger. Provided like a docker volume mount spec (type=volume,source=foo or type=bind,source=/foo)")

	flag.Parse()

	if cacheMountFl.Source == defaultPath {
		if err := os.MkdirAll(defaultPath, 0750); err != nil {
			panic(err)
		}
	}

	cacheMount := mount.Mount(*cacheMountFl)
	client, cleanup, err = env.EnsureDagger(context.Background(), &cacheMount)
	if err != nil {
		panic(err)
	}

	ret := m.Run()
	cleanup()
	os.Exit(ret)
}
