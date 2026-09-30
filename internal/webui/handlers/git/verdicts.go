package git

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
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
		followVerdict(w, req, store, v, body.Lead)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": v, "status": "recorded"})
}

func followVerdict(w http.ResponseWriter, req *http.Request, store *review.Local, verdict loomgit.Verdict, lead string) {
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
		if len(followed.Pending) > 0 {
			paused, err := store.FollowingPaused(req.Context(), verdict.Workspace, lead)
			if err != nil {
				writeReviewError(w, err)
				return
			}
			status = "approved_waiting_for_dependency"
			if paused {
				status = "approved_paused"
			}
		} else {
			status = "applied"
		}
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": verdict, "status": status})
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
	revisions, err := store.TaskRevisions(req.Context(), req.PathValue("ws"), req.PathValue("id"))
	if err != nil {
		writeReviewError(w, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": revisions})
}
