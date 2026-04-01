package webui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

// logFrontendBuildMeta reads the embedded .build-meta file and logs when the
// frontend was built. This makes it immediately obvious when the embedded
// frontend is stale (e.g., developer ran `go install` without `npm run build`).
func logFrontendBuildMeta() {
	data, err := frontendFS.ReadFile("frontend/dist/.build-meta")
	if err != nil {
		slog.Warn("frontend build metadata not found — frontend may be stale (run 'make install' to rebuild)")
		return
	}
	var meta struct {
		BuiltAt string `json:"built_at"`
		GitHash string `json:"git_hash"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		slog.Warn("failed to parse frontend build metadata", "err", err)
		return
	}
	slog.Info("embedded frontend", "built_at", meta.BuiltAt, "git_hash", meta.GitHash)
}

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
		// Reject unregistered API paths — never serve SPA HTML for /api/* requests
		if strings.HasPrefix(r.URL.Path, "/api/") {
			respondError(w, http.StatusNotFound, "not found")
			return
		}

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
			// Static assets (JS, CSS, images, fonts) should 404, not get SPA fallback.
			// SPA routing only applies to extensionless paths (e.g. /ws/abc/settings).
			if isStaticAsset(urlPath) {
				http.NotFound(w, r)
				return
			}
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

// isStaticAsset returns true if the URL path looks like a static asset
// (JS, CSS, image, font, etc.) that should 404 rather than get SPA fallback.
func isStaticAsset(urlPath string) bool {
	ext := path.Ext(urlPath)
	switch ext {
	case ".js", ".css", ".map",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".avif",
		".woff", ".woff2", ".ttf", ".eot",
		".json", ".xml", ".txt", ".webmanifest":
		return true
	}
	return false
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

// devFrontendHandler returns an http.Handler that serves frontend assets from
// a directory on disk instead of the embedded filesystem. This enables
// development without recompiling the Go binary. It preserves SPA routing
// by serving index.html for paths that don't match existing files.
func devFrontendHandler(dir string) http.Handler {
	if dir == "" {
		dir = "internal/webui/frontend/dist"
	}

	// Resolve to absolute path for path traversal validation
	absDir, err := filepath.Abs(dir)
	if err != nil {
		slog.Warn("failed to resolve dev frontend directory", "err", err)
		absDir = dir
	}

	// Warn if the directory doesn't exist at startup
	if _, err := os.Stat(absDir); err != nil {
		slog.Warn("dev frontend directory not found (run 'npm run build' first)", "dir", absDir)
	}

	fileServer := http.FileServer(http.Dir(absDir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject unregistered API paths — never serve SPA HTML for /api/* requests
		if strings.HasPrefix(r.URL.Path, "/api/") {
			respondError(w, http.StatusNotFound, "not found")
			return
		}

		urlPath := path.Clean(r.URL.Path)
		if urlPath == "/" {
			urlPath = "/index.html"
		}

		if strings.Contains(urlPath, "..") || !strings.HasPrefix(urlPath, "/") {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}

		// Check if file exists on disk
		filePath := filepath.Join(absDir, strings.TrimPrefix(urlPath, "/"))

		// Defense-in-depth: verify resolved path stays within the serving directory
		absFilePath, err := filepath.Abs(filePath)
		if err != nil || !strings.HasPrefix(absFilePath, absDir) {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}

		cleanFile := filepath.Clean(filePath)
		_, err = os.Stat(cleanFile)
		if err != nil {
			if isStaticAsset(urlPath) {
				http.NotFound(w, r)
				return
			}
			// File doesn't exist - serve index.html for SPA routing
			r.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			fileServer.ServeHTTP(w, r)
			return
		}

		// File exists - serve it (no long caching in dev mode)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		fileServer.ServeHTTP(w, r)
	})
}
