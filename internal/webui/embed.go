// Package webui embeds the built web UI. `make web` (or `npm run build` in
// web/) writes the files into dist/.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Files returns the web UI rooted at dist/.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is a constant path inside the embedded tree
	}
	return sub
}
