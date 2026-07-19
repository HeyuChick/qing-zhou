// Package frontend embeds and serves the new Vue 3 SPA frontend.
//
// In development, set QZ_WEB_DIR=frontend/dist to serve from disk.
// In production, the dist/ directory is embedded in the binary.
// To build: run "npm run build" in frontend/, then rebuild the Go binary.
package frontend

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

// assetFS returns the frontend filesystem.
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
// Requests under /assets/ that don't match a real file get a 404 instead of
// the SPA fallback, so the browser doesn't receive HTML when it expects JS/CSS.
func Handler() http.Handler {
	sub := assetFS()
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(sub, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					// Vite content-hashes these filenames (e.g. app-a1b2c3.js), so the
					// bytes at a given URL never change — cache them for a year and stop
					// re-downloading the whole bundle on every SPA visit.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-store, must-revalidate")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
			// Static assets that don't exist → 404, not SPA fallback.
			if strings.HasPrefix(p, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		// index.html and SPA fallback must never be cached — it's the bootstrap that
		// references the hashed bundles, so a stale copy pins the app to old assets.
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			// dist/ holds only the .gitkeep placeholder on a fresh clone, so this
			// is what you get if you build the Go binary without building the
			// frontend first. Swallowing it serves an empty 200 — a blank page
			// with nothing in the log to explain it. Say so instead.
			http.Error(w, "前端资源缺失：请先在 frontend/ 下执行 `npx vite build`，"+
				"再重新编译 Go 二进制（前端在 go build 时内嵌）。"+
				"开发时也可设 QZ_WEB_DIR=frontend/dist 直读磁盘。",
				http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
