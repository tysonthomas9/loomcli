package misc

import (
	"net/http"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/webui/frontendassets"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// HandleBuildInfo returns the frontend asset identity served by this process.
// The served bundle is immutable for the process lifetime (a new binary
// restarts the process), so the content hash is computed once and cached
// instead of re-hashing the whole frontend tree on every poll.
func HandleBuildInfo(frontendDir string, build string) http.HandlerFunc {
	var (
		once sync.Once
		info frontendassets.BuildInfo
	)
	return func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() {
			info = frontendassets.ReadBuildInfo(frontendDir, build)
		})
		w.Header().Set("Cache-Control", "no-store")
		handler.WriteJSON(w, http.StatusOK, info)
	}
}
