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
		if actorBackend, ok := issueBackend.(backend.ActorReleaser); ok {
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

const blockedIssueStatus = "blocked"

type TaskClaimOptions struct {
	EpicID  string
	Actor   string
	Limit   int
	LockTTL time.Duration
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

func ClaimReadyTask(ctx context.Context, issueBackend backend.IssueBackend, opts TaskClaimOptions) (*ClaimedTask, error) {
	if issueBackend == nil {
		return nil, fmt.Errorf("issue backend required: %w", domain.ErrInvalid)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultClaimReadyLimit
	}
	ready, err := issueBackend.Ready(ctx, backend.ReadyOpts{ParentID: strings.TrimSpace(opts.EpicID), Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	actor := strings.TrimSpace(opts.Actor)
	for _, issue := range ready {
		if strings.TrimSpace(issue.ID) == "" {
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

func claimIssue(ctx context.Context, issueBackend backend.IssueBackend, issueID string, lockTTL time.Duration, actor string) error {
	if actor != "" {
		if actorBackend, ok := issueBackend.(backend.ActorClaimer); ok {
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
