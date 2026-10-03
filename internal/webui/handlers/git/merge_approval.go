package git

import (
	"encoding/json"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type mergeApprovalRequest struct {
	Lead    string             `json:"lead"`
	HeadSHA string             `json:"head_sha"`
	Actor   publish.MergeActor `json:"actor"`
}

var approveMerge = publish.ApproveMergeLocal
var cancelMergeApproval = publish.CancelMergeApprovalLocal
var mergeApprovalState = publish.MergeApprovalLocal

// handleMergeApproval is Approve and merge on a task whose PR is open (D29
// (3)): POST records a human's approval at the head they saw and merges the PR
// if it is the bottom of its stack; DELETE is Cancel auto-merge; GET reports
// the state. Only a human may approve; the lead never merges this way.
func handleMergeApproval(w http.ResponseWriter, r *http.Request) {
	workspace, change := r.PathValue("ws"), r.PathValue("change")
	if r.Method == http.MethodGet {
		result, err := mergeApprovalState(r.Context(), workspace, change)
		writeMergeApprovalResult(w, result, err)
		return
	}
	var request mergeApprovalRequest
	if r.Body == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request) != nil {
		handler.RespondError(w, http.StatusBadRequest, "actor is required")
		return
	}
	actor := reportedHuman(request.Actor)
	if r.Method == http.MethodDelete {
		result, err := cancelMergeApproval(r.Context(), workspace, change, actor)
		writeMergeApprovalResult(w, result, err)
		return
	}
	if request.Lead == "" || request.HeadSHA == "" {
		handler.RespondError(w, http.StatusBadRequest, "lead and head_sha are required")
		return
	}
	result, err := approveMerge(r.Context(), workspace, request.Lead, change, request.HeadSHA, actor)
	writeMergeApprovalResult(w, result, err)
}

func writeMergeApprovalResult(w http.ResponseWriter, result publish.MergeApprovalView, err error) {
	if err != nil {
		writeMergeResult(w, nil, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
}
