package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/qemu-micro-env/cmd/entrypoint/vmconfig"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type config struct {
	Debug       bool
	VM          vmconfig.VMConfig
	StateDir    string
	ImageConfig vmImageConfig
	ImageRef    string
}

type logFormatter struct {
	base *nested.Formatter
	name string
}

func (f *logFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	entry.Data["component"] = f.name
	return f.base.Format(entry)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		os.Stdin.Close() // Make sure we don't block on stdin
	}()

	if err := do(ctx); err != nil {
		logrus.Fatal(err)
	}
}

func do(ctx context.Context) error {
	var cfg config

	flag.BoolVar(&cfg.Debug, "debug", false, "enable debug logging")
	runnerFlags(flag.CommandLine, &cfg)
	buildFlags(flag.CommandLine, &cfg)

	flag.Parse()

	if cfg.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}
	logrus.SetOutput(os.Stderr)

	docker := docker.NewClient()

	switch flag.Arg(0) {
	case "build":
		set := flag.NewFlagSet("build", flag.ExitOnError)
		set.BoolVar(&cfg.Debug, "debug", cfg.Debug, "enable debug logging")
		buildFlags(set, &cfg)

		var args []string
		if flag.NArg() > 1 {
			args = flag.Args()[1:]
		}

		if err := set.Parse(args); err != nil {
			return err
		}

		if cfg.Debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		dgst, err := doBuilder(ctx, cfg, docker.Transport())
		if err != nil {
			return err
		}
		fmt.Println(dgst)
	case "run":
		set := flag.NewFlagSet("run", flag.ExitOnError)
		set.BoolVar(&cfg.Debug, "debug", cfg.Debug, "enable debug logging")
		runnerFlags(set, &cfg)

		var args []string
		if flag.NArg() > 1 {
			args = flag.Args()[1:]
		}

		if err := set.Parse(args); err != nil {
			return err
		}

		if cfg.Debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		cfg.ImageRef = set.Arg(0)
		if cfg.ImageRef == "" || cfg.ImageRef == "-" {
			dt, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
			if err != nil {
				return err
			}
			dts := strings.TrimSpace(string(dt))
			if _, err := digest.Parse(dts); err != nil {
				return fmt.Errorf("invalid image digest: %s, %w", dts, err)
			}
			cfg.ImageRef = dts
		}

		return doRunner(ctx, cfg, docker.Transport())
	case "":
		dgst, err := doBuilder(ctx, cfg, docker.Transport())
		if err != nil {
			return err
		}
		cfg.ImageRef = dgst
		return doRunner(ctx, cfg, docker.Transport())
	default:
		return fmt.Errorf("unknown command: %s", flag.Arg(0))
	}
	return nil
}
