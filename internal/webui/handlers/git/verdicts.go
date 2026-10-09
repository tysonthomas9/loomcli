package git

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskreview"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
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
	// Merge is Approve and merge on a task whose PR is already open, for a new
	// version that needs approving again (D29 (3)): the merge is approved at
	// this revision's head and waits for the PR to carry it. Human only.
	Merge bool `json:"merge"`
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
	if body.Merge && v.Kind != "reject" {
		// A lead's approval records as policy; approveMerge refuses it (D15).
		if _, err := approveMerge(req.Context(), v.Workspace, body.Lead, v.Change, v.HeadSHA,
			reportedHuman(publish.MergeActor{Kind: body.Actor.Kind, ID: body.Actor.ID})); err != nil {
			// The verdict stands; only the merge approval was refused.
			handler.WriteJSON(w, http.StatusConflict, map[string]any{"success": false,
				"error": "merge_approval_failed", "message": err.Error(), "status": "recorded", "data": v})
			return
		}
	}
	if v.Kind == "approve" || v.Kind == "override" || v.Kind == "policy" {
		followVerdict(w, req, store, v, body.Lead, publisher)
		return
	}
	settleVerdictTask(req.Context(), v)
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": v, "status": "recorded"})
}

// settleTask moves the verdict's task out of code review when the verdict
// decides it (D29); tests replace it.
var settleTask = func(ctx context.Context, workspace, change string) (taskreview.Decision, error) {
	return taskreview.SettleChange(ctx, "", workspace, change)
}

// settleVerdictTask settles the task right after its verdict so the task
// shows its outcome at once. The reconcile loop retries a failure.
func settleVerdictTask(ctx context.Context, verdict loomgit.Verdict) {
	if _, err := settleTask(ctx, verdict.Workspace, verdict.Change); err != nil {
		slog.WarnContext(ctx, "task review settle after verdict failed", "workspace", verdict.Workspace, "change", verdict.Change, "err", err)
	}
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
			// The verdict is recorded; only its apply is held. Say so, and
			// name the paths, so the reviewer sees a decided revision.
			handler.WriteJSON(w, http.StatusConflict, map[string]any{
				"success": false, "error": code, "paths": followed.Paths,
				"status": "recorded", "data": verdict, "message": heldMessage(code, followed.Paths),
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
	writeApprovalResponse(w, req, verdict, lead, status, available)
}

// heldMessage explains an approval that was recorded but could not apply.
func heldMessage(code string, paths []string) string {
	why := "it could not be applied to the lead's working area"
	switch code {
	case string(loomgit.ApplyPending):
		why = "the lead's working area has unsaved edits to the same files"
	case string(loomgit.Conflict):
		why = "it conflicts with the stack"
	}
	message := "Approved, not applied yet: " + why
	if len(paths) > 0 {
		message += " (" + strings.Join(paths, ", ") + ")"
	}
	return message + ". No PR until it applies."
}

// writeApprovalResponse opens the PR an Approve and create PR verdict asked
// for, once its working area exists, and reports the outcome with the status.
func writeApprovalResponse(w http.ResponseWriter, req *http.Request, verdict loomgit.Verdict, lead, status string, available bool) {
	response := map[string]any{"success": true, "data": verdict, "status": status}
	defer settleVerdictTask(req.Context(), verdict)
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
	outcomes, err := publishApproved(ctx, verdict.Workspace, lead, stackstore.Declared())
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
	if reader, closeReader, err := openRevisionReader(); err == nil {
		for i := range revisions {
			revisions[i].Date, _ = reader.CommitDate(req.Context(), req.PathValue("ws"), revisions[i].Repo, revisions[i].HeadSHA)
		}
		_ = closeReader()
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": revisions})
}
