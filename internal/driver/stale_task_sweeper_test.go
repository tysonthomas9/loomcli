//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// seedSweeperFixture creates a driver, version, driver run (optionally
// claimed into running), and one running TaskRun whose LastHeartbeat is
// backdated by heartbeatAge.
func seedSweeperFixture(t *testing.T, st *memstore.Store, ws string, driverRunStatus domain.DriverRunStatus, heartbeatAge time.Duration) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws,
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     ws,
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    ws,
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	if driverRunStatus == domain.DriverRunRunning {
		if _, err := st.DriverRuns().Claim(ctx, ws, "run-1", "node-1", "lease-1"); err != nil {
			t.Fatalf("Claim driver run: %v", err)
		}
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: ws,
		TaskRunID:    "task-run-1",
		DriverRunID:  "run-1",
		TaskID:       ws + "-1",
		Status:       domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	if _, err := st.TaskRuns().Heartbeat(ctx, ws, "task-run-1", store.TaskRunHeartbeat{
		HeartbeatAt: time.Now().UTC().Add(-heartbeatAge),
	}); err != nil {
		t.Fatalf("Heartbeat task run: %v", err)
	}
}

func TestStaleTaskSweeperRunOnce(t *testing.T) {
	tests := []struct {
		name             string
		driverRunStatus  domain.DriverRunStatus
		heartbeatAge     time.Duration
		maxAge           time.Duration
		sweepWorkspace   string
		wantRecovered    int
		wantSkippedFresh int
		wantTaskStatus   domain.TaskRunStatus
	}{
		{
			name:            "stale running task run fails with stale_task_run",
			driverRunStatus: domain.DriverRunRunning,
			heartbeatAge:    10 * time.Minute,
			maxAge:          5 * time.Minute,
			sweepWorkspace:  "WS",
			wantRecovered:   1,
			wantTaskStatus:  domain.TaskRunFailed,
		},
		{
			name:             "fresh heartbeat untouched",
			driverRunStatus:  domain.DriverRunRunning,
			heartbeatAge:     time.Minute,
			maxAge:           5 * time.Minute,
			sweepWorkspace:   "WS",
			wantSkippedFresh: 1,
			wantTaskStatus:   domain.TaskRunRunning,
		},
		{
			name:            "non-running driver run skipped",
			driverRunStatus: domain.DriverRunQueued,
			heartbeatAge:    10 * time.Minute,
			maxAge:          5 * time.Minute,
			sweepWorkspace:  "WS",
			wantTaskStatus:  domain.TaskRunRunning,
		},
		{
			name:            "zero max age defaults to twenty minutes",
			driverRunStatus: domain.DriverRunRunning,
			heartbeatAge:    25 * time.Minute,
			maxAge:          0,
			sweepWorkspace:  "WS",
			wantRecovered:   1,
			wantTaskStatus:  domain.TaskRunFailed,
		},
		{
			// Regression: a long-but-live run (e.g. a daytona sandbox + agent run,
			// observed at ~11-12m) must NOT be swept under the default threshold.
			// The old 5-minute default killed exactly this; 20m spares it.
			name:             "long live run within default threshold not swept",
			driverRunStatus:  domain.DriverRunRunning,
			heartbeatAge:     12 * time.Minute,
			maxAge:           0,
			sweepWorkspace:   "WS",
			wantSkippedFresh: 1,
			wantTaskStatus:   domain.TaskRunRunning,
		},
		{
			name:            "empty workspace key sweeps all workspaces",
			driverRunStatus: domain.DriverRunRunning,
			heartbeatAge:    10 * time.Minute,
			maxAge:          5 * time.Minute,
			sweepWorkspace:  "",
			wantRecovered:   1,
			wantTaskStatus:  domain.TaskRunFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
				t.Fatalf("Create workspace: %v", err)
			}
			seedSweeperFixture(t, st, "WS", tt.driverRunStatus, tt.heartbeatAge)

			sweeper := &StaleTaskSweeper{Store: st, WorkspaceKey: tt.sweepWorkspace, MaxAge: tt.maxAge}
			result, err := sweeper.RunOnce(ctx)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if result.Recovered != tt.wantRecovered || result.SkippedFresh != tt.wantSkippedFresh {
				t.Fatalf("result = %+v, want recovered=%d skippedFresh=%d", result, tt.wantRecovered, tt.wantSkippedFresh)
			}
			if tt.wantRecovered > 0 && (len(result.RecoveredTaskRunIDs) != tt.wantRecovered || result.RecoveredTaskRunIDs[0] != "task-run-1") {
				t.Fatalf("recovered task run ids = %v, want [task-run-1]", result.RecoveredTaskRunIDs)
			}

			taskRun, err := st.TaskRuns().Get(ctx, "WS", "task-run-1")
			if err != nil {
				t.Fatalf("Get task run: %v", err)
			}
			if taskRun.Status != tt.wantTaskStatus {
				t.Fatalf("task run status = %s, want %s", taskRun.Status, tt.wantTaskStatus)
			}
			if tt.wantTaskStatus == domain.TaskRunFailed {
				if taskRun.ErrorClass != "stale_task_run" || taskRun.ErrorMessage != "task run heartbeat is stale" {
					t.Fatalf("task run error = %q/%q, want stale_task_run/heartbeat message", taskRun.ErrorClass, taskRun.ErrorMessage)
				}
				if taskRun.FinishedAt == nil {
					t.Fatal("task run FinishedAt = nil, want set")
				}
			}
		})
	}
}

func TestStaleTaskSweeperRequiresStore(t *testing.T) {
	if _, err := (&StaleTaskSweeper{}).RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce with nil store: expected error, got nil")
	}
}

func TestStaleTaskSweeperReconcilesSessionsInRecoveryPass(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	seedSweeperFixture(t, st, "WS", domain.DriverRunRunning, 10*time.Minute)
	ref, err := st.AgentSessions().Open(ctx, store.SessionRunContext{
		WorkspaceKey: "WS", TaskRunID: "task-run-1", Attempt: 1, FencingToken: 0,
	}, store.SessionDescriptor{InvocationKey: "agent", Backend: "codex", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("Open agent session: %v", err)
	}
	wrongFence, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "stale-wrong-fence", AgentID: "codex", TaskRunID: "task-run-1",
		InvocationKey: "wrong-fence", Status: domain.AgentSessionRunning, Attempt: 1,
		Metadata: map[string]string{store.SessionMetadataFencingToken: "1"},
	})
	if err != nil {
		t.Fatalf("Create wrong-fence session: %v", err)
	}
	result, err := (&StaleTaskSweeper{Store: st, WorkspaceKey: "WS", MaxAge: 5 * time.Minute}).RunOnce(ctx)
	if err != nil || result.Recovered != 1 || result.ReconciledSessions != 1 {
		t.Fatalf("RunOnce = %+v, %v", result, err)
	}
	session, err := st.AgentSessions().Get(ctx, "WS", ref.SessionID)
	if err != nil {
		t.Fatalf("Get agent session: %v", err)
	}
	if session.Status != domain.AgentSessionFailed || session.ErrorClass != staleTaskRunErrorClass || session.Metadata["finalized_by"] != SessionFinalizedByStale {
		t.Fatalf("stale-reconciled session = %+v", session)
	}
	untouched, err := st.AgentSessions().Get(ctx, "WS", wrongFence.SessionID)
	if err != nil || untouched.Status != domain.AgentSessionRunning {
		t.Fatalf("wrong-fence stale session changed: session=%+v err=%v", untouched, err)
	}
}
