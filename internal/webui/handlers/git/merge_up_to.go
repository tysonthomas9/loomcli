package git

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type mergeUpToRequest struct {
	StackID string   `json:"stack_id"`
	Target  string   `json:"target"`
	Heads   []string `json:"heads"`
}

var mergePreview = publish.MergeStackPreviewLocal
var mergeRequest = publish.MergeStackLocal

func handleMergeUpTo(w http.ResponseWriter, r *http.Request) {
	workspace, lead := middleware.WorkspaceFromContext(r.Context()), r.PathValue("name")
	var result publish.MergeStackView
	var err error
	if r.Method == http.MethodGet {
		if r.URL.Query().Get("stack_id") == "" || r.URL.Query().Get("target") == "" {
			handler.RespondError(w, http.StatusBadRequest, "stack_id and target are required")
			return
		}
		result, err = mergePreview(r.Context(), workspace, lead, r.URL.Query().Get("stack_id"), r.URL.Query().Get("target"))
	} else {
		var request mergeUpToRequest
		if r.Body == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request) != nil ||
			request.StackID == "" || request.Target == "" || len(request.Heads) == 0 {
			handler.RespondError(w, http.StatusBadRequest, "stack_id, target and confirmed heads are required")
			return
		}
		result, err = mergeRequest(r.Context(), workspace, lead, request.StackID, request.Target, request.Heads)
	}
	if err != nil {
		var coded *loomgit.Error
		if errors.As(err, &coded) {
			handler.RespondError(w, http.StatusConflict, err.Error())
			return
		}
		writeAgentGitError(w, err, http.StatusBadGateway)
		return
	}
	handler.WriteJSON(w, http.StatusOK, result)
}
