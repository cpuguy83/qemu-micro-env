package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
)

func TestFile(t *testing.T) {
	st := llb.Scratch().
		File(llb.Mkdir("/foo/bar", 0o755, llb.WithParents(true))).
		File(llb.Mkfile("/foo/bar/baz", 0o644, []byte("hello world")))

	f := NewFile(st, "/foo/bar/baz")

	ctx := context.Background()

	nst := llb.Scratch().File(llb.Copy(f.State(), f.Path(), "/baz"))
	def, err := nst.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	_, err = client.Solve(ctx, def, bkclient.SolveOpt{
		Exports: []bkclient.ExportEntry{
			{Type: "local", OutputDir: dir},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	dt, err := os.ReadFile(filepath.Join(dir, "baz"))
	if err != nil {
		t.Fatal(err)
	}
	if string(dt) != "hello world" {
		t.Errorf("unexpected content: %s", string(dt))
	}

	nst = llb.Scratch().File(llb.Copy(f.State(), f.Path(), "copied/baz", createParentsCopyOption{}))
	def, err = nst.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dir = t.TempDir()
	_, err = client.Solve(ctx, def, bkclient.SolveOpt{
		Exports: []bkclient.ExportEntry{
			{Type: "local", OutputDir: dir},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	dt, err = os.ReadFile(filepath.Join(dir, "copied/baz"))
	if err != nil {
		t.Fatal(err)
	}
	if string(dt) != "hello world" {
		t.Errorf("unexpected content: %s", string(dt))
	}

}
