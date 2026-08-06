package workspace

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// HandleSetDefaultWorkspace keeps the removed endpoint explicit for older
// clients without retaining a disabled mutation facade.
func HandleSetDefaultWorkspace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.WriteJSON(w, http.StatusGone, WorkspaceResponse{Success: false, Error: "default workspace selection has been removed; use an explicit workspace"})
	}
}

// HandleClearDefaultWorkspace reports the same retired contract for DELETE.
func HandleClearDefaultWorkspace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.WriteJSON(w, http.StatusGone, WorkspaceResponse{Success: false, Error: "default workspace selection has been removed; use an explicit workspace"})
	}
}
