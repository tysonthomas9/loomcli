package git

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	// ApproveOnly applies an approval without opening its PR (D29 Approve
	// only). By default an approval opens the PR as soon as it applies.
	ApproveOnly bool `json:"approve_only"`
}

var followApproved = apply.FollowLocal
var publishApproved = publish.PublishApproved
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
	v, err := store.SubmitForLeadPublishing(req.Context(), req.PathValue("ws"), req.PathValue("change"), number,
		body.HeadSHA, body.Verdict, body.Reason, body.Actor, body.Lead, !body.ApproveOnly)
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
			if err := publisher(req.Context(), verdict.Workspace, lead); err != nil {
				handler.WriteJSON(w, http.StatusConflict, map[string]any{"success": false,
					"error": "publish_failed", "message": err.Error(), "status": status})
				return
			}
		}
	}
	writeApprovalResponse(w, req, verdict, lead, status, available)
}

// writeApprovalResponse opens the PR an Approve and create PR verdict asked
// for, once its working area exists, and reports the outcome with the status.
func writeApprovalResponse(w http.ResponseWriter, req *http.Request, verdict loomgit.Verdict, lead, status string, available bool) {
	response := map[string]any{"success": true, "data": verdict, "status": status}
	if verdict.Publish && available {
		outcome, err := publishVerdict(req.Context(), verdict, lead)
		if err != nil {
			// The approval and apply stand; the background reconciler retries the PR.
			handler.WriteJSON(w, http.StatusConflict, map[string]any{"success": false,
				"error": "publish_failed", "message": err.Error(), "status": status, "publish": outcome})
			return
		}
		if outcome.Status == "published" {
			response["status"] = "published"
		}
		if outcome.Status != "" {
			response["publish"] = outcome
		}
	}
	handler.WriteJSON(w, http.StatusOK, response)
}

// publishVerdict opens the PR an Approve and create PR verdict asked for once
// its change is applied; a held apply leaves the intent for the reconciler.
func publishVerdict(ctx context.Context, verdict loomgit.Verdict, lead string) (publish.ApprovalOutcome, error) {
	outcomes, err := publishApproved(ctx, verdict.Workspace, lead)
	for _, outcome := range outcomes {
		if outcome.Change == verdict.Change {
			if err != nil && outcome.Status != "pending" {
				// Another change's publish failed; this one is settled.
				err = nil
			}
			return outcome, err
		}
	}
	// Not published yet (held, or the journal could not be read): the
	// reconciler retries it, so the approval itself still succeeded.
	return publish.ApprovalOutcome{}, nil
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
	if reader, closeReader, err := openRevisionReader(); err == nil {
		for i := range revisions {
			revisions[i].Date, _ = reader.CommitDate(req.Context(), req.PathValue("ws"), revisions[i].Repo, revisions[i].HeadSHA)
		}
		_ = closeReader()
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": revisions})
}
