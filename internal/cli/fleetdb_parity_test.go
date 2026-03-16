package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// setupParityBackends constructs both a bdBackend and fleetDBBackend from shared
// fixture data, so parity tests compare identical logical inputs through both paths.
func setupParityBackends(t *testing.T) (IssueTracker, IssueTracker) {
	t.Helper()

	fixtureDeps := []Dependency{{
		IssueID:     "test-1",
		DependsOnID: "dep-1",
		Type:        "blocks",
		CreatedAt:   "2026-01-01T00:00:00Z",
		CreatedBy:   "test",
	}}

	fixtureIssueWithDeps := BdIssue{
		ID:           "test-1",
		Title:        "Test issue",
		Status:       "open",
		Priority:     2,
		IssueType:    "task",
		Design:       "some design text",
		Assignee:     "alice",
		Labels:       []string{"phase-1"},
		Dependencies: fixtureDeps,
	}

	fixtureIssueNoDeps := BdIssue{
		ID:           "test-1",
		Title:        "Test issue",
		Status:       "open",
		Priority:     2,
		IssueType:    "task",
		Design:       "some design text",
		Assignee:     "alice",
		Labels:       []string{"phase-1"},
		Dependencies: []Dependency{},
	}

	fixtureStats := BdStats{}
	fixtureStats.Summary.TotalIssues = 10
	fixtureStats.Summary.OpenIssues = 3
	fixtureStats.Summary.ClosedIssues = 4
	fixtureStats.Summary.InProgressIssues = 1
	fixtureStats.Summary.BlockedIssues = 1
	fixtureStats.Summary.DeferredIssues = 2
	fixtureStats.Summary.TombstoneIssues = 3
	fixtureStats.Summary.PinnedIssues = 1

	// Configure MockBDRunner to return fixture JSON based on subcommand.
	mockRunner := &MockBDRunner{
		RunFunc: func(_ string, args ...string) CommandResult {
			var data interface{}
			switch args[0] {
			case "ready":
				data = []BdIssue{fixtureIssueWithDeps}
			case "list", "blocked":
				data = []BdIssue{fixtureIssueNoDeps}
			case "stats":
				data = fixtureStats
			case "show":
				data = []BdIssue{fixtureIssueWithDeps}
			default:
				return CommandResult{Err: fmt.Errorf("unexpected command: %s", args[0])}
			}
			jsonBytes, err := json.Marshal(data)
			if err != nil {
				return CommandResult{Err: err}
			}
			return CommandResult{Stdout: string(jsonBytes)}
		},
	}

	// Configure mockFleetService with matching fixture data.
	issueCopy := fixtureIssueNoDeps // struct copy so GetIssue mutation doesn't affect fixture
	mockSvc := &mockFleetService{
		readyIssues:   []BdIssue{fixtureIssueNoDeps},
		listIssues:    []BdIssue{fixtureIssueNoDeps},
		blockedIssues: []BdIssue{fixtureIssueNoDeps},
		deps:          fixtureDeps,
		stats:         fixtureStats,
		issue:         &issueCopy,
	}

	bd := newBDBackend(mockRunner, "/test")
	fleet := newTestFleetDBBackend(mockSvc)
	return bd, fleet
}

