package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestDefaultEpicHasReadyTasks_HasTasks(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{{ID: "task-1"}}
	setDefaultIssueBackend(mock)

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Error("expected true, got false")
	}
}

func TestDefaultEpicHasReadyTasks_NoTasks(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{}
	setDefaultIssueBackend(mock)

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if has {
		t.Error("expected false, got true")
	}
}

func TestDefaultEpicHasReadyTasks_TrackerError(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.ReadyErr = fmt.Errorf("timeout")
	setDefaultIssueBackend(mock)

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err == nil {
		t.Fatal("expected error from tracker")
	}
	if has {
		t.Error("expected false on error")
	}
	if got := err.Error(); !strings.HasPrefix(got, "failed to check ready tasks for epic") {
		t.Errorf("error = %q, want prefix 'failed to check ready tasks for epic'", got)
	}
}

func TestDefaultEpicHasReadyTasks_PassesCorrectOpts(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		if opts.ParentID != "epic-xyz" {
			t.Errorf("ParentID = %q, want epic-xyz", opts.ParentID)
		}
		if opts.Limit != 1 {
			t.Errorf("Limit = %d, want 1", opts.Limit)
		}
		return nil, nil
	}
	setDefaultIssueBackend(mock)

	defaultEpicHasReadyTasks("epic-xyz")
	if mock.CallCount("Ready") != 1 {
		t.Errorf("expected 1 Ready call, got %d", mock.CallCount("Ready"))
	}
}
