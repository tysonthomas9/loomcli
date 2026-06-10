package workflows

// In-process E2E for the Phase 1 acceptance criteria
// (docs/design/dynamic-workflow-runner.md). The real EpicReconciler,
// RunLifecycle, and capture pipeline run against platform.MemStore
// (which mirrors fleet-db's verified admission/fencing semantics) and
// a scripted execution plane that plays the TS epic-runner agent's
// role: re-derive the frontier, start TaskRuns idempotently, close the
// epic through the ActionLedger.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
	"github.com/tysonthomas9/loomcli/internal/workflows/execplane/fake"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

// epicWorld simulates the issue store side of fleet-db: child tasks
// with statuses and the epic's closed flag. The scripted agent reads
// it the way the TS SDK reads fleet-db's issues API.
type epicWorld struct {
	mu     sync.Mutex
	epicID string
	tasks  map[string]string // task id → "open" | "closed"
	closed bool
}

func (w *epicWorld) readyTasks() []string {
	var out []string
	for id, status := range w.tasks {
		if status == "open" {
			out = append(out, id)
		}
	}
	return out
}

func (w *epicWorld) allClosed() bool {
	for _, status := range w.tasks {
		if status != "closed" {
			return false
		}
	}
	return true
}

// closeTask closes a child task and publishes the issue mutation, like
// fleet-db does when a coding agent finishes the issue.
func (w *epicWorld) closeTask(m *platform.MemStore, taskID string) {
	w.mu.Lock()
	w.tasks[taskID] = "closed"
	w.mu.Unlock()
	m.AppendEvent(platform.MutationEvent{Action: "issue.close", EntityType: "issue", EntityID: taskID})
}

// agentScript is the deterministic stand-in for the TS epic-runner
// agent: one wake = re-derive frontier → start TaskRuns (idempotent)
// → close epic via ledger when exhausted.
func agentScript(t *testing.T, m *platform.MemStore, world *epicWorld) fake.Script {
	t.Helper()
	return func(inv fake.Invocation) []execplane.Event {
		ctx := context.Background()
		var msg struct {
			EpicID string `json:"epic_id"`
			RunID  string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(inv.Request.Message), &msg); err != nil {
			t.Errorf("bad wake message: %v", err)
		}
		world.mu.Lock()
		defer world.mu.Unlock()
		if ready := world.readyTasks(); len(ready) > 0 {
			for _, taskID := range ready {
				_, err := m.TaskRuns().Create(ctx, ws, platform.TaskRunCreate{
					TaskRunID: "tr-" + taskID, DriverRunID: msg.RunID, TaskID: taskID,
				})
				if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
					t.Errorf("start task %s: %v", taskID, err)
				}
			}
		} else if world.allClosed() && !world.closed {
			entry, err := m.ActionLedger().Create(ctx, ws, platform.LedgerCreate{
				IdempotencyKey: "close-epic:" + msg.EpicID, ActionType: "update_status", TargetRef: msg.EpicID,
			})
			if err != nil {
				t.Errorf("ledger create: %v", err)
			} else if entry.Status == platform.LedgerPending {
				world.closed = true
				if _, err := m.ActionLedger().Complete(ctx, ws, entry.ActionID, platform.LedgerApplied); err != nil {
					t.Errorf("ledger complete: %v", err)
				}
			}
		}
		return []execplane.Event{
			{Type: "text_delta", Data: json.RawMessage(`{"type":"text_delta","text":"reconciled"}`)},
			{Type: execplane.EventIdle, Data: json.RawMessage(`{"type":"idle"}`)},
		}
	}
}

func resolveVia(world *epicWorld) func(context.Context, string) (string, bool) {
	return func(_ context.Context, issueID string) (string, bool) {
		world.mu.Lock()
		defer world.mu.Unlock()
		if _, ok := world.tasks[issueID]; ok {
			return world.epicID, true
		}
		return "", false
	}
}

// waitForDriver blocks until the reconciler stamps its dev version.
func waitForDriver(t *testing.T, m *platform.MemStore) *platform.Driver {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, err := m.Drivers().Get(context.Background(), ws, "epic-runner")
		if err == nil && d.ActiveVersionID != "" {
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("driver dev version never stamped")
	return nil
}

func taskRunCount(t *testing.T, m *platform.MemStore) int {
	t.Helper()
	trs, err := m.TaskRuns().List(context.Background(), ws, platform.TaskRunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return len(trs)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestE2E_EpicRunner_ReconcilerAdvancesAndClosesEpic drives an epic
// from two open tasks to closed: run requested → TaskRuns started →
// tasks close (issue events wake the reconciler) → epic closed via an
// effectively-once ledger action.
func TestE2E_EpicRunner_ReconcilerAdvancesAndClosesEpic(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	world := &epicWorld{epicID: "EPIC-E2E", tasks: map[string]string{"T1": "open", "T2": "open"}}
	plane := fake.New(agentScript(t, m, world))
	cfg := testConfig(m, plane)
	cfg.ResolveEpic = resolveVia(world)
	r, err := NewEpicReconciler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)
	d := waitForDriver(t, m)

	// Admission (the `loom workflow run` path).
	if _, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-e2e-1", DriverID: d.DriverID, DriverVersionID: d.ActiveVersionID,
		EpicID: world.epicID, SourceKind: "cli",
	}); err != nil {
		t.Fatal(err)
	}

	// Wake 1 starts a TaskRun per ready child.
	waitFor(t, "both task runs", func() bool { return taskRunCount(t, m) == 2 })

	// Children complete (the existing agent supervisor's work) — each
	// close wakes the reconciler; after the last, the epic closes.
	world.closeTask(m, "T1")
	world.closeTask(m, "T2")
	waitFor(t, "epic closed", func() bool {
		world.mu.Lock()
		defer world.mu.Unlock()
		return world.closed
	})

	// Every side effect went through idempotent records: still exactly
	// one TaskRun per child, and the closing run completed.
	if n := taskRunCount(t, m); n != 2 {
		t.Fatalf("task runs: %d, want 2", n)
	}
	waitFor(t, "all runs terminal", func() bool {
		runs, _ := m.DriverRuns().List(context.Background(), ws, platform.DriverRunFilter{EpicID: world.epicID})
		for _, run := range runs {
			if !run.Status.Terminal() {
				return false
			}
			if run.Status != platform.DriverRunCompleted {
				t.Fatalf("run %s: %s (%s)", run.RunID, run.Status, run.ErrorClass)
			}
		}
		return len(runs) > 0
	})
}

