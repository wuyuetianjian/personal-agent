//go:build webui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

func assets() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
