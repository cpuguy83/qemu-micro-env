package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"dagger.io/dagger"
)

func main() {
	ctx := context.Background()
	if err := do(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func do(ctx context.Context) error {
	cgVerFl := flag.Int("cgroup-version", 2, "cgroup version to use")

	flag.Parse()

	switch *cgVerFl {
	case 1, 2:
	default:
		return fmt.Errorf("invalid cgroup version: %d", *cgVerFl)
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	qcow := MakeQcow(client, WithInit(client, AlpineRootfs(client), "/sbin/init"), 10*1024*1024*1024)

	img := QemuImg(client).
		WithMountedFile("/tmp/rootfs.qcow2", qcow).
		WithExec(
			[]string{
				"/usr/bin/qemu-system-x86_64",
			},
		)

	img.ExitCode(ctx)

	return nil
}
