package main

import (
	"context"
	"strings"
	"testing"

	"dagger.io/dagger"
	"golang.org/x/exp/slices"
)

func TestAlpineRootfs(t *testing.T) {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithWorkdir("../../"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	rootfs := AlpineRootfs(client)
	entries, err := rootfs.Entries(ctx, dagger.DirectoryEntriesOpts{
		Path: "/sbin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(entries, "custom-init") {
		t.Fatal("init should not be in the rootfs yet")
	}

	rootfs = WithInit(client, rootfs, "/sbin/custom-init")

	entries, err = rootfs.Entries(ctx, dagger.DirectoryEntriesOpts{
		Path: "/sbin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(entries, "custom-init") {
		t.Fatal("init not found")
	}

	v, err := client.Container().WithRootfs(rootfs).WithExec([]string{"/sbin/custom-init", "_check", "--text", "this is a test"}).Stdout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(v) != "this is a test" {
		t.Fatalf("unexpected output: %s", v)
	}

	qcow := MakeQcow(client, rootfs, 10*1024*1024*1024)
	diff := MakeQcowDiff(client, qcow)

	c := QemuImg(client).
		WithMountedFile("/tmp/rootfs.qcow2", qcow).
		WithMountedFile("/tmp/rootfs-diff.qcow2", diff).
		WithExec([]string{"/usr/bin/qemu-img", "check", "/tmp/rootfs.qcow2"}).
		WithExec([]string{"/usr/bin/qemu-img", "check", "/tmp/rootfs-diff.qcow2"})

	code, err := c.ExitCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		stdout, _ := c.Stdout(ctx)
		stderr, _ := c.Stderr(ctx)
		t.Fatalf("qcow2 diff check failed: stdout: %s, stderr: %s", stdout, stderr)
	}
}
