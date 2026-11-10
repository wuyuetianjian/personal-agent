//go:build !webui

package webui

import "io/fs"

func assets() fs.FS { return nil }
