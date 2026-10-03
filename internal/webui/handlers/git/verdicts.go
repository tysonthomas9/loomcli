package git

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func writeReviewError(w http.ResponseWriter, err error) {
	code, status := "internal_error", http.StatusInternalServerError
	var coded *loomgit.Error
	if errors.As(err, &coded) {
		code, status = coded.Code(), http.StatusConflict
	} else if review.IsNotFound(err) {
		code, status = "not_found", http.StatusNotFound
	}
	handler.WriteJSON(w, status, map[string]any{"success": false, "error": code})
}

type verdictRequest struct {
	HeadSHA string       `json:"head_sha"`
	Verdict string       `json:"verdict"`
	Reason  string       `json:"reason"`
	Actor   review.Actor `json:"actor"`
	Lead    string       `json:"lead"`
}

var followApproved = apply.FollowLocal
var hasWorkingArea = func(ctx context.Context, store *review.Local, workspace, lead string) (bool, error) {
	areas, err := store.WorkingAreas(ctx, workspace, lead)
	return len(areas) > 0, err
}

func handleVerdict(w http.ResponseWriter, req *http.Request) {
	handleVerdictWithPublisher(w, req, publish.ReconcileEpicLead)
}

func handleVerdictWithPublisher(w http.ResponseWriter, req *http.Request, publisher func(context.Context, string, string) error) {
	number, err := strconv.Atoi(req.PathValue("r"))
	if err != nil || number < 1 {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid_revision"})
		return
	}
	var body verdictRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.HeadSHA == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid_verdict"})
		return
	}
	store, err := review.OpenLocal()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	if body.Lead == "" {
		body.Lead = "lead"
		if body.Actor.Kind == "lead" {
			body.Lead = body.Actor.ID
		}
	}
	v, err := store.SubmitForLead(req.Context(), req.PathValue("ws"), req.PathValue("change"), number,
		body.HeadSHA, body.Verdict, body.Reason, body.Actor, body.Lead)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	if v.Kind == "approve" || v.Kind == "override" || v.Kind == "policy" {
		followVerdict(w, req, store, v, body.Lead, publisher)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": v, "status": "recorded"})
}

func followVerdict(w http.ResponseWriter, req *http.Request, store *review.Local, verdict loomgit.Verdict,
	lead string, publisher func(context.Context, string, string) error) {
	status := "approved_waiting_for_working_area"
	available, areaErr := hasWorkingArea(req.Context(), store, verdict.Workspace, lead)
	if areaErr != nil {
		writeReviewError(w, areaErr)
		return
	}
	if available {
		followed, followErr := followApproved(req.Context(), verdict.Workspace, lead)
		if followErr != nil {
			code := "follow_failed"
			var coded *loomgit.Error
			if errors.As(followErr, &coded) {
				code = coded.Code()
			}
			handler.WriteJSON(w, http.StatusConflict, map[string]any{
				"success": false, "error": code, "paths": followed.Paths,
				"suggestion": "Create a fix-up task from the conflicting paths and approve its new revision.",
			})
			return
		}
		var reason string
		var err error
		if status, reason, err = followStatus(req.Context(), store, verdict, lead, followed); err != nil {
			writeReviewError(w, err)
			return
		}
		if status == "spent" {
			// Never applied: report why, and never publish it.
			handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": verdict, "status": status, "reason": reason})
			return
		}
		if status == "applied" {
			if err := publisher(req.Context(), verdict.Workspace, lead); err != nil {
				handler.WriteJSON(w, http.StatusConflict, map[string]any{"success": false,
					"error": "publish_failed", "message": err.Error(), "status": status})
				return
			}
		}
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": verdict, "status": status})
}

var approvalFollowState = func(ctx context.Context, store *review.Local, workspace, lead, change string, revision int) (string, string, error) {
	if store == nil {
		return "", "", nil
	}
	return store.ApprovalFollowState(ctx, workspace, lead, change, revision)
}

// followStatus reports what following did for this verdict's change. It is
// "applied" only when the change was applied now or its follow is still
// applied; a spent follow reports its reason; otherwise the approval waits.
func followStatus(ctx context.Context, store *review.Local, verdict loomgit.Verdict, lead string,
	followed apply.FollowResult) (string, string, error) {
	if slices.Contains(followed.Applied, verdict.Change) {
		return "applied", "", nil
	}
	for _, spent := range followed.Spent {
		if spent.Change == verdict.Change {
			return "spent", spent.Reason, nil
		}
	}
	if len(followed.Pending) > 0 {
		paused, err := store.FollowingPaused(ctx, verdict.Workspace, lead)
		if err != nil || !paused {
			return "approved_waiting_for_dependency", "", err
		}
		return "approved_paused", "", nil
	}
	state, reason, err := approvalFollowState(ctx, store, verdict.Workspace, lead, verdict.Change, verdict.Number)
	if err != nil || (state != "applied" && state != "spent") {
		return "approved", "", err
	}
	return state, reason, nil
}

func handleFollowing(w http.ResponseWriter, req *http.Request) {
	lead := req.PathValue("lead")
	if lead == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "lead_required"})
		return
	}
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid_following"})
		return
	}
	store, err := review.OpenLocal()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	workspace := req.PathValue("ws")
	if err := store.SetFollowingPaused(req.Context(), workspace, lead, body.Paused); err != nil {
		writeReviewError(w, err)
		return
	}
	if !body.Paused {
		if _, err := followApproved(req.Context(), workspace, lead); err != nil {
			writeReviewError(w, err)
			return
		}
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "paused": body.Paused})
}

func handleTaskRevisions(w http.ResponseWriter, req *http.Request) {
	store, err := review.OpenLocal()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	revisions, err := store.TaskRevisionsForLead(req.Context(), req.PathValue("ws"), req.PathValue("id"), req.URL.Query().Get("lead"))
	if err != nil {
		writeReviewError(w, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": revisions})
}