// TestE2E_EpicRunner_CrashResumeWithoutDuplicates simulates loom dying
// mid-wake (a claimed running run + one TaskRun already started, then
// the process is gone). A fresh reconciler must fail the orphan with a
// clear error class, re-wake the epic, and complete it WITHOUT
// duplicate TaskRuns or duplicate ledger applications.
func TestE2E_EpicRunner_CrashResumeWithoutDuplicates(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	world := &epicWorld{epicID: "EPIC-CRASH", tasks: map[string]string{"T1": "open", "T2": "open"}}
	ctx := context.Background()

	// State left behind by the crashed previous loom process.
	if _, err := m.Drivers().Create(ctx, ws, platform.Driver{DriverID: "epic-runner", Name: "epic-runner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Drivers().CreateVersion(ctx, ws, "epic-runner", platform.DriverVersion{
		VersionID: "ver-crash", Version: 1, SourceDigest: "sha256:dev", BundleDigest: "sha256:dev",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DriverRuns().Create(ctx, ws, platform.DriverRunCreate{
		RunID: "run-crashed", DriverID: "epic-runner", DriverVersionID: "ver-crash",
		EpicID: world.epicID, SourceKind: "cli",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DriverRuns().Claim(ctx, ws, "run-crashed", "node-test", "lease-dead"); err != nil {
		t.Fatal(err)
	}
	// The crashed wake had already started T1.
	if _, err := m.TaskRuns().Create(ctx, ws, platform.TaskRunCreate{
		TaskRunID: "tr-T1", DriverRunID: "run-crashed", TaskID: "T1",
	}); err != nil {
		t.Fatal(err)
	}

	// Restart: same stable NodeID.
	plane := fake.New(agentScript(t, m, world))
	cfg := testConfig(m, plane)
	cfg.ResolveEpic = resolveVia(world)
	r, err := NewEpicReconciler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)

	// Orphan recorded with a clear error class.
	waitFor(t, "orphan recovered", func() bool {
		run, err := m.DriverRuns().Get(ctx, ws, "run-crashed")
		return err == nil && run.Status == platform.DriverRunFailed && run.ErrorClass == ErrorClassLoomRestart
	})
	// Re-wake completes the frontier: T2 started, T1 NOT duplicated.
	waitFor(t, "both task runs", func() bool { return taskRunCount(t, m) == 2 })

	world.closeTask(m, "T1")
	world.closeTask(m, "T2")
	waitFor(t, "epic closed", func() bool {
		world.mu.Lock()
		defer world.mu.Unlock()
		return world.closed
	})
	if n := taskRunCount(t, m); n != 2 {
		t.Fatalf("task runs after resume: %d, want 2 (no duplicates)", n)
	}
}

// TestE2E_EpicRunner_OneActivePerEpicRejection: while a wake is
// active, a concurrent run request for the same epic is absorbed by
// fleet-db admission — the caller gets the existing active run and no
// second run is created.
func TestE2E_EpicRunner_OneActivePerEpicRejection(t *testing.T) {
	t.Parallel()
	m := platform.NewMemStore()
	plane := newGatedPlane()
	cfg := testConfig(m, plane)
	r, err := NewEpicReconciler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	startReconciler(t, r)
	d := waitForDriver(t, m)

	const epic = "EPIC-CONC"
	first, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-first", DriverID: d.DriverID, DriverVersionID: d.ActiveVersionID,
		EpicID: epic, SourceKind: "cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-plane.invoked: // the wake is live and holding
	case <-time.After(5 * time.Second):
		t.Fatal("first run never invoked")
	}

	// Concurrent second request (the UI's Run Epic button).
	second, err := m.DriverRuns().Create(context.Background(), ws, platform.DriverRunCreate{
		RunID: "run-second", DriverID: d.DriverID, DriverVersionID: d.ActiveVersionID,
		EpicID: epic, SourceKind: "ui",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.RunID != first.RunID {
		t.Fatalf("admission created a second active run: %+v", second)
	}
	runs, err := m.DriverRuns().List(context.Background(), ws, platform.DriverRunFilter{EpicID: epic})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs for epic: %d, want 1", len(runs))
	}

	close(plane.release)
	waitFor(t, "held run terminal", func() bool {
		run, err := m.DriverRuns().Get(context.Background(), ws, first.RunID)
		return err == nil && run.Status.Terminal()
	})
}
