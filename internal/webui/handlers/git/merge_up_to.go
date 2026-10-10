package git

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/user"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type mergeUpToRequest struct {
	Actor publish.MergeActor `json:"actor"`
}

var queueMergeUpTo = publish.QueueMergeUpToLocal
var mergeUpToView = publish.MergeUpToViewLocal
var mergeQueue = publish.MergeQueueLocal
var stackCards = publish.StackCardsLocal

// handleMergeUpTo queues "merge up to this task's PR" (D38): the PR and the
// approved PRs below it, bottom up. It is the one merge queue the PR page's
// button and the lead's `loom merge` share. A lead is refused with
// "Lead may merge is off" while that setting is off.
// GET shows the merge up to the change and its per-layer progress.
func handleMergeUpTo(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		result, err := mergeUpToView(r.Context(), r.PathValue("ws"), r.PathValue("change"))
		writeMergeResult(w, result, err)
		return
	}
	var request mergeUpToRequest
	if r.Body == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request) != nil {
		handler.RespondError(w, http.StatusBadRequest, "actor is required")
		return
	}
	result, err := queueMergeUpTo(r.Context(), r.PathValue("ws"), r.PathValue("change"), reportedHuman(request.Actor))
	writeMergeResult(w, result, err)
}

// handleMergeQueue lists the workspace's queued, running and blocked stack merges.
func handleMergeQueue(w http.ResponseWriter, r *http.Request) {
	result, err := mergeQueue(r.Context(), r.PathValue("ws"))
	writeMergeResult(w, result, err)
}

// handleStacks lists the workspace's published stacks for the PR page's stack view.
func handleStacks(w http.ResponseWriter, r *http.Request) {
	result, err := stackCards(r.Context(), r.PathValue("ws"))
	writeMergeResult(w, result, err)
}

func writeMergeResult(w http.ResponseWriter, result any, err error) {
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

// reportedHuman names a browser human with no ID as the OS user running the
// local server: in local mode the browser user is that user (advisory, D28).
func reportedHuman(actor publish.MergeActor) publish.MergeActor {
	if actor.Kind == "human" && actor.ID == "" {
		actor.ID = localHumanID()
	}
	return actor
}

var localHumanID = func() string {
	if current, err := user.Current(); err == nil {
		return current.Username
	}
	return ""
}
