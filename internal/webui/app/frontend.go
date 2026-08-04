package app

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (app *Server) registerFrontendRoutes() {
	if app.config.FrontendDir == "" {
		return
	}
	app.mux.HandleFunc("/", serveFrontend(app.config.FrontendDir))
}

func serveFrontend(root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		servePath := cleanFrontendPath(r.URL.Path)
		if servePath != "" {
			fullPath := filepath.Join(root, filepath.FromSlash(servePath))
			if isWithinDir(root, fullPath) {
				if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
					http.ServeFile(w, r, fullPath)
					return
				}
			}
		}
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
	}
}

func cleanFrontendPath(raw string) string {
	cleaned := path.Clean("/" + raw)
	if cleaned == "/" || cleaned == "." {
		return ""
	}
	return strings.TrimPrefix(cleaned, "/")
}

func isWithinDir(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
