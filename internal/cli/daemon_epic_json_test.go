package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestDefaultQueryOpenEpics_ReturnsTrackerResults(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ListResult = []BdIssue{
		{ID: "epic-1", Priority: 0},
		{ID: "epic-2", Priority: 2},
	}
	setDefaultTracker(mock)

	epics, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(epics) != 2 {
		t.Fatalf("expected 2 epics, got %d", len(epics))
	}
	if epics[0].ID != "epic-1" || epics[0].Priority != 0 {
		t.Errorf("epics[0] = %+v, want {ID:epic-1, Priority:0}", epics[0])
	}
	if epics[1].ID != "epic-2" || epics[1].Priority != 2 {
		t.Errorf("epics[1] = %+v, want {ID:epic-2, Priority:2}", epics[1])
	}
}

func TestDefaultQueryOpenEpics_EmptyResults(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ListResult = []BdIssue{}
	setDefaultTracker(mock)

	epics, err := defaultQueryOpenEpics()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(epics) != 0 {
		t.Errorf("expected empty slice, got %d epics", len(epics))
	}
}

func TestDefaultQueryOpenEpics_TrackerError(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ListErr = fmt.Errorf("connection refused")
	setDefaultTracker(mock)

	epics, err := defaultQueryOpenEpics()
	if err == nil {
		t.Fatal("expected error from tracker")
	}
	if epics != nil {
		t.Errorf("expected nil slice, got %v", epics)
	}
	if got := err.Error(); !strings.HasPrefix(got, "failed to list open epics:") {
		t.Errorf("error = %q, want prefix 'failed to list open epics:'", got)
	}
}

func TestDefaultQueryOpenEpics_PassesCorrectOpts(t *testing.T) {
	resetDefaultTracker()
	t.Cleanup(resetDefaultTracker)

	mock := NewMockTracker()
	mock.ListFunc = func(_ context.Context, opts ListOpts) ([]BdIssue, error) {
		if opts.Type != "epic" {
			t.Errorf("Type = %q, want epic", opts.Type)
		}
		if opts.Status != "open" {
			t.Errorf("Status = %q, want open", opts.Status)
		}
		if opts.Limit != 0 {
			t.Errorf("Limit = %d, want 0", opts.Limit)
		}
		return nil, nil
	}
	setDefaultTracker(mock)

	defaultQueryOpenEpics()
	if mock.CallCount("List") != 1 {
		t.Errorf("expected 1 List call, got %d", mock.CallCount("List"))
	}
}

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
