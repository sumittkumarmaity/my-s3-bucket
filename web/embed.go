package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded React dashboard SPA.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := strings.TrimPrefix(r.URL.Path, "/")
		if cleanPath == "" || cleanPath == "index.html" {
			fileServer.ServeHTTP(w, r)
			return
		}

		f, err := sub.Open(cleanPath)
		if err != nil {
			// Fallback to index.html for client-side routing
			r.URL.Path = "/"
		} else {
			_ = f.Close()
		}
		fileServer.ServeHTTP(w, r)
	})
}
