// Package webui optionally embeds the independently built Web client.
package webui

import "io/fs"

// Assets is nil in normal API-only builds. Build with -tags webui after
// running make web-assets to include trusted, locally built frontend bytes.
func Assets() fs.FS { return assets() }
