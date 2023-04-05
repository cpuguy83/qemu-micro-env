package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker/buildkitopt"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/cpuguy83/qemu-micro-env/build"
	"github.com/cpuguy83/qemu-micro-env/build/vmconfig"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/identity"
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
	set.Var(&cfg.ImageConfig.kernel, "kernel", "kernel spec")
	set.Var(&cfg.ImageConfig.initrd, "initrd", "initrd spec")
}

func checkMergeOp(ctx context.Context, client *bkclient.Client) {
	def, err := llb.Merge([]llb.State{llb.Scratch().File(llb.Mkdir("/a", 0700)), llb.Scratch().File(llb.Mkdir("/b", 0700))}).Marshal(ctx)
	if err != nil {
		panic(err)
	}
	if _, err := client.Solve(ctx, def, bkclient.SolveOpt{
		Exports: mobyExports,
	}, nil); err != nil {
		logrus.WithError(err).Info("Disabling MergeOp support due to error")
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
		res, err = client.Solve(ctx, def, bkclient.SolveOpt{
			Exports:   mobyExports,
			Ref:       ref,
			LocalDirs: map[string]string{hostKernelContext: "/"},
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
