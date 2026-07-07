package api

import (
	"net/http"
	"os"

	"qingzhou/frontend"
	"qingzhou/web"
)

// newFrontendHandler returns the handler for the new Vue 3 frontend.
// When QZ_WEB_DIR is set, it serves from disk (dev mode) via the original
// web handler (which respects QZ_WEB_DIR). Otherwise, it uses the embedded
// frontend/dist from the frontend package.
func newFrontendHandler() http.Handler {
	// In dev mode with QZ_WEB_DIR, the web.Handler already reads from disk,
	// so we can just use it — point QZ_WEB_DIR=frontend/dist.
	if os.Getenv("QZ_WEB_DIR") != "" {
		return web.Handler()
	}
	return frontend.Handler()
}
