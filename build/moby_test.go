package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	bkclient "github.com/moby/buildkit/client"
)

func TestGetMoby(t *testing.T) {
	st, err := GetMoby("")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	dt, err := st.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	client.Solve(ctx, dt, bkclient.SolveOpt{
		Exports: []bkclient.ExportEntry{
			{Type: "local", OutputDir: dir},
		},
	}, nil)

	defer func() {
		if t.Failed() {
			filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				t.Log(filepath.Join(dir, path))
				return nil
			})
		}
	}()

	prefix := "usr/local/bin"

	bins := []string{
		"docker",
		"dockerd",
		"containerd",
		"containerd-shim-runc-v2",
		"runc",
	}

	for _, b := range bins {
		_, err := os.Stat(filepath.Join(dir, prefix, b))
		if err != nil {
			t.Errorf("failed to find %s: %v", b, err)
		}
	}
}
