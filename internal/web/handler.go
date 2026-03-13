package web

import (
	"io/fs"
	"net/http"
	"strings"
)

// NewSPAHandler returns an http.Handler that serves the embedded SPA.
// Static files are served directly; all other routes serve index.html
// to support client-side routing.
func NewSPAHandler() http.Handler {
	subFS, err := fs.Sub(DistFS, "dist")
	if err != nil {
		// Should never happen — if dist/ is missing the binary won't compile.
		panic("web: embedded dist/ directory not found: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Strip leading slash for fs.Open
		fsPath := strings.TrimPrefix(path, "/")
		if fsPath == "" {
			fsPath = "index.html"
		}

		// Try to open the file in the embedded FS
		f, err := subFS.Open(fsPath)
		if err == nil {
			f.Close()
			// File exists — serve it directly
			fileServer.ServeHTTP(w, r)
			return
		}

		// File doesn't exist — serve index.html for SPA routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
