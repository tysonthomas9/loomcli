package git

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// taskApprovalRequest approves a task's newest revision in every repo at
// once (P2.23): all of them are published, or none is.
type taskApprovalRequest struct {
	Verdict   string                 `json:"verdict"`
	Reason    string                 `json:"reason"`
	Actor     review.Actor           `json:"actor"`
	Lead      string                 `json:"lead"`
	Revisions []taskApprovalRevision `json:"revisions"`
}

type taskApprovalRevision struct {
	ChangeID string `json:"change_id"`
	Number   int    `json:"number"`
	HeadSHA  string `json:"head_sha"`
}

// approvalFailure names a repo whose approval did not apply.
type approvalFailure struct {
	Repo   string   `json:"repo"`
	Change string   `json:"change"`
	Status string   `json:"status"`
	Paths  []string `json:"paths,omitempty"`
}

func handleTaskApproval(w http.ResponseWriter, req *http.Request) {
	handleTaskApprovalWithPublisher(w, req, publish.ReconcileEpicLead)
}

// handleTaskApprovalWithPublisher records every repo's approval without a
// publish intent and applies them. Only when every repo applied does it ask
// for the PRs; otherwise no repo is published and the reply names the repo
// that did not apply. A repo applied before another failed stays applied in
// the lead's working area, with no PR, until the task is approved again.
func handleTaskApprovalWithPublisher(w http.ResponseWriter, req *http.Request, publisher func(context.Context, string, string) error) {
	body, ok := decodeTaskApproval(req)
	if !ok {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid_approval"})
		return
	}
	store, err := review.OpenLocal()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	ctx, workspace := req.Context(), req.PathValue("ws")
	repos, err := taskApprovalRepos(ctx, store, workspace, req.PathValue("id"), body)
	if err != nil {
		handler.WriteJSON(w, http.StatusConflict, map[string]any{"success": false, "error": "stale_revision", "message": err.Error()})
		return
	}
	available, err := hasWorkingArea(ctx, store, workspace, body.Lead)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	// No working area: nothing can apply now, so record the PR intent and
	// let each repo publish as it applies, as a one-repo approval does.
	verdicts, err := recordTaskApproval(ctx, store, workspace, body, !available)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() {
		for _, v := range verdicts {
			settleVerdictTask(ctx, v)
		}
	}()
	if !available {
		handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "status": "approved_waiting_for_working_area"})
		return
	}
	failed, err := applyTaskApproval(ctx, store, workspace, body.Lead, verdicts, repos)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	if len(failed) > 0 {
		handler.WriteJSON(w, http.StatusConflict, map[string]any{
			"success": false, "error": "not_all_applied", "status": "recorded", "repo": failed[0].Repo,
			"failed": failed, "data": map[string]string{"Kind": body.Verdict}, "message": notAllAppliedMessage(failed[0]),
		})
		return
	}
	publishTaskApproval(w, req, store, body, verdicts, publisher)
}

// decodeTaskApproval reads an approve or override of at least one revision.
func decodeTaskApproval(req *http.Request) (taskApprovalRequest, bool) {
	var body taskApprovalRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body.Revisions) == 0 ||
		(body.Verdict != "approve" && body.Verdict != "override") {
		return body, false
	}
	if body.Lead == "" {
		body.Lead = "lead"
	}
	return body, true
}

// recordTaskApproval records every repo's verdict, with or without the PR
// intent.
func recordTaskApproval(ctx context.Context, store *review.Local, workspace string, body taskApprovalRequest,
	publishNow bool) ([]loomgit.Verdict, error) {
	verdicts := make([]loomgit.Verdict, 0, len(body.Revisions))
	for _, r := range body.Revisions {
		v, err := store.SubmitForLeadPublishing(ctx, workspace, r.ChangeID, r.Number, r.HeadSHA,
			body.Verdict, body.Reason, body.Actor, body.Lead, publishNow)
		if err != nil {
			return verdicts, err
		}
		verdicts = append(verdicts, v)
	}
	return verdicts, nil
}

