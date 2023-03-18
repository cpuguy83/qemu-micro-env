package build

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBuildMod(t *testing.T) {
	for name, builder := range Modules() {
		name := name
		builder := builder

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, err := builder(client)
			if err != nil {
				t.Fatal(err)
			}

			dir := t.TempDir()
			ok, err := f.Export(context.Background(), filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("failed to export init module")
			}
		})
	}
}
