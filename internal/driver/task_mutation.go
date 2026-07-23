package driver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

type TaskCompleteOptions struct {
	TaskID  string
	Reason  string
	Session string
	Force   bool
}

type TaskReleaseOptions struct {
	TaskID string
	Actor  string
}

type TaskMutationResult struct {
	ID       string `json:"id"`
	Status   string `json:"status,omitempty"`
	Released bool   `json:"released,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type actorReleaser interface {
	ReleaseIssueAsActor(context.Context, string, string) error
}

func CompleteTask(ctx context.Context, issueBackend backend.IssueBackend, opts TaskCompleteOptions) (*TaskMutationResult, error) {
	if issueBackend == nil {
		return nil, fmt.Errorf("issue backend required: %w", domain.ErrInvalid)
	}
	taskID := strings.TrimSpace(opts.TaskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id required: %w", domain.ErrInvalid)
	}
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		reason = "completed by driver"
	}
	result, err := issueBackend.Close(ctx, taskID, backend.CloseParams{
		Reason:  reason,
		Session: opts.Session,
		Force:   opts.Force,
	})
	if err != nil {
		return nil, fmt.Errorf("complete task %q: %w", taskID, err)
	}
	status := ""
	if result != nil && result.Closed != nil {
		status = result.Closed.Status
	}
	return &TaskMutationResult{ID: taskID, Status: status, Reason: reason}, nil
}

func ReleaseTask(ctx context.Context, issueBackend backend.IssueBackend, opts TaskReleaseOptions) (*TaskMutationResult, error) {
	if issueBackend == nil {
		return nil, fmt.Errorf("issue backend required: %w", domain.ErrInvalid)
	}
	taskID := strings.TrimSpace(opts.TaskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id required: %w", domain.ErrInvalid)
	}
	actor := strings.TrimSpace(opts.Actor)
	if actor != "" {
		if actorBackend, ok := issueBackend.(actorReleaser); ok {
			if err := actorBackend.ReleaseIssueAsActor(ctx, taskID, actor); err != nil {
				return nil, fmt.Errorf("release task %q: %w", taskID, err)
			}
			return &TaskMutationResult{ID: taskID, Released: true}, nil
		}
	}
	if err := issueBackend.ReleaseIssueLock(ctx, taskID, actor); err != nil {
		return nil, fmt.Errorf("release task %q: %w", taskID, err)
	}
	return &TaskMutationResult{ID: taskID, Released: true}, nil
}

const defaultClaimReadyLimit = 100

// claimByIDReadyScanDepth is the default ready-view scan depth for the
// targeted claim-by-id path (ClaimTask). It matches the router's own depth
// (see cli.FetchReadyIssues, which scans 10000 for the same crowding reason):
// ready queues fold in open + review + in_progress items, so review work can
// crowd the few truly-workable tasks past a small cutoff. Scanning only the
// first defaultClaimReadyLimit (100) entries makes a targeted claim of a task
// at position >100 silently return a conflict (false 409) that is
// indistinguishable from an honest race. There is no cheaper per-id readiness
// probe on IssueBackend — Ready() is the canonical gate and is not keyed
// finer than its narrowing filters — so we scan router-scale by default.
const claimByIDReadyScanDepth = 10000

const blockedIssueStatus = "blocked"

func oneNonEmptyString(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

type TaskClaimOptions struct {
	EpicID string
	Actor  string
	// Type optionally narrows the ready view to a single issue type (e.g.
	// "bug"), filtered server-side via ReadyOpts.Type. Empty means no filter.
	Type string
	// SourceRepo optionally narrows ready work to one workspace repository.
	SourceRepo string
	Limit      int
	LockTTL    time.Duration
}

type ClaimedTask struct {
	ID            string    `json:"id"`
	Title         string    `json:"title,omitempty"`
	Status        string    `json:"status,omitempty"`
	Priority      int       `json:"priority,omitempty"`
	IssueType     string    `json:"issueType,omitempty"`
	Assignee      string    `json:"assignee,omitempty"`
	Labels        []string  `json:"labels,omitempty"`
	SourceRepo    string    `json:"sourceRepo,omitempty"`
	Parent        string    `json:"parent,omitempty"`
	ClaimedBy     string    `json:"claimedBy,omitempty"`
	ClaimedAt     time.Time `json:"claimedAt,omitempty"`
	ClaimActionID string    `json:"claimActionId,omitempty"`
}

type actorClaimer interface {
	ClaimIssueAsActor(context.Context, string, time.Duration, string) error
}

func ClaimReadyTask(ctx context.Context, issueBackend backend.IssueBackend, opts TaskClaimOptions) (*ClaimedTask, error) {
	ready, err := ReadyTaskCandidates(ctx, issueBackend, opts)
	if err != nil {
		return nil, err
	}
	actor := strings.TrimSpace(opts.Actor)
	for _, issue := range ready {
		err := claimIssue(ctx, issueBackend, issue.ID, opts.LockTTL, actor)
		if err == nil {
			return ClaimedTaskFromIssue(issue, actor, "", time.Now().UTC()), nil
		}
		if backend.IsKind(err, backend.KindConflict) {
			continue
		}
		return nil, fmt.Errorf("claim ready task %q: %w", issue.ID, err)
	}
	return nil, nil
}

// ReadyTaskCandidates is the read-only readiness gate shared by legacy task
// claim helpers and the typed DriverRun Work Item claim path. It never mutates
// an IssueBackend.
func ReadyTaskCandidates(ctx context.Context, issueBackend backend.IssueBackend, opts TaskClaimOptions) ([]backend.IssueData, error) {
	if issueBackend == nil {
		return nil, fmt.Errorf("issue backend required: %w", domain.ErrInvalid)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultClaimReadyLimit
	}
	ready, err := issueBackend.Ready(ctx, backend.ReadyOpts{
		ParentID:    strings.TrimSpace(opts.EpicID),
		Type:        strings.TrimSpace(opts.Type),
		SourceRepos: oneNonEmptyString(opts.SourceRepo),
		Limit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	filtered := make([]backend.IssueData, 0, len(ready))
	for _, issue := range ready {
		if strings.TrimSpace(issue.ID) == "" || strings.EqualFold(strings.TrimSpace(issue.Status), blockedIssueStatus) {
			continue
		}
		filtered = append(filtered, issue)
	}
	return filtered, nil
}

// TaskClaimByIDOptions parameterizes a targeted claim of one specific ready
// task (claim-by-id). EpicID optionally narrows the ready view (empty = the
// whole workspace ready queue); Limit bounds it (defaults to
// defaultClaimReadyLimit).
type TaskClaimByIDOptions struct {
	TaskID  string
	Actor   string
	EpicID  string
	Limit   int
	LockTTL time.Duration
}

// ClaimTask claims a SPECIFIC ready task by id, taking the SAME task lease as
// ClaimReadyTask (the issue claim lock via claimIssue). Unlike claim-ready — a
// queue-order pull — this targets one task, so event-driven pickup no longer
// needs the racy claim-and-release loop.
//
// It gates on the canonical ready view exactly as ClaimReadyTask does: the
// target must appear in Ready() (and not be blocked). A task that is not ready
// (blocked, closed, dependency-blocked, deferred) or already claimed — hence
// absent from the ready view — fails with a conflict-class error
// (domain.ErrConflict), as does a claim that races another owner
// (backend.KindConflict). A non-existent task also surfaces as a conflict: it
// is simply never in the ready view.
func ClaimTask(ctx context.Context, issueBackend backend.IssueBackend, opts TaskClaimByIDOptions) (*ClaimedTask, error) {
	target, err := ReadyTaskByID(ctx, issueBackend, opts)
	if err != nil {
		return nil, err
	}
	actor := strings.TrimSpace(opts.Actor)
	if err := claimIssue(ctx, issueBackend, target.ID, opts.LockTTL, actor); err != nil {
		if backend.IsKind(err, backend.KindConflict) {
			return nil, fmt.Errorf("task %q is already claimed: %w", target.ID, domain.ErrConflict)
		}
		return nil, fmt.Errorf("claim task %q: %w", target.ID, err)
	}
	return ClaimedTaskFromIssue(*target, actor, "", time.Now().UTC()), nil
}

// ReadyTaskByID finds one exact task in the canonical ready view without
// mutating it. Absence, blocked state, and an already-held claim all share the
// existing conflict-class contract.
func ReadyTaskByID(ctx context.Context, issueBackend backend.IssueBackend, opts TaskClaimByIDOptions) (*backend.IssueData, error) {
	if issueBackend == nil {
		return nil, fmt.Errorf("issue backend required: %w", domain.ErrInvalid)
	}
	taskID := strings.TrimSpace(opts.TaskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id required: %w", domain.ErrInvalid)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = claimByIDReadyScanDepth
	}
	ready, err := issueBackend.Ready(ctx, backend.ReadyOpts{ParentID: strings.TrimSpace(opts.EpicID), Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	for i := range ready {
		if strings.TrimSpace(ready[i].ID) == taskID && !strings.EqualFold(strings.TrimSpace(ready[i].Status), blockedIssueStatus) {
			issue := ready[i]
			return &issue, nil
		}
	}
	return nil, fmt.Errorf("task %q is not ready or already claimed: %w", taskID, domain.ErrConflict)
}

func claimIssue(ctx context.Context, issueBackend backend.IssueBackend, issueID string, lockTTL time.Duration, actor string) error {
	if actor != "" {
		if actorBackend, ok := issueBackend.(actorClaimer); ok {
			return actorBackend.ClaimIssueAsActor(ctx, issueID, lockTTL, actor)
		}
	}
	return issueBackend.ClaimIssue(ctx, issueID, lockTTL)
}

// ClaimedTaskFromIssue builds the stable driver wire response after a typed
// owner-fenced claim has committed. claimedAt should come from the committed
// issue/action envelope, not the caller's body.
func ClaimedTaskFromIssue(issue backend.IssueData, actor, claimActionID string, claimedAt time.Time) *ClaimedTask {
	if claimedAt.IsZero() {
		claimedAt = time.Now().UTC()
	}
	return &ClaimedTask{
		ID:            issue.ID,
		Title:         issue.Title,
		Status:        "in_progress",
		Priority:      issue.Priority,
		IssueType:     issue.IssueType,
		Assignee:      issue.Assignee,
		Labels:        append([]string(nil), issue.Labels...),
		SourceRepo:    issue.SourceRepo,
		Parent:        issue.Parent,
		ClaimedBy:     actor,
		ClaimedAt:     claimedAt,
		ClaimActionID: claimActionID,
	}
}