// taskApprovalRepos checks each revision is its repo's newest for the task
// and returns the repo of each change.
func taskApprovalRepos(ctx context.Context, store *review.Local, workspace, task string, body taskApprovalRequest) (map[string]string, error) {
	revisions, err := store.TaskRevisionsForLead(ctx, workspace, task, body.Lead)
	if err != nil {
		return nil, err
	}
	repos := map[string]string{}
	for _, want := range body.Revisions {
		i := slices.IndexFunc(revisions, func(r review.TaskRevision) bool {
			return r.ChangeID == want.ChangeID && r.Number == want.Number && r.HeadSHA == want.HeadSHA && !r.Superseded
		})
		if i < 0 {
			return nil, errors.New("revision " + want.ChangeID + " is not this task's newest; reload and review again")
		}
		repos[want.ChangeID] = revisions[i].Repo
	}
	return repos, nil
}

// applyTaskApproval applies every approval and returns the repos that did
// not apply, in request order.
func applyTaskApproval(ctx context.Context, store *review.Local, workspace, lead string,
	verdicts []loomgit.Verdict, repos map[string]string) ([]approvalFailure, error) {
	followed, followErr := followApproved(ctx, workspace, lead)
	code := ""
	var coded *loomgit.Error
	if errors.As(followErr, &coded) {
		code = coded.Code()
	} else if followErr != nil {
		code = "follow_failed"
	}
	var failed []approvalFailure
	for _, v := range verdicts {
		if slices.Contains(followed.Applied, v.Change) {
			continue
		}
		status, _, err := followStatus(ctx, store, v, lead, followed)
		if err != nil {
			return nil, err
		}
		if status == "applied" {
			continue
		}
		if state, _, err := approvalFollowState(ctx, store, workspace, lead, v.Change, v.Number); err == nil && state != "" && state != "approved" {
			status = state
		} else if code != "" {
			status = code
		}
		failed = append(failed, approvalFailure{Repo: repos[v.Change], Change: v.Change, Status: status, Paths: followed.Paths})
	}
	return failed, nil
}

// notAllAppliedMessage names the repo that did not apply and says no repo
// is published.
func notAllAppliedMessage(f approvalFailure) string {
	why := "it could not be applied to the lead's working area"
	switch f.Status {
	case string(loomgit.Conflict):
		why = "it conflicts with the lead's current code"
	case string(loomgit.ApplyPending):
		why = "the lead's working area has unsaved edits to the same files"
	case "spent":
		why = "its apply request can no longer apply"
	}
	message := f.Repo + ": couldn't apply: " + why
	if len(f.Paths) > 0 {
		message += " (" + strings.Join(f.Paths, ", ") + ")"
	}
	return message + ". No PR is opened for any repo until every repo applies."
}

// publishTaskApproval asks for every repo's PR now that all of them applied.
func publishTaskApproval(w http.ResponseWriter, req *http.Request, store *review.Local, body taskApprovalRequest,
	verdicts []loomgit.Verdict, publisher func(context.Context, string, string) error) {
	ctx, workspace := req.Context(), req.PathValue("ws")
	if err := publisher(ctx, workspace, body.Lead); err != nil {
		handler.WriteJSON(w, http.StatusConflict, map[string]any{"success": false,
			"error": "publish_failed", "message": err.Error(), "status": "applied"})
		return
	}
	for i, v := range verdicts {
		// Approving the same head again records the PR intent (D40).
		armed, err := store.SubmitForLeadPublishing(ctx, workspace, v.Change, v.Number, v.HeadSHA,
			body.Verdict, body.Reason, body.Actor, body.Lead, true)
		if err != nil {
			writeReviewError(w, err)
			return
		}
		verdicts[i] = armed
	}
	// One publish pass opens every applied repo's PR.
	all, err := publishApproved(ctx, workspace, body.Lead, stackstore.Declared())
	outcomes := make([]publish.ApprovalOutcome, 0, len(verdicts))
	status := "published"
	for _, v := range verdicts {
		i := slices.IndexFunc(all, func(o publish.ApprovalOutcome) bool { return o.Change == v.Change })
		if i < 0 {
			status = "applied"
			continue
		}
		if all[i].Status != "published" {
			status = "applied"
		}
		outcomes = append(outcomes, all[i])
	}
	if err != nil {
		// The approvals and applies stand; the reconciler retries the PRs.
		handler.WriteJSON(w, http.StatusConflict, map[string]any{"success": false,
			"error": "publish_failed", "message": err.Error(), "status": "applied", "publish": outcomes})
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "status": status, "publish": outcomes})
}
