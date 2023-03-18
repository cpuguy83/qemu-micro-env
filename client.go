package env

import (
	"context"
	"errors"
	"os"
	"sync"

	"dagger.io/dagger"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	"github.com/cpuguy83/go-docker/container/containerapi/mount"
	"github.com/cpuguy83/go-docker/errdefs"
	"github.com/cpuguy83/go-docker/image"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/sirupsen/logrus"
)

var (
	DaggerEngineRef   = image.Remote{Host: "registry.dagger.io", Locator: "engine", Tag: "v0.4.0"}
	DockerTransportFn = transport.DefaultTransport

	L = logrus.NewEntry(logrus.StandardLogger())

	daggerMu sync.Mutex
)

func DaggerClient(ctx context.Context) (*dagger.Client, error) {
	return dagger.Connect(ctx, dagger.WithLogOutput(L.WriterLevel(logrus.DebugLevel)))
}

func EnsureDagger(ctx context.Context, cacheMount *mount.Mount) (client *dagger.Client, cleanup func(), err error) {
	// First try without locking
	if os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST") != "" {
		client, err = DaggerClient(ctx)
		return
	}

	daggerMu.Lock()
	locked := true

	if os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST") != "" {
		daggerMu.Unlock()
		client, err = DaggerClient(ctx)
		return
	}

	defer func() {
		if locked {
			daggerMu.Unlock()
		}
	}()

	docker := docker.NewClient(func(cfg *docker.NewClientConfig) {
		cfg.Transport, err = DockerTransportFn()
	})
	if err != nil {
		return
	}

	ctr, err := CreateDaggerContainer(ctx, docker, cacheMount)
	if err != nil {
		return
	}
	cleanup = func() {
		if err := docker.ContainerService().Remove(ctx, ctr.ID(), container.WithRemoveForce); err != nil {
			L.WithError(err).Error("failed to remove dagger container")
		}
	}

	os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-container://"+ctr.ID())
	daggerMu.Unlock()
	locked = false
	client, err = DaggerClient(ctx)
	return
}

func CreateDaggerContainer(ctx context.Context, docker *docker.Client, cacheMount *mount.Mount) (*container.Container, error) {
	var mounts []mount.Mount
	if cacheMount != nil {
		if cacheMount.Target == "" {
			cacheMount.Target = "/var/lib/dagger"
		}
		mounts = []mount.Mount{*cacheMount}
	}
	opts := []container.CreateOption{
		container.WithCreateHostConfig(containerapi.HostConfig{
			AutoRemove: true,
			Privileged: true,
			Mounts:     mounts,
		}),
	}

	ctr, err := docker.ContainerService().Create(ctx, DaggerEngineRef.String(), opts...)
	if err != nil {
		if !errors.Is(err, errdefs.ErrNotFound) {
			return nil, err
		}

		if err := docker.ImageService().Pull(ctx, DaggerEngineRef, image.WithPullProgressMessage(func(ctx context.Context, msg image.PullProgressMessage) error {
			L.WithFields(logrus.Fields{
				"status":   msg.Status,
				"detail":   msg.Detail,
				"id":       msg.ID,
				"progress": msg.Progress,
			}).Debug("pulling dagger engine image")
			return nil
		})); err != nil {
			return nil, err
		}
		ctr, err = docker.ContainerService().Create(ctx, DaggerEngineRef.String(), opts...)
	}

	if err != nil {
		return nil, err
	}

	return ctr, ctr.Start(ctx)
}
