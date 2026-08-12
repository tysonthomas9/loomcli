package memstore

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDriverRunTerminalTransitionsEnqueueDurableOutcomes(t *testing.T) {
	runs := newDriverRunStore(nil, nil)
	create := func(runID string) {
		t.Helper()
		if _, err := runs.Create(t.Context(), store.DriverRunCreate{
			WorkspaceKey: "WS", RunID: runID, DriverID: "driver", DriverVersionID: "v1",
		}); err != nil {
			t.Fatalf("Create(%s): %v", runID, err)
		}
	}

	create("finished")
	claimed, err := runs.Claim(t.Context(), "WS", "finished", "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.Finish(t.Context(), "WS", "finished", store.DriverRunFinish{
		NodeID: "node", LeaseID: "lease", FencingToken: claimed.FencingToken,
		Status: domain.DriverRunCompleted, Summary: "done",
	}); err != nil {
		t.Fatal(err)
	}

	create("cascade")
	if _, err := runs.CancelQueuedRun(t.Context(), "WS", "cascade", "parent terminal", "parent_run_terminal"); err != nil {
		t.Fatal(err)
	}

	create("replace")
	if !runs.cancelQueuedForSupersede("WS", "replace", "newer event") {
		t.Fatal("replace cancellation did not win")
	}

	create("stale")
	if _, err := runs.Claim(t.Context(), "WS", "stale", "node", "stale-lease"); err != nil {
		t.Fatal(err)
	}
	runs.mu.Lock()
	runs.items["WS"]["stale"].LastHeartbeat = time.Now().UTC().Add(-time.Hour)
	runs.mu.Unlock()
	if result, err := runs.RecoverStale(t.Context(), "WS", store.StaleDriverRunRecovery{MaxAgeSeconds: 60}); err != nil || result.Recovered != 1 {
		t.Fatalf("RecoverStale = %+v, %v", result, err)
	}

	now := time.Now().UTC().Add(time.Second)
	outcomes, err := runs.ClaimDriverRunOutcomes(t.Context(), store.DriverRunOutcomeClaim{
		WorkspaceKey: "WS", ClaimID: "claim-1", Before: now, ClaimUntil: now.Add(time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 4 {
		t.Fatalf("outcomes = %+v, want four terminal transitions", outcomes)
	}
	status := make(map[string]domain.DriverRunStatus, len(outcomes))
	for _, outcome := range outcomes {
		status[outcome.RunID] = outcome.Status
		if outcome.Attempt != 1 || outcome.OccurredAt.IsZero() {
			t.Fatalf("outcome = %+v", outcome)
		}
	}
	if status["finished"] != domain.DriverRunCompleted ||
		status["cascade"] != domain.DriverRunCancelled ||
		status["replace"] != domain.DriverRunCancelled ||
		status["stale"] != domain.DriverRunFailed {
		t.Fatalf("statuses = %+v", status)
	}
}
