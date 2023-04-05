package build

import (
	"fmt"
	gofs "io/fs"

	"github.com/moby/buildkit/client/llb"
)

// RewriteFn is a function that can be used to rewrite the path of a file or directory
// It is used by GoFSToDagger to allow callers to pass in a function that can rewrite the path.
type RewriteFn func(string) string

// GoFSToDagger converts a gofs.FS to llb state.
func GoFSToLLB(fs gofs.FS, rewrite RewriteFn) (llb.State, error) {
	st := llb.Scratch()
	err := gofs.WalkDir(fs, ".", func(path string, entry gofs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rewritten := path
		if rewrite != nil {
			rewritten = rewrite(path)
			if rewritten == "" {
				return nil
			}
		}

		if path == "." {
			return nil
		}

		fi, err := entry.Info()
		if err != nil {
			return err
		}

		if entry.IsDir() {
			st = st.File(llb.Mkdir(rewritten, fi.Mode(), llb.WithParents(true)))
			return nil
		}

		dt, err := gofs.ReadFile(fs, path)
		if err != nil {
			return err
		}
		st = st.File(llb.Mkfile(rewritten, fi.Mode(), dt))
		return nil
	})
	if err != nil {
		return llb.Scratch(), fmt.Errorf("error walking embedded source dir: %w", err)
	}
	return st, nil
}
