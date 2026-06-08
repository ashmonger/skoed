// Package swaggerui serves the M4.5 API Documentation Browser.
//
// Bundles Swagger UI 5 + a custom index.html via go:embed and exposes
// two helpers the api package mounts under /api/docs and
// /api/openapi.yaml. Zero filesystem reads at request time.
package swaggerui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed assets
var assets embed.FS

//go:embed assets/index.html
var indexHTML []byte

//go:embed assets/openapi.yaml
var openapiYAML []byte

// OpenAPIYAML returns the bundled OpenAPI spec as raw bytes. The
// management API mounts this under /api/openapi.yaml.
func OpenAPIYAML() []byte { return openapiYAML }

// AssetHandler serves the bundled Swagger UI under /api/docs. It strips
// the /api/docs/ prefix and resolves the rest against the embedded FS.
//
// The bare /api/docs and /api/docs/ paths return the custom index.html.
func AssetHandler() http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// embed package guarantees this exists at build time
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip /api/docs from the URL so the FS root maps to assets/.
		stripped := strings.TrimPrefix(r.URL.Path, "/api/docs")
		stripped = strings.TrimPrefix(stripped, "/")
		if stripped == "" || stripped == "index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(indexHTML)
			return
		}

		// Set Content-Type explicitly for the two well-known assets so
		// browsers don't have to guess.
		switch path.Ext(stripped) {
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		}

		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + stripped
		fileServer.ServeHTTP(w, r2)
	})
}

// ServeOpenAPI writes the bundled OpenAPI YAML to w. Used by the
// /api/openapi.yaml route.
func ServeOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openapiYAML)
}
