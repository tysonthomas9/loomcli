package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// setupParityBackends creates both a bdBackend (via MockBDRunner) and a
// fleetDBBackend (via mockFleetService) seeded with identical fixture data.
// The bd side gets pre-hydrated JSON for commands that trigger dep hydration
// on the fleet side (ready, show), ensuring DeepEqual after processing.
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

	// Configure MockBDRunner to return pre-hydrated JSON keyed on command.
	mockRunner := &MockBDRunner{
		RunFunc: func(_ string, args ...string) CommandResult {
			if len(args) == 0 {
				return CommandResult{Err: fmt.Errorf("no args")}
			}
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
			b, err := json.Marshal(data)
			if err != nil {
				return CommandResult{Err: err}
			}
			return CommandResult{Stdout: string(b)}
		},
	}

	// Configure mockFleetService — fleet returns un-hydrated issues;
	// Ready() and GetIssue() hydrate via GetDependencies.
	issueCopy := fixtureIssueNoDeps // copy: GetIssue hydrates deps on the pointed-to struct
	mockSvc := &mockFleetService{
		readyIssues:   []BdIssue{fixtureIssueNoDeps},
		listIssues:    []BdIssue{fixtureIssueNoDeps},
		blockedIssues: []BdIssue{fixtureIssueNoDeps},
		deps:          fixtureDeps,
		stats:         fixtureStats,
		issue:         &issueCopy,
	}

	bd := newBDBackend(mockRunner, "/test")
	fleet := newFleetDBBackend(mockSvc, newFleetTestLogger())
	return bd, fleet
}

