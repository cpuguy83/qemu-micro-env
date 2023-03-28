package cmd

import (
	"embed"
)

//go:embed all:init all:entrypoint all:runner
var src embed.FS

func Source() embed.FS {
	return src
}
