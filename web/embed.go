// Package web embeds and serves the single-page frontend.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// assetFS returns the frontend filesystem. In dev, set QZ_WEB_DIR=web/dist to
// serve straight from disk so edits show on browser refresh (no rebuild). In
// production, leave it unset to use the files embedded in the binary.
func assetFS() fs.FS {
	if dir := os.Getenv("QZ_WEB_DIR"); dir != "" {
		return os.DirFS(dir)
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Handler serves static assets, falling back to index.html for SPA routes.
func Handler() http.Handler {
	sub := assetFS()
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Embedded files have no real mtime; disable caching so rebuilds/edits
		// are always picked up.
		w.Header().Set("Cache-Control", "no-store, must-revalidate")

		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(sub, p); err == nil && !st.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		index, _ := fs.ReadFile(sub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
