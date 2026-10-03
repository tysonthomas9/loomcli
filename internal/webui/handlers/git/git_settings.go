package git

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

var readGitSettings = publish.GitSettingsLocal
var writeGitSettings = publish.SetGitSettingsLocal

type gitSettingsRequest struct {
	publish.GitSettingsChange
	Actor review.Actor `json:"actor"`
}

// handleGitSettings reads (GET) or changes (PUT) the workspace Git settings.
// The reported actor is trusted in local mode, so human-only is advisory (D28).
func handleGitSettings(w http.ResponseWriter, r *http.Request) {
	workspace := middleware.WorkspaceFromContext(r.Context())
	if r.Method == http.MethodGet {
		settings, err := readGitSettings(r.Context(), workspace)
		if err != nil {
			writeMergeResult(w, nil, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, settings)
		return
	}
	var request gitSettingsRequest
	if r.Body == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request) != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid git settings")
		return
	}
	settings, warning, err := writeGitSettings(r.Context(), workspace, request.GitSettingsChange,
		review.Actor(reportedHuman(publish.MergeActor(request.Actor))), nil)
	var coded *loomgit.Error
	switch {
	case errors.As(err, &coded) && coded.Kind == loomgit.MergeNotAuthorized:
		handler.RespondError(w, http.StatusForbidden, err.Error())
	case coded != nil:
		handler.RespondError(w, http.StatusConflict, err.Error())
	case err != nil:
		handler.RespondError(w, http.StatusBadRequest, err.Error())
	default:
		handler.WriteJSON(w, http.StatusOK, map[string]any{"settings": settings, "warning": warning})
	}
}
