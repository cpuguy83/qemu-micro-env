package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker/buildkitopt"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/cpuguy83/qemu-micro-env/build"
	"github.com/cpuguy83/qemu-micro-env/build/vmconfig"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var mobyExports = []bkclient.ExportEntry{
	{Type: "moby"},
}

func buildFlags(set *flag.FlagSet, cfg *config) {
	vmconfig.AddVMFlags(set, &cfg.VM)
	set.StringVar(&cfg.ImageConfig.size, "qcow-size", defaultQcowSize, "Size for the created qcow image")
	set.StringVar(&cfg.ImageConfig.rootfs, "rootfs", "", "Image to get a rootfs from. If empty will use the default rootfs.")
	set.Var(&cfg.ImageConfig.kernel, "kernel", "kernel spec (docker-image://<image> (assumes /boot/vmlinuz), local://<path to vmlinuz>, <path to vmlinuz> (same as local://), version://<version> (compile from source))")
	set.Var(&cfg.ImageConfig.initrd, "initrd", "initrd spec (docker-image://<image> (assumes /boot/initrd.img), local://<path to initrd.img>, <path to initrd.img> (same as local://))")
	set.Var(&cfg.ImageConfig.modules, "modules", "kernel modules spec (docker-image://<image> (assumes /lib/modules), local://<path to modules dir>, <path to modules dir> (same as local://))")
	set.StringVar(&cfg.CacheSpec, "remote-cache", os.Getenv("BUILDKIT_REMOTE_CACHE"), "Buildkit remote cache spec, default comes from the BUILDKIT_REMOTE_CACHE environment variable")
	set.StringVar(&cfg.Tag, "t", "", "Tag the produced image")
	set.BoolVar(&cfg.Push, "push", false, "Push the produced image")
}

func checkMergeOp(ctx context.Context, client *bkclient.Client) {
	_, err := client.Build(ctx, bkclient.SolveOpt{}, "", func(ctx context.Context, client gateway.Client) (*gateway.Result, error) {
		capset := client.BuildOpts().Caps
		err := capset.Supports(pb.CapMergeOp)
		if !build.UseMergeOp {
			logrus.WithError(err).Info("Disabling MergeOp support due to error")
		}
		return &gateway.Result{}, nil
	}, nil)
	if err != nil {
		logrus.WithError(err).Warn("Error checking for MergeOp support, disabling")
		build.UseMergeOp = false
	}
}

func doBuilder(ctx context.Context, cfg config, tr transport.Doer) (string, error) {
	logrus.SetFormatter(&logFormatter{&nested.Formatter{}, "builder"})

	client, err := bkclient.New(ctx, "", buildkitopt.FromDocker(tr)...)
	if err != nil {
		return "", err
	}
	defer client.Close()

	checkMergeOp(ctx, client)

	spec, err := specFromFlags(ctx, cfg.ImageConfig)
	if err != nil {
		return "", err
	}

	img, err := mkImage(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("error building image LLB: %w", err)
	}

	def, err := img.Marshal(ctx)
	if err != nil {
		return "", fmt.Errorf("error marshaling LLB: %w", err)
	}

	ref := identity.NewID()

	eg, ctx := errgroup.WithContext(ctx)

	var res *bkclient.SolveResponse
	ch := make(chan *bkclient.SolveStatus)
	eg.Go(func() error {
		var err error
		var cacheOpts []bkclient.CacheOptionsEntry
		if cfg.CacheSpec != "" {
			attrs := make(map[string]string)
			var typ string
			var skip bool
			for _, kv := range strings.Split(cfg.CacheSpec, ",") {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					skip = true
					logrus.WithField("attr", kv).Error("Invalid cache spec attribute, skipping cache configuration")
					break
				}
				if k == "type" {
					typ = v
					continue
				}
				attrs[k] = v
			}
			if !skip && typ == "" {
				skip = true
				logrus.Warn("Cache spec is missing type parameter, skipping")
			}
			if !skip {
				cacheOpts = append(cacheOpts, bkclient.CacheOptionsEntry{
					Type:  typ,
					Attrs: attrs,
				})
			}
		}

		exports := mobyExports
		if cfg.Tag != "" {
			if exports[0].Attrs == nil {
				exports[0].Attrs = make(map[string]string, 1)
			}
			exports[0].Attrs["name"] = cfg.Tag
			if cfg.Push {
				exports[0].Attrs["push"] = "true"
			}
		}
		res, err = client.Solve(ctx, def, bkclient.SolveOpt{
			Exports:      exports,
			Ref:          ref,
			CacheExports: cacheOpts,
			CacheImports: cacheOpts,
			LocalDirs:    getLocalContexts(cfg),
		}, ch)
		if err != nil {
			return fmt.Errorf("error solving: %w", err)
		}
		return nil
	})

	var logs []string
	eg.Go(func() error {
		for st := range ch {
			for _, l := range st.Logs {
				dt := strings.TrimSpace(string(l.Data))
				logs = append(logs, dt)
				logrus.Debug(dt)
			}
		}
		return nil
	})

	if err := eg.Wait(); err != nil {
		for _, l := range logs {
			logrus.Warn(l)
		}

		return "", err
	}

	dgst, ok := res.ExporterResponse[exptypes.ExporterImageDigestKey]
	if !ok {
		return "", fmt.Errorf("no image digest returned")
	}
	return dgst, nil
}
