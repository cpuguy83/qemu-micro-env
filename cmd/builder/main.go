package main

import (
	"context"
	"flag"
	"os"
	"os/signal"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker/buildkitopt"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/cpuguy83/qemu-micro-env/cmd/entrypoint/vmconfig"
	bkclient "github.com/moby/buildkit/client"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type logFormatter struct {
	base *nested.Formatter
}

func (f *logFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	entry.Data["component"] = "builder"
	return f.base.Format(entry)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer cancel()

	if err := do(ctx); err != nil {
		logrus.Fatal(err)
	}
}

func do(ctx context.Context) error {
	var (
		cfg        vmconfig.VMConfig
		vmImageCfg vmImageConfig
	)

	vmconfig.AddVMFlags(flag.CommandLine, &cfg)
	flag.StringVar(&vmImageCfg.size, "qcow-size", defaultQcowSize, "Size for the created qcow image")
	flag.StringVar(&vmImageCfg.rootfs, "rootfs", "", "Image to get a rootfs from. If empty will use the default rootfs.")
	debug := flag.Bool("debug", false, "enable debug logging")

	flag.Parse()

	if vmImageCfg.rootfs == "" && cfg.InitCmd == "" {
		cfg.InitCmd = "/usr/bin/dockerd"
	}

	logrus.SetFormatter(&logFormatter{&nested.Formatter{}})
	logrus.SetOutput(os.Stderr)
	if *debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	tr, err := transport.DefaultTransport()
	if err != nil {
		return err
	}

	client, err := bkclient.New(ctx, "", buildkitopt.FromDocker(tr)...)
	if err != nil {
		return err
	}
	defer client.Close()

	// cacheMount := mountSpecFl.AsMount()

	spec, err := specFromFlags(ctx, vmImageCfg)
	if err != nil {
		return err
	}

	img, err := mkImage(ctx, spec)
	if err != nil {
		return err
	}

	def, err := img.Marshal(ctx)
	if err != nil {
		return err
	}

	statusCh := make(chan *bkclient.SolveStatus)
	go func() {
		for s := range statusCh {
			for _, v := range s.Logs {
				logrus.Debug(string(v.Data))
			}
		}
	}()

	_, err = client.Solve(ctx, def, bkclient.SolveOpt{}, nil)
	return err
}
