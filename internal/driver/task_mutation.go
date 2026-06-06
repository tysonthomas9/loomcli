package driver

import (
	"context"
	"fmt"
	"strings"

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
