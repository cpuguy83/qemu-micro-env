package cmd

import (
	"embed"
)

//go:embed all:init all:entrypoint
var src embed.FS

func Source() embed.FS {
	return src
}
