package build

import (
	"context"
	"testing"
)

func TestBuildKernel(t *testing.T) {
	ctr := client.Container().From("ubuntu:jammy").WithExec([]string{
		"/bin/sh", "-c",
		`apt-get update && apt-get install -y \
			build-essential \
			libelf-dev \
			libncurses-dev \
			libssl-dev \
			libelf-dev \
			bc \
			flex \
			bison
		`,
	})
	source, err := GetKernelSource(client, "6.2.2")
	if err != nil {
		t.Fatal(err)
	}

	kern := BuildKernel(ctr, source, nil)
	if err != nil {
		t.Fatal(err)
	}

	sz, err := kern.Size(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sz == 0 {
		t.Fatal("kernel size is zero")
	}
}
