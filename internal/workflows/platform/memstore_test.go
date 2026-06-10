package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const ws = "test-ws"

func seedDriver(t *testing.T, m *MemStore) (driverID, versionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := m.Drivers().Create(ctx, ws, Driver{DriverID: "epic-runner", Name: "epic-runner"}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	v, err := m.Drivers().CreateVersion(ctx, ws, "epic-runner", DriverVersion{
		VersionID: "ver-1", Version: 1, SourceDigest: "sha256:dev", BundleDigest: "sha256:dev",
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	return "epic-runner", v.VersionID
}

func TestMemStore_DriverRunAdmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemStore()
	drv, ver := seedDriver(t, m)

	r1, err := m.DriverRuns().Create(ctx, ws, DriverRunCreate{
		RunID: "run-1", DriverID: drv, DriverVersionID: ver, EpicID: "EPIC-1", IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatalf("create run-1: %v", err)
	}
	if r1.RunID != "run-1" || r1.Status != DriverRunQueued {
		t.Fatalf("unexpected run: %+v", r1)
	}

	// Idempotency-key hit returns the existing run.
	r2, err := m.DriverRuns().Create(ctx, ws, DriverRunCreate{
		RunID: "run-2", DriverID: drv, DriverVersionID: ver, IdempotencyKey: "k1",
	})
	if err != nil || r2.RunID != "run-1" {
		t.Fatalf("idempotency dedupe: got %v run=%+v", err, r2)
	}

	// one_active_per_epic returns the existing active run.
	r3, err := m.DriverRuns().Create(ctx, ws, DriverRunCreate{
		RunID: "run-3", DriverID: drv, DriverVersionID: ver, EpicID: "EPIC-1", IdempotencyKey: "k3",
	})
	if err != nil || r3.RunID != "run-1" {
		t.Fatalf("one_active_per_epic dedupe: got %v run=%+v", err, r3)
	}

	// Finishing the active run frees the epic for a new run.
	claimed, err := m.DriverRuns().Claim(ctx, ws, "run-1", "node-a", "lease-a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := m.DriverRuns().Finish(ctx, ws, "run-1", "node-a", "lease-a", claimed.FencingToken, DriverRunFinish{Status: DriverRunCompleted}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	r4, err := m.DriverRuns().Create(ctx, ws, DriverRunCreate{
		RunID: "run-4", DriverID: drv, DriverVersionID: ver, EpicID: "EPIC-1", IdempotencyKey: "k4",
	})
	if err != nil || r4.RunID != "run-4" {
		t.Fatalf("post-finish admission: got %v run=%+v", err, r4)
	}
}

func TestMemStore_ClaimOwnershipAndFencing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemStore()
	drv, ver := seedDriver(t, m)
	if _, err := m.DriverRuns().Create(ctx, ws, DriverRunCreate{RunID: "run-1", DriverID: drv, DriverVersionID: ver}); err != nil {
		t.Fatal(err)
	}

	claimed, err := m.DriverRuns().Claim(ctx, ws, "run-1", "node-a", "lease-a")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.FencingToken == 0 || claimed.Status != DriverRunRunning {
		t.Fatalf("claim result: %+v", claimed)
	}
	if _, err := m.DriverRuns().Claim(ctx, ws, "run-1", "node-b", "lease-b"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("double claim: want conflict, got %v", err)
	}
	// Wrong fencing token rejected.
	if _, err := m.DriverRuns().Heartbeat(ctx, ws, "run-1", "node-a", "lease-a", claimed.FencingToken+1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("bad fence heartbeat: want conflict, got %v", err)
	}
	if _, err := m.DriverRuns().Heartbeat(ctx, ws, "run-1", "node-a", "lease-a", claimed.FencingToken); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
}

func TestMemStore_TaskRunDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemStore()
	if _, err := m.TaskRuns().Create(ctx, ws, TaskRunCreate{TaskRunID: "tr-T1", TaskID: "T1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.TaskRuns().Create(ctx, ws, TaskRunCreate{TaskRunID: "tr-T1", TaskID: "T1"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate: want already-exists, got %v", err)
	}
}

func TestMemStore_LedgerIdempotency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemStore()
	e1, err := m.ActionLedger().Create(ctx, ws, LedgerCreate{IdempotencyKey: "close-epic:E1", ActionType: "update_status", TargetRef: "E1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.ActionLedger().Complete(ctx, ws, e1.ActionID, LedgerApplied); err != nil {
		t.Fatalf("complete: %v", err)
	}
	e2, err := m.ActionLedger().Create(ctx, ws, LedgerCreate{IdempotencyKey: "close-epic:E1", ActionType: "update_status", TargetRef: "E1"})
	if err != nil || e2.ActionID != e1.ActionID || e2.Status != LedgerApplied {
		t.Fatalf("dedupe: got %v entry=%+v", err, e2)
	}
	// Completing again with the same status is a no-op.
	if _, err := m.ActionLedger().Complete(ctx, ws, e1.ActionID, LedgerApplied); err != nil {
		t.Fatalf("re-complete: %v", err)
	}
}

func TestMemStore_PollWakesOnAppend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemStore()

	// Immediate return with no timeout.
	page, err := m.Events().Poll(ctx, ws, MutationPoll{Since: "0"})
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("empty poll: %v %+v", err, page)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		m.AppendEvent(MutationEvent{Action: "issue.close", EntityType: "issue", EntityID: "T1"})
	}()
	start := time.Now()
	page, err = m.Events().Poll(ctx, ws, MutationPoll{Since: page.Cursor, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].EntityID != "T1" {
		t.Fatalf("poll events: %+v", page.Events)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("poll did not wake on append")
	}
	// Cursor advances past consumed events.
	page2, err := m.Events().Poll(ctx, ws, MutationPoll{Since: page.Cursor})
	if err != nil || len(page2.Events) != 0 {
		t.Fatalf("cursor advance: %v %+v", err, page2)
	}
}
