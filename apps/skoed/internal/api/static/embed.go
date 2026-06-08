// Package static embeds the compiled skoed Web UI (Vite-built Vue SPA at
// web/dist/) into the binary via go:embed. The fileserver served by the API
// at GET / and GET /assets/* reads from this FS.
//
// Build pipeline:
//   1. cd web && npm install && npm run build      → web/dist/
//   2. cp -r web/dist apps/skoed/internal/api/static/dist
//   3. go build (the //go:embed picks up dist/ on next compile)
//
// On a node booted before the SPA has been built (e.g. CI for a Go-only
// change), the embedded FS is empty and the router falls through to
// http.NotFound for / — the API endpoints stay unaffected.
package static

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distRaw embed.FS

// FS returns the embedded SPA filesystem rooted at "dist/". Returns an
// empty FS if the embed slot was never populated.
func FS() fs.FS {
	sub, err := fs.Sub(distRaw, "dist")
	if err != nil {
		return distRaw
	}
	return sub
}

// HasIndex reports whether the embedded SPA actually contains an index.html.
// Used by the router to decide whether to mount the static handler at all.
func HasIndex() bool {
	f, err := distRaw.Open("dist/index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
