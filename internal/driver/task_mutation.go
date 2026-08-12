package driver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

// DriverTaskRunCompletionOptions parameterizes the fenced completion of a
// deferred TaskRun through the TaskRun store (driver complete-task).
type DriverTaskRunCompletionOptions struct {
	TaskID       string
	CompletionID string
	LeaseToken   string
	ArtifactIDs  []string
	LogsRef      string
	ArtifactsRef string
	Reason       string
}

// CompleteDriverTaskRun finalizes a deferred TaskRun via the fenced
// TaskRunStore Complete path, closing the underlying FleetDB task. Shared by
// the driver CLI complete-task subcommand and the driver-op HTTP API.
func CompleteDriverTaskRun(ctx context.Context, taskRuns store.TaskRunStore, ws, taskRunID string, opts DriverTaskRunCompletionOptions) (*TaskMutationResult, error) {
	taskRun, err := taskRuns.Get(ctx, ws, taskRunID)
	if err != nil {
		return nil, fmt.Errorf("get task run: %w", err)
	}
	if opts.TaskID != "" && taskRun.TaskID != opts.TaskID {
		return nil, fmt.Errorf("task run %q belongs to task %q, not %q: %w", taskRunID, taskRun.TaskID, opts.TaskID, domain.ErrInvalid)
	}
	completionID := strings.TrimSpace(opts.CompletionID)
	if completionID == "" {
		completionID = "complete-" + taskRunID
	}
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		reason = "completed by driver"
	}
	completed, err := taskRuns.Complete(ctx, ws, taskRunID, store.TaskRunComplete{
		CompletionID:        completionID,
		NodeID:              taskRun.NodeID,
		LeaseID:             taskRun.LeaseID,
		LeaseToken:          opts.LeaseToken,
		FencingToken:        taskRun.FencingToken,
		Status:              domain.TaskRunCompleted,
		LogsRef:             opts.LogsRef,
		ArtifactsRef:        opts.ArtifactsRef,
		RequiredArtifactIDs: opts.ArtifactIDs,
		RequireArtifacts:    len(opts.ArtifactIDs) > 0,
		CloseTask:           true,
		CloseReason:         reason,
	})
	if err != nil {
		return nil, err
	}
	return &TaskMutationResult{ID: completed.TaskID, Status: string(completed.Status), Reason: reason}, nil
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

type TaskClaimOptions struct {
	EpicID string
	Actor  string
	// Type optionally narrows the ready view to a single issue type (e.g.
	// "bug"), filtered server-side via ReadyOpts.Type. Empty means no filter.
	Type    string
	Limit   int
	LockTTL time.Duration
	// ExcludeLabels skips ready tasks carrying ANY of these labels.
	//
	// The epic-runner claims label-blind: any open, unblocked child of the
	// epic is fair game. That is correct for a fan-out of independent tasks
	// and wrong for an epic whose children are mid-flight in a
	// label-routed daemon pipeline — a task stamped by one stage and waiting
	// for the next is still "ready", so a Run Epic dispatches it to a generic
	// implementer and closes it, skipping the remaining stages.
	//
	// Filtering here rather than in the ready query is deliberate: the server
	// ready view has no exclusion filter, and the set is already in hand.
	ExcludeLabels []string
}

type ClaimedTask struct {
	ID         string    `json:"id"`
	Title      string    `json:"title,omitempty"`
	Status     string    `json:"status,omitempty"`
	Priority   int       `json:"priority,omitempty"`
	IssueType  string    `json:"issueType,omitempty"`
	Assignee   string    `json:"assignee,omitempty"`
	Labels     []string  `json:"labels,omitempty"`
	SourceRepo string    `json:"sourceRepo,omitempty"`
	Parent     string    `json:"parent,omitempty"`
	ClaimedBy  string    `json:"claimedBy,omitempty"`
	ClaimedAt  time.Time `json:"claimedAt,omitempty"`
}

type actorClaimer interface {
	ClaimIssueAsActor(context.Context, string, time.Duration, string) error
}