func TestBackendParity(t *testing.T) {
	ctx := context.Background()

	t.Run("Ready", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, bdErr := bd.Ready(ctx, ReadyOpts{})
		fleetResult, fleetErr := fleet.Ready(ctx, ReadyOpts{})
		if bdErr != nil {
			t.Fatalf("bd error: %v", bdErr)
		}
		if fleetErr != nil {
			t.Fatalf("fleet error: %v", fleetErr)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Errorf("Ready mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
		if bdResult[0].Dependencies == nil {
			t.Error("bd Ready: Dependencies should not be nil")
		}
		if bdResult[0].Labels == nil {
			t.Error("bd Ready: Labels should not be nil")
		}
	})

	t.Run("List", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, bdErr := bd.List(ctx, ListOpts{Status: "open"})
		fleetResult, fleetErr := fleet.List(ctx, ListOpts{Status: "open"})
		if bdErr != nil {
			t.Fatalf("bd error: %v", bdErr)
		}
		if fleetErr != nil {
			t.Fatalf("fleet error: %v", fleetErr)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Errorf("List mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
		if bdResult[0].Dependencies == nil {
			t.Error("bd List: Dependencies should be empty slice, not nil")
		}
		if fleetResult[0].Dependencies == nil {
			t.Error("fleet List: Dependencies should be empty slice, not nil")
		}
	})

	t.Run("Blocked", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, bdErr := bd.Blocked(ctx)
		fleetResult, fleetErr := fleet.Blocked(ctx)
		if bdErr != nil {
			t.Fatalf("bd error: %v", bdErr)
		}
		if fleetErr != nil {
			t.Fatalf("fleet error: %v", fleetErr)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Errorf("Blocked mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, bdErr := bd.Stats(ctx)
		fleetResult, fleetErr := fleet.Stats(ctx)
		if bdErr != nil {
			t.Fatalf("bd error: %v", bdErr)
		}
		if fleetErr != nil {
			t.Fatalf("fleet error: %v", fleetErr)
		}
		if !reflect.DeepEqual(bdResult, fleetResult) {
			t.Errorf("Stats mismatch:\n  bd:    %+v\n  fleet: %+v", bdResult, fleetResult)
		}
		// Verify all 8 summary fields are non-zero to catch zero-value false positives.
		s := bdResult.Summary
		if s.TotalIssues != 10 || s.OpenIssues != 3 || s.ClosedIssues != 4 ||
			s.InProgressIssues != 1 || s.BlockedIssues != 1 || s.PinnedIssues != 1 ||
			s.DeferredIssues != 2 || s.TombstoneIssues != 3 {
			t.Errorf("Stats summary fields not fully populated: %+v", s)
		}
	})

	t.Run("GetIssue", func(t *testing.T) {
		bd, fleet := setupParityBackends(t)
		bdResult, bdErr := bd.GetIssue(ctx, "test-1")
		fleetResult, fleetErr := fleet.GetIssue(ctx, "test-1")
		if bdErr != nil {
			t.Fatalf("bd error: %v", bdErr)
		}
		if fleetErr != nil {
			t.Fatalf("fleet error: %v", fleetErr)
		}
		if !reflect.DeepEqual(*bdResult, *fleetResult) {
			t.Errorf("GetIssue mismatch:\n  bd:    %+v\n  fleet: %+v", *bdResult, *fleetResult)
		}
		if len(bdResult.Dependencies) != 1 {
			t.Errorf("bd GetIssue: expected 1 dep, got %d", len(bdResult.Dependencies))
		}
	})

	t.Run("EmptyResults", func(t *testing.T) {
		emptyRunner := &MockBDRunner{
			RunFunc: func(_ string, args ...string) CommandResult {
				if len(args) == 0 {
					return CommandResult{Err: fmt.Errorf("no args")}
				}
				var data interface{}
				switch args[0] {
				case "ready", "list", "blocked":
					data = []BdIssue{}
				case "stats":
					data = BdStats{}
				default:
					return CommandResult{Err: fmt.Errorf("unexpected: %s", args[0])}
				}
				b, _ := json.Marshal(data)
				return CommandResult{Stdout: string(b)}
			},
		}
		emptySvc := &mockFleetService{
			readyIssues:   []BdIssue{},
			listIssues:    []BdIssue{},
			blockedIssues: []BdIssue{},
			deps:          []Dependency{},
		}

		bd := newBDBackend(emptyRunner, "/test")
		fleet := newFleetDBBackend(emptySvc, newFleetTestLogger())

		bdReady, err := bd.Ready(ctx, ReadyOpts{})
		if err != nil {
			t.Fatalf("bd empty Ready error: %v", err)
		}
		fleetReady, err := fleet.Ready(ctx, ReadyOpts{})
		if err != nil {
			t.Fatalf("fleet empty Ready error: %v", err)
		}
		if !reflect.DeepEqual(bdReady, fleetReady) {
			t.Errorf("Empty Ready mismatch:\n  bd:    %+v\n  fleet: %+v", bdReady, fleetReady)
		}

		bdList, err := bd.List(ctx, ListOpts{})
		if err != nil {
			t.Fatalf("bd empty List error: %v", err)
		}
		fleetList, err := fleet.List(ctx, ListOpts{})
		if err != nil {
			t.Fatalf("fleet empty List error: %v", err)
		}
		if !reflect.DeepEqual(bdList, fleetList) {
			t.Errorf("Empty List mismatch:\n  bd:    %+v\n  fleet: %+v", bdList, fleetList)
		}

		bdBlocked, err := bd.Blocked(ctx)
		if err != nil {
			t.Fatalf("bd empty Blocked error: %v", err)
		}
		fleetBlocked, err := fleet.Blocked(ctx)
		if err != nil {
			t.Fatalf("fleet empty Blocked error: %v", err)
		}
		if !reflect.DeepEqual(bdBlocked, fleetBlocked) {
			t.Errorf("Empty Blocked mismatch:\n  bd:    %+v\n  fleet: %+v", bdBlocked, fleetBlocked)
		}

		bdStats, err := bd.Stats(ctx)
		if err != nil {
			t.Fatalf("bd empty Stats error: %v", err)
		}
		fleetStats, err := fleet.Stats(ctx)
		if err != nil {
			t.Fatalf("fleet empty Stats error: %v", err)
		}
		if !reflect.DeepEqual(bdStats, fleetStats) {
			t.Errorf("Empty Stats mismatch:\n  bd:    %+v\n  fleet: %+v", bdStats, fleetStats)
		}

		// Verify returned slices are non-nil (empty []BdIssue{} not nil).
		if bdReady == nil {
			t.Error("bd empty Ready returned nil, want empty slice")
		}
		if fleetReady == nil {
			t.Error("fleet empty Ready returned nil, want empty slice")
		}
	})

	t.Run("Errors", func(t *testing.T) {
		errRunner := &MockBDRunner{
			RunFunc: func(_ string, _ ...string) CommandResult {
				return CommandResult{Err: fmt.Errorf("bd failure")}
			},
		}
		errSvc := &mockFleetService{
			readyErr:   fmt.Errorf("service failure"),
			listErr:    fmt.Errorf("service failure"),
			blockedErr: fmt.Errorf("service failure"),
			statsErr:   fmt.Errorf("service failure"),
			issueErr:   fmt.Errorf("service failure"),
		}

		bd := newBDBackend(errRunner, "/test")
		fleet := newFleetDBBackend(errSvc, newFleetTestLogger())

		_, bdErr := bd.Ready(ctx, ReadyOpts{})
		_, fleetErr := fleet.Ready(ctx, ReadyOpts{})
		if bdErr == nil || fleetErr == nil {
			t.Error("Ready: both backends should return errors")
		}

		_, bdErr = bd.List(ctx, ListOpts{})
		_, fleetErr = fleet.List(ctx, ListOpts{})
		if bdErr == nil || fleetErr == nil {
			t.Error("List: both backends should return errors")
		}

		_, bdErr = bd.Blocked(ctx)
		_, fleetErr = fleet.Blocked(ctx)
		if bdErr == nil || fleetErr == nil {
			t.Error("Blocked: both backends should return errors")
		}

		_, bdErr = bd.Stats(ctx)
		_, fleetErr = fleet.Stats(ctx)
		if bdErr == nil || fleetErr == nil {
			t.Error("Stats: both backends should return errors")
		}

		_, bdErr = bd.GetIssue(ctx, "x")
		_, fleetErr = fleet.GetIssue(ctx, "x")
		if bdErr == nil || fleetErr == nil {
			t.Error("GetIssue: both backends should return errors")
		}
	})
}
