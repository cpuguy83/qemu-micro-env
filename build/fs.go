package build

import (
	gofs "io/fs"

	"dagger.io/dagger"
)

// RewriteFn is a function that can be used to rewrite the path of a file or directory
// It is used by GoFSToDagger to allow callers to pass in a function that can rewrite the path.
type RewriteFn func(string) string

// GoFSToDagger converts a gofs.FS to a dagger.Directory.
// This does require reading the contents of all files into memory and the data is converted to a string.
func GoFSToDagger(fs gofs.FS, dir *dagger.Directory, rewrite RewriteFn) (*dagger.Directory, error) {
	err := gofs.WalkDir(fs, ".", func(path string, d gofs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rewritten := rewrite(path)
		if rewritten == "" {
			return nil
		}

		if path == "." {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			return err
		}

		perm := fi.Mode().Perm()
		if d.IsDir() {
			dir = dir.WithNewDirectory(rewritten, dagger.DirectoryWithNewDirectoryOpts{Permissions: int(perm)})
			return nil
		}

		b, err := gofs.ReadFile(fs, path)
		if err != nil {
			return err
		}

		dir = dir.WithNewFile(rewritten, string(b), dagger.DirectoryWithNewFileOpts{Permissions: int(perm)})
		return nil
	})
	return dir, err
}
