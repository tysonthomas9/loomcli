package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

// frontendHandler returns an http.Handler that serves the embedded frontend assets.
// It implements SPA routing by returning index.html for paths that don't match
// any existing file in the embedded filesystem.
func frontendHandler() http.Handler {
	// Strip the "frontend/dist" prefix to serve files from the root
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		// This should never happen since we embed the directory at compile time
		panic("failed to create sub filesystem: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path and check if the file exists.
		// Note: path.Clean removes .. elements and embed.FS is inherently safe,
		// but we add explicit validation for defense-in-depth.
		urlPath := path.Clean(r.URL.Path)
		if urlPath == "/" {
			urlPath = "/index.html"
		}

		// Reject any path that still contains ".." after cleaning (shouldn't happen)
		// or doesn't start with "/" (invalid path)
		if strings.Contains(urlPath, "..") || !strings.HasPrefix(urlPath, "/") {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}

		// Try to open the file to see if it exists
		filePath := strings.TrimPrefix(urlPath, "/")
		_, err := fs.Stat(distFS, filePath)

		if err != nil {
			// File doesn't exist - serve index.html for SPA routing
			r.URL.Path = "/"
			setCacheHeaders(w, false)
			fileServer.ServeHTTP(w, r)
			return
		}

		// File exists - serve it with appropriate cache headers
		setCacheHeaders(w, shouldCache(urlPath))
		fileServer.ServeHTTP(w, r)
	})
}

// setCacheHeaders sets appropriate cache headers based on whether the file
// should be cached (hashed assets) or not (index.html).
func setCacheHeaders(w http.ResponseWriter, cache bool) {
	if cache {
		// Long cache for hashed assets (1 year)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// No cache for index.html - ensures clients always get the latest version
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	}
}

// shouldCache returns true if the file should have long cache headers.
// Vite adds content hashes to asset filenames, so they can be cached forever.
func shouldCache(urlPath string) bool {
	// index.html should never be cached
	if urlPath == "/index.html" || urlPath == "/" {
		return false
	}
	// Assets in /assets/ directory have content hashes
	if strings.HasPrefix(urlPath, "/assets/") {
		return true
	}
	return false
}
