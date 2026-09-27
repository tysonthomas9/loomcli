//go:build daemon_bugreplay

package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// Found bug #23 (#129): a live own-lock conflict resumes work, but the issue
// projection must also show the active worker instead of a free task.
func TestBugReplay_Found23_OwnLockedResumeRepairsBoard(t *testing.T) {
	issue := backend.IssueData{ID: "task-1", Status: "open"}
	mock := clitest.NewMockIssueBackend()
	mock.ClaimIssueFn = func(context.Context, string, time.Duration) error {
		return &backend.BackendError{Kind: backend.KindConflict, Op: "ClaimIssue", Meta: map[string]string{"existing_owner": "worker-1"}}
	}
	mock.UpdateFn = func(_ context.Context, id string, patch backend.UpdateParams) error {
		if id != issue.ID {
			t.Fatalf("updated issue %q, want %q", id, issue.ID)
		}
		if patch.Status != nil {
			issue.Status = *patch.Status
		}
		if patch.Assignee != nil {
			issue.Assignee = *patch.Assignee
		}
		return nil
	}
	s := &Supervisor{IssueBackend: mock}
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "worker-1", Role: "task"}}
	if !s.claimResumeTask(ap, issue.ID) {
		t.Fatal("own-locked task was not resumed")
	}
	if ap.AssignedTaskID != issue.ID {
		t.Fatalf("agent assigned task = %q, want %q", ap.AssignedTaskID, issue.ID)
	}
	if issue.Status != "in_progress" || issue.Assignee != "worker-1" {
		t.Fatalf("resumed task board projection = status %q, assignee %q; want in_progress / worker-1", issue.Status, issue.Assignee)
	}
}
