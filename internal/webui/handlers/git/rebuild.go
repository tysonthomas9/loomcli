package git

import (
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// handleRebuild sets a stale dependent aside so its next attempt builds on
// the newest revision of the task it depends on. Never automatic: a human
// (or the lead) asks for it from the task's revisions.
func handleRebuild(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Actor review.Actor `json:"actor"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Actor.ID == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid_rebuild"})
		return
	}
	store, err := review.OpenLocal()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	workspace := req.PathValue("ws")
	result, err := store.Rebuild(req.Context(), workspace, req.PathValue("id"), body.Actor)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	// The reject reopens the task, so the daemon starts the rebuild.
	settleVerdictTask(req.Context(), loomgit.Verdict{Workspace: workspace, Change: result.Change})
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
}
