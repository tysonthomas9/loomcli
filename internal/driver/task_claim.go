package driver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

const defaultClaimReadyLimit = 100

// parkedIssueStatus is fleet-db's explicit parked issue status: task runs
// exhausted their retry budget and the issue is held for human review.
// Un-parking is a human action (move the issue back to open).
const parkedIssueStatus = "parked"

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
	ready, err := issueBackend.Ready(ctx, backend.ReadyOpts{ParentID: strings.TrimSpace(opts.EpicID), Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list ready tasks: %w", err)
	}
	actor := strings.TrimSpace(opts.Actor)
	for _, issue := range ready {
		if strings.TrimSpace(issue.ID) == "" {
			continue
		}
		// Parked issues (retry budget exhausted, held for human review)
		// are excluded server-side from the ready view; skip them here
		// too in case a backend still returns them.
		if strings.EqualFold(strings.TrimSpace(issue.Status), parkedIssueStatus) {
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
