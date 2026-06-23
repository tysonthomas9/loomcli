package misc

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/frontendassets"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// HandleBuildInfo returns the frontend asset identity served by this process.
func HandleBuildInfo(frontendDir string, build string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		handler.WriteJSON(w, http.StatusOK, frontendassets.ReadBuildInfo(frontendDir, build))
	}
}
