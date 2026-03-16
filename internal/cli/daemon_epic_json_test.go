package cli

import (
	"context"
	"fmt"
	"testing"
)

func TestDefaultQueryOpenEpics_CallsListWithCorrectOpts(t *testing.T) {
	var capturedOpts ListOpts
	mock := &MockIssueTracker{
		ListFunc: func(_ context.Context, opts ListOpts) ([]BdIssue, error) {
			capturedOpts = opts
			return []BdIssue{}, nil
		},
	}
	setDefaultTracker(mock)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	_, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.IssueType != "epic" {
		t.Errorf("IssueType = %q, want %q", capturedOpts.IssueType, "epic")
	}
	if capturedOpts.Status != "open" {
		t.Errorf("Status = %q, want %q", capturedOpts.Status, "open")
	}
	if capturedOpts.Limit != 0 {
		t.Errorf("Limit = %d, want 0", capturedOpts.Limit)
	}
}

func TestDefaultQueryOpenEpics_ConvertsBdIssuesToEpicInfo(t *testing.T) {
	mock := &MockIssueTracker{
		ListResult: []BdIssue{
			{ID: "epic-1", Priority: 0},
			{ID: "epic-2", Priority: 2},
		},
	}
	setDefaultTracker(mock)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	epics, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(epics) != 2 {
		t.Fatalf("expected 2 epics, got %d", len(epics))
	}
	if epics[0].ID != "epic-1" || epics[0].Priority != 0 {
		t.Errorf("epics[0] = %+v, want {ID:epic-1 Priority:0}", epics[0])
	}
	if epics[1].ID != "epic-2" || epics[1].Priority != 2 {
		t.Errorf("epics[1] = %+v, want {ID:epic-2 Priority:2}", epics[1])
	}
}

func TestDefaultQueryOpenEpics_ReturnsEmptySliceForNoResults(t *testing.T) {
	mock := &MockIssueTracker{
		ListResult: []BdIssue{},
	}
	setDefaultTracker(mock)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	epics, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(epics) != 0 {
		t.Errorf("expected empty slice, got %d epics", len(epics))
	}
}

func TestDefaultQueryOpenEpics_PropagatesTrackerError(t *testing.T) {
	mock := &MockIssueTracker{
		ListErr: fmt.Errorf("connection refused"),
	}
	setDefaultTracker(mock)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	epics, err := defaultQueryOpenEpics()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if epics != nil {
		t.Errorf("expected nil slice, got %v", epics)
	}
}

func TestDefaultEpicHasReadyTasks_CallsReadyWithCorrectOpts(t *testing.T) {
	var capturedOpts ReadyOpts
	mock := &MockIssueTracker{
		ReadyFunc: func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
			capturedOpts = opts
			return []BdIssue{}, nil
		},
	}
	setDefaultTracker(mock)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	_, err := defaultEpicHasReadyTasks("epic-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "epic-xyz" {
		t.Errorf("ParentID = %q, want %q", capturedOpts.ParentID, "epic-xyz")
	}
	if capturedOpts.Limit != 1 {
		t.Errorf("Limit = %d, want 1", capturedOpts.Limit)
	}
}

func TestDefaultEpicHasReadyTasks_ReturnsTrueWhenTasksExist(t *testing.T) {
	t.Run("has tasks", func(t *testing.T) {
		mock := &MockIssueTracker{
			ReadyResult: []BdIssue{{ID: "task-1"}},
		}
		setDefaultTracker(mock)
		t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

		has, err := defaultEpicHasReadyTasks("epic-xyz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !has {
			t.Error("expected true, got false")
		}
	})

	t.Run("no tasks", func(t *testing.T) {
		mock := &MockIssueTracker{
			ReadyResult: []BdIssue{},
		}
		setDefaultTracker(mock)
		t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

		has, err := defaultEpicHasReadyTasks("epic-xyz")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if has {
			t.Error("expected false, got true")
		}
	})
}

func TestDefaultEpicHasReadyTasks_PropagatesTrackerError(t *testing.T) {
	mock := &MockIssueTracker{
		ReadyErr: fmt.Errorf("timeout"),
	}
	setDefaultTracker(mock)
	t.Cleanup(func() { setDefaultTracker(defaultDeps.Tracker) })

	has, err := defaultEpicHasReadyTasks("epic-xyz")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if has {
		t.Error("expected false on error")
	}
}
