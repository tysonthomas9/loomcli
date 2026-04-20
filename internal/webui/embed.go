package webui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:frontend/dist
var FrontendFS embed.FS

// LogFrontendBuildMeta reads the embedded .build-meta file and logs when the
// frontend was built. This makes it immediately obvious when the embedded
// frontend is stale (e.g., developer ran `go build` without `make build-frontend`).
func LogFrontendBuildMeta() {
	data, err := FrontendFS.ReadFile("frontend/dist/.build-meta")
	if err != nil {
		logger.Warn("frontend build metadata not found — frontend may be stale (run 'make build-frontend' to rebuild)")
		return
	}
	var meta struct {
		BuiltAt string `json:"built_at"`
		GitHash string `json:"git_hash"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		logger.Warn("failed to parse frontend build metadata", "err", err)
		return
	}
	logger.Info("embedded frontend", "built_at", meta.BuiltAt, "git_hash", meta.GitHash)
}

// FrontendHandler returns an http.Handler that serves the embedded frontend assets.
// It implements SPA routing by returning index.html for paths that don't match
// any existing file in the embedded filesystem.
//
// /api/* paths are rejected with a JSON 404 as defense-in-depth — in normal
// wiring the /api/ catch-all handler (registered BEFORE this handler via Go
// 1.22+ longest-match routing) handles those first, so this branch only
// triggers if the frontend handler is mounted standalone.
func FrontendHandler() http.Handler {
	distFS, err := fs.Sub(FrontendFS, "frontend/dist")
	if err != nil {
		panic("failed to create sub filesystem: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Normalize the path first so the /api/* check can't be bypassed by
		// double slashes or other quirks that survive HTTP parsing
		// (e.g. `//api/foo` before path.Clean).
		urlPath := path.Clean(r.URL.Path)
		if !strings.HasPrefix(urlPath, "/") {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}
		if strings.HasPrefix(urlPath, "/api/") {
			respondError(w, http.StatusNotFound, "not found")
			return
		}
		if urlPath == "/" {
			urlPath = "/index.html"
		}

		// path.Clean collapses any `..` segments; path traversal beyond the
		// embed root is further prevented by fs.Sub rooting at frontend/dist.
		filePath := strings.TrimPrefix(urlPath, "/")
		_, err := fs.Stat(distFS, filePath)

		if err != nil {
			// Static assets (JS, CSS, images, fonts) should 404, not get SPA fallback.
			// SPA routing only applies to extensionless paths (e.g. /ws/abc/settings).
			if IsStaticAsset(urlPath) {
				http.NotFound(w, r)
				return
			}
			// File doesn't exist — serve index.html for SPA routing.
			r.URL.Path = "/"
			SetCacheHeaders(w, false)
			fileServer.ServeHTTP(w, r)
			return
		}

		SetCacheHeaders(w, ShouldCache(urlPath))
		fileServer.ServeHTTP(w, r)
	})
}

// SetCacheHeaders sets appropriate cache headers based on whether the file
// should be cached (hashed assets) or not (index.html).
func SetCacheHeaders(w http.ResponseWriter, cache bool) {
	if cache {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	}
}

// IsStaticAsset returns true if the URL path looks like a static asset
// (JS, CSS, image, font, etc.) that should 404 rather than get SPA fallback.
func IsStaticAsset(urlPath string) bool {
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

// ShouldCache returns true if the file should have long cache headers.
// Vite emits content-hashed filenames under /assets/, so they can be cached
// forever. Fonts under /fonts/ are also versioned (filename encodes the
// font version) and safe to cache long-term — avoids refetching on every page
// load in production.
func ShouldCache(urlPath string) bool {
	if urlPath == "/index.html" || urlPath == "/" {
		return false
	}
	if strings.HasPrefix(urlPath, "/assets/") || strings.HasPrefix(urlPath, "/fonts/") {
		return true
	}
	return false
}