func TestBackendParity(t *testing.T) {
	ctx := context.Background()

	t.Run("Ready", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, err := bd.Ready(ctx, ReadyOpts{})
		if err != nil {
			t.Fatalf("bd.Ready: %v", err)
		}
		fleetResult, err := fleet.Ready(ctx, ReadyOpts{})
		if err != nil {
			t.Fatalf("fleet.Ready: %v", err)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Fatalf("Ready mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
		if len(bdResult) == 0 {
			t.Fatal("expected non-empty Ready result")
		}
		if bdResult[0].Dependencies == nil {
			t.Error("bd Dependencies should not be nil")
		}
		if fleetResult[0].Dependencies == nil {
			t.Error("fleet Dependencies should not be nil")
		}
		if len(bdResult[0].Dependencies) != 1 {
			t.Errorf("bd Ready: expected 1 dep, got %d", len(bdResult[0].Dependencies))
		}
		if bdResult[0].Labels == nil {
			t.Error("bd Labels should not be nil")
		}
		if fleetResult[0].Labels == nil {
			t.Error("fleet Labels should not be nil")
		}
	})

	t.Run("List", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, err := bd.List(ctx, ListOpts{Status: "open"})
		if err != nil {
			t.Fatalf("bd.List: %v", err)
		}
		fleetResult, err := fleet.List(ctx, ListOpts{Status: "open"})
		if err != nil {
			t.Fatalf("fleet.List: %v", err)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Errorf("List mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
		if len(bdResult) > 0 && bdResult[0].Dependencies == nil {
			t.Error("bd List: Dependencies should be [] not nil")
		}
		if len(fleetResult) > 0 && fleetResult[0].Dependencies == nil {
			t.Error("fleet List: Dependencies should be [] not nil")
		}
	})

	t.Run("Blocked", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, err := bd.Blocked(ctx)
		if err != nil {
			t.Fatalf("bd.Blocked: %v", err)
		}
		fleetResult, err := fleet.Blocked(ctx)
		if err != nil {
			t.Fatalf("fleet.Blocked: %v", err)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Errorf("Blocked mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, err := bd.Stats(ctx)
		if err != nil {
			t.Fatalf("bd.Stats: %v", err)
		}
		fleetResult, err := fleet.Stats(ctx)
		if err != nil {
			t.Fatalf("fleet.Stats: %v", err)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Errorf("Stats mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
	})

	t.Run("GetIssue", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, err := bd.GetIssue(ctx, "test-1")
		if err != nil {
			t.Fatalf("bd.GetIssue: %v", err)
		}
		fleetResult, err := fleet.GetIssue(ctx, "test-1")
		if err != nil {
			t.Fatalf("fleet.GetIssue: %v", err)
		}
		if bdResult == nil || fleetResult == nil {
			t.Fatalf("GetIssue returned nil without error: bd=%v fleet=%v", bdResult, fleetResult)
		}
		if !reflect.DeepEqual(*bdResult, *fleetResult) {
			t.Errorf("GetIssue mismatch:\n  bd:    %+v\n  fleet: %+v", *bdResult, *fleetResult)
		}
		if len(bdResult.Dependencies) != 1 {
			t.Errorf("bd GetIssue: expected 1 dep, got %d", len(bdResult.Dependencies))
		}
		if !reflect.DeepEqual(bdResult.Dependencies, fleetResult.Dependencies) {
			t.Error("GetIssue: Dependencies not identical")
		}
	})

	t.Run("EmptyResults", func(t *testing.T) {
		emptyStats := BdStats{}

		emptyRunner := &MockBDRunner{
			RunFunc: func(_ string, args ...string) CommandResult {
				switch args[0] {
				case "ready", "list", "blocked":
					return CommandResult{Stdout: "[]"}
				case "stats":
					jsonBytes, _ := json.Marshal(emptyStats)
					return CommandResult{Stdout: string(jsonBytes)}
				default:
					return CommandResult{Err: fmt.Errorf("unexpected: %s", args[0])}
				}
			},
		}

		emptySvc := &mockFleetService{
			readyIssues:   []BdIssue{},
			listIssues:    []BdIssue{},
			blockedIssues: []BdIssue{},
			deps:          []Dependency{},
			stats:         emptyStats,
		}

		bd := newBDBackend(emptyRunner, "/test")
		fleet := newTestFleetDBBackend(emptySvc)

		// Ready
		bdReady, err := bd.Ready(ctx, ReadyOpts{})
		if err != nil {
			t.Fatalf("bd.Ready: %v", err)
		}
		fleetReady, err := fleet.Ready(ctx, ReadyOpts{})
		if err != nil {
			t.Fatalf("fleet.Ready: %v", err)
		}
		if !reflect.DeepEqual(bdReady, fleetReady) {
			t.Errorf("Empty Ready mismatch:\n  bd:    %+v\n  fleet: %+v", bdReady, fleetReady)
		}
		if bdReady == nil {
			t.Error("bd empty Ready should be non-nil []BdIssue{}")
		}
		if fleetReady == nil {
			t.Error("fleet empty Ready should be non-nil []BdIssue{}")
		}

		// List
		bdList, err := bd.List(ctx, ListOpts{Status: "open"})
		if err != nil {
			t.Fatalf("bd.List: %v", err)
		}
		fleetList, err := fleet.List(ctx, ListOpts{Status: "open"})
		if err != nil {
			t.Fatalf("fleet.List: %v", err)
		}
		if !reflect.DeepEqual(bdList, fleetList) {
			t.Errorf("Empty List mismatch:\n  bd:    %+v\n  fleet: %+v", bdList, fleetList)
		}
		if bdList == nil {
			t.Error("bd empty List should be non-nil")
		}
		if fleetList == nil {
			t.Error("fleet empty List should be non-nil")
		}

		// Blocked
		bdBlocked, err := bd.Blocked(ctx)
		if err != nil {
			t.Fatalf("bd.Blocked: %v", err)
		}
		fleetBlocked, err := fleet.Blocked(ctx)
		if err != nil {
			t.Fatalf("fleet.Blocked: %v", err)
		}
		if !reflect.DeepEqual(bdBlocked, fleetBlocked) {
			t.Errorf("Empty Blocked mismatch:\n  bd:    %+v\n  fleet: %+v", bdBlocked, fleetBlocked)
		}
		if bdBlocked == nil {
			t.Error("bd empty Blocked should be non-nil")
		}
		if fleetBlocked == nil {
			t.Error("fleet empty Blocked should be non-nil")
		}

		// Stats
		bdStats, err := bd.Stats(ctx)
		if err != nil {
			t.Fatalf("bd.Stats: %v", err)
		}
		fleetStats, err := fleet.Stats(ctx)
		if err != nil {
			t.Fatalf("fleet.Stats: %v", err)
		}
		if !reflect.DeepEqual(bdStats, fleetStats) {
			t.Errorf("Empty Stats mismatch:\n  bd:    %+v\n  fleet: %+v", bdStats, fleetStats)
		}
	})

	t.Run("Errors", func(t *testing.T) {
		testErr := fmt.Errorf("service unavailable")

		errRunner := &MockBDRunner{
			RunFunc: func(_ string, _ ...string) CommandResult {
				return CommandResult{Err: testErr}
			},
		}
		errSvc := &mockFleetService{
			readyErr:   testErr,
			listErr:    testErr,
			blockedErr: testErr,
			statsErr:   testErr,
			issueErr:   testErr,
		}

		bd := newBDBackend(errRunner, "/test")
		fleet := newTestFleetDBBackend(errSvc)

		// Both backends must return non-nil errors.
		// Do NOT compare error strings — prefixes differ by design.
		if _, err := bd.Ready(ctx, ReadyOpts{}); err == nil {
			t.Error("bd.Ready: expected error")
		}
		if _, err := fleet.Ready(ctx, ReadyOpts{}); err == nil {
			t.Error("fleet.Ready: expected error")
		}
		if _, err := bd.List(ctx, ListOpts{}); err == nil {
			t.Error("bd.List: expected error")
		}
		if _, err := fleet.List(ctx, ListOpts{}); err == nil {
			t.Error("fleet.List: expected error")
		}
		if _, err := bd.Blocked(ctx); err == nil {
			t.Error("bd.Blocked: expected error")
		}
		if _, err := fleet.Blocked(ctx); err == nil {
			t.Error("fleet.Blocked: expected error")
		}
		if _, err := bd.Stats(ctx); err == nil {
			t.Error("bd.Stats: expected error")
		}
		if _, err := fleet.Stats(ctx); err == nil {
			t.Error("fleet.Stats: expected error")
		}
		if _, err := bd.GetIssue(ctx, "test-1"); err == nil {
			t.Error("bd.GetIssue: expected error")
		}
		if _, err := fleet.GetIssue(ctx, "test-1"); err == nil {
			t.Error("fleet.GetIssue: expected error")
		}
	})
}
