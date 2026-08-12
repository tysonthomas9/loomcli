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
	seedSweeperFixtureAt(t, st, ws, driverRunStatus, time.Now().UTC().Add(-heartbeatAge))
}

func seedSweeperFixtureAt(t *testing.T, st *memstore.Store, ws string, driverRunStatus domain.DriverRunStatus, heartbeatAt time.Time) {
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
		HeartbeatAt: heartbeatAt,
	}); err != nil {
		t.Fatalf("Heartbeat task run: %v", err)
	}
}

func TestStaleTaskSweeperUsesMonotonicElapsedTimeAcrossWallClockJumps(t *testing.T) {
	t.Run("forward jump before first sweep cannot age a live heartbeat", func(t *testing.T) {
		ctx, st, sweeper, wall, monotonic := newClockJumpSweeper(t)
		*wall = wall.Add(2 * time.Hour)
		*monotonic = 2 * time.Second

		assertSweepResult(t, sweeper, ctx, 0, 1)
		assertTaskRunStatus(t, st, domain.TaskRunRunning)
	})

	t.Run("forward jump cannot age a live heartbeat", func(t *testing.T) {
		ctx, st, sweeper, wall, monotonic := newClockJumpSweeper(t)
		assertSweepResult(t, sweeper, ctx, 0, 1)

		*wall = wall.Add(2 * time.Hour)
		*monotonic = 2 * time.Second
		assertSweepResult(t, sweeper, ctx, 0, 1)
		assertTaskRunStatus(t, st, domain.TaskRunRunning)
	})

	t.Run("backward jump protects a fresh heartbeat and recovery resumes", func(t *testing.T) {
		ctx, st, sweeper, wall, monotonic := newClockJumpSweeper(t)
		assertSweepResult(t, sweeper, ctx, 0, 1)

		*wall = wall.Add(-2 * time.Hour)
		*monotonic = 2 * time.Second
		if _, err := st.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", store.TaskRunHeartbeat{HeartbeatAt: *wall}); err != nil {
			t.Fatalf("post-jump heartbeat: %v", err)
		}
		assertSweepResult(t, sweeper, ctx, 0, 1)

		*wall = wall.Add(19 * time.Minute)
		*monotonic += 19 * time.Minute
		assertSweepResult(t, sweeper, ctx, 0, 1)

		*wall = wall.Add(2 * time.Minute)
		*monotonic += 2 * time.Minute
		assertSweepResult(t, sweeper, ctx, 1, 0)
		assertTaskRunStatus(t, st, domain.TaskRunFailed)
	})
}

func newClockJumpSweeper(t *testing.T) (context.Context, *memstore.Store, *StaleTaskSweeper, *time.Time, *time.Duration) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	wall := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	monotonic := time.Duration(0)
	seedSweeperFixtureAt(t, st, "WS", domain.DriverRunRunning, wall.Add(-10*time.Minute))
	sweeper := &StaleTaskSweeper{
		Store: st, WorkspaceKey: "WS", MaxAge: 20 * time.Minute,
		Now: func() time.Time { return wall }, MonotonicNow: func() time.Duration { return monotonic },
		ClockOrigin: wall,
	}
	return ctx, st, sweeper, &wall, &monotonic
}

func assertSweepResult(t *testing.T, sweeper *StaleTaskSweeper, ctx context.Context, recovered, fresh int) {
	t.Helper()
	result, err := sweeper.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Recovered != recovered || result.SkippedFresh != fresh {
		t.Fatalf("result = %+v, want recovered=%d skippedFresh=%d", result, recovered, fresh)
	}
}

func assertTaskRunStatus(t *testing.T, st *memstore.Store, want domain.TaskRunStatus) {
	t.Helper()
	run, err := st.TaskRuns().Get(context.Background(), "WS", "task-run-1")
	if err != nil {
		t.Fatalf("Get task run: %v", err)
	}
	if run.Status != want {
		t.Fatalf("task run status = %s, want %s", run.Status, want)
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