func ClaimReadyTask(ctx context.Context, issueBackend backend.IssueBackend, opts TaskClaimOptions) (*ClaimedTask, error) {
	if issueBackend == nil {
		return nil, fmt.Errorf("issue backend required: %w", domain.ErrInvalid)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultClaimReadyLimit
	}
	ready, err := issueBackend.Ready(ctx, backend.ReadyOpts{
		ParentID: strings.TrimSpace(opts.EpicID),
		Type:     strings.TrimSpace(opts.Type),
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	actor := strings.TrimSpace(opts.Actor)
	excluded := normalizeLabelSet(opts.ExcludeLabels)
	for _, issue := range ready {
		if strings.TrimSpace(issue.ID) == "" {
			continue
		}
		if hasAnyLabel(issue.Labels, excluded) {
			continue
		}
		// Blocked issues are excluded server-side from the ready view; skip
		// them here too in case a backend still returns them.
		if strings.EqualFold(strings.TrimSpace(issue.Status), blockedIssueStatus) {
			continue
		}
		err := claimIssue(ctx, issueBackend, issue.ID, opts.LockTTL, actor)
		if err == nil {
			return claimedTaskFromIssue(issue, actor), nil
		}
		if backend.IsKind(err, backend.KindConflict) {
			continue
		}
		return nil, fmt.Errorf("claim ready task %q: %w", issue.ID, err)
	}
	return nil, nil
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
	if issueBackend == nil {
		return nil, fmt.Errorf("issue backend required: %w", domain.ErrInvalid)
	}
	taskID := strings.TrimSpace(opts.TaskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id required: %w", domain.ErrInvalid)
	}
	// Default to a router-scale scan so a crowded ready view cannot hide a
	// genuinely-ready target past the small claim-ready cutoff (false 409). An
	// explicit caller limit still narrows the scan.
	limit := opts.Limit
	if limit <= 0 {
		limit = claimByIDReadyScanDepth
	}
	ready, err := issueBackend.Ready(ctx, backend.ReadyOpts{ParentID: strings.TrimSpace(opts.EpicID), Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	var target *backend.IssueData
	for i := range ready {
		if strings.TrimSpace(ready[i].ID) == taskID {
			target = &ready[i]
			break
		}
	}
	// Not in the ready view — or explicitly blocked (some backends still list
	// blocked issues) — means not claimable right now: not ready or already
	// claimed. Fail conflict-class so the caller can distinguish it from a
	// transport error.
	if target == nil || strings.EqualFold(strings.TrimSpace(target.Status), blockedIssueStatus) {
		return nil, fmt.Errorf("task %q is not ready or already claimed: %w", taskID, domain.ErrConflict)
	}
	actor := strings.TrimSpace(opts.Actor)
	if err := claimIssue(ctx, issueBackend, target.ID, opts.LockTTL, actor); err != nil {
		if backend.IsKind(err, backend.KindConflict) {
			return nil, fmt.Errorf("task %q is already claimed: %w", taskID, domain.ErrConflict)
		}
		return nil, fmt.Errorf("claim task %q: %w", taskID, err)
	}
	return claimedTaskFromIssue(*target, actor), nil
}

// normalizeLabelSet lowercases and trims a label list into a lookup set,
// dropping blanks. Labels compare case-insensitively so a pipeline label
// configured as "Criticized" still guards a task stamped "criticized".
func normalizeLabelSet(labels []string) map[string]struct{} {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			out[l] = struct{}{}
		}
	}
	return out
}

func hasAnyLabel(labels []string, set map[string]struct{}) bool {
	if len(set) == 0 {
		return false
	}
	for _, l := range labels {
		if _, ok := set[strings.ToLower(strings.TrimSpace(l))]; ok {
			return true
		}
	}
	return false
}

func claimIssue(ctx context.Context, issueBackend backend.IssueBackend, issueID string, lockTTL time.Duration, actor string) error {
	if actor != "" {
		if actorBackend, ok := issueBackend.(actorClaimer); ok {
			return actorBackend.ClaimIssueAsActor(ctx, issueID, lockTTL, actor)
		}
	}
	return issueBackend.ClaimIssue(ctx, issueID, lockTTL)
}

func claimedTaskFromIssue(issue backend.IssueData, actor string) *ClaimedTask {
	return &ClaimedTask{
		ID:         issue.ID,
		Title:      issue.Title,
		Status:     "in_progress",
		Priority:   issue.Priority,
		IssueType:  issue.IssueType,
		Assignee:   issue.Assignee,
		Labels:     append([]string(nil), issue.Labels...),
		SourceRepo: issue.SourceRepo,
		Parent:     issue.Parent,
		ClaimedBy:  actor,
		ClaimedAt:  time.Now().UTC(),
	}
}
