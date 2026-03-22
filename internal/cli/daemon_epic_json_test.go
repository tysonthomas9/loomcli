package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestDefaultEpicHasReadyTasks_HasTasks(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ReadyResult = []BdIssue{{ID: "task-1"}}
	setDefaultTracker(mock)

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Error("expected true, got false")
	}
}

func TestDefaultEpicHasReadyTasks_NoTasks(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ReadyResult = []BdIssue{}
	setDefaultTracker(mock)

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if has {
		t.Error("expected false, got true")
	}
}

func TestDefaultEpicHasReadyTasks_TrackerError(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ReadyErr = fmt.Errorf("timeout")
	setDefaultTracker(mock)

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
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		if opts.ParentID != "epic-xyz" {
			t.Errorf("ParentID = %q, want epic-xyz", opts.ParentID)
		}
		if opts.Limit != 1 {
			t.Errorf("Limit = %d, want 1", opts.Limit)
		}
		return nil, nil
	}
	setDefaultTracker(mock)

	defaultEpicHasReadyTasks("epic-xyz")
	if mock.CallCount("Ready") != 1 {
		t.Errorf("expected 1 Ready call, got %d", mock.CallCount("Ready"))
	}
}
