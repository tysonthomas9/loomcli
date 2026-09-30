//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
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

type captureOrderStore struct {
	store.Store
	onRecover     func(context.Context, string, string, store.StaleTaskRunRecovery)
	releaseResult *store.StaleTaskRunRecoveryResult
}

func (st captureOrderStore) DriverRuns() store.DriverRunStore {
	return captureOrderDriverRuns{DriverRunStore: st.Store.DriverRuns(), onRecover: st.onRecover, releaseResult: st.releaseResult}
}

type captureOrderDriverRuns struct {
	store.DriverRunStore
	onRecover     func(context.Context, string, string, store.StaleTaskRunRecovery)
	releaseResult *store.StaleTaskRunRecoveryResult
}

func (runs captureOrderDriverRuns) RecoverStaleTaskRuns(ctx context.Context, workspace, runID string, recovery store.StaleTaskRunRecovery) (*store.StaleTaskRunRecoveryResult, error) {
	if runs.onRecover != nil {
		runs.onRecover(ctx, workspace, runID, recovery)
	}
	result, err := runs.DriverRunStore.RecoverStaleTaskRuns(ctx, workspace, runID, recovery)
	if err != nil || runs.releaseResult == nil {
		return result, err
	}
	result.Released = runs.releaseResult.Released
	result.ReleasedTaskIDs = runs.releaseResult.ReleasedTaskIDs
	return result, nil
}

func TestStaleTaskSweeperReportsOwnershipRelease(t *testing.T) {
	for _, test := range []struct {
		name        string
		released    int
		releasedIDs []string
		wantWarning bool
	}{
		{name: "released task", released: 1, releasedIDs: []string{"WS-1"}},
		{name: "already released or not reopened", wantWarning: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
				t.Fatal(err)
			}
			seedSweeperFixture(t, st, "WS", domain.DriverRunRunning, 10*time.Minute)
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			response := &store.StaleTaskRunRecoveryResult{Released: test.released, ReleasedTaskIDs: test.releasedIDs}
			sweeper := &StaleTaskSweeper{Store: captureOrderStore{Store: st, releaseResult: response}, WorkspaceKey: "WS", MaxAge: 5 * time.Minute}
			if result, err := sweeper.RunOnce(ctx); err != nil || result.Recovered != 1 {
				t.Fatalf("sweep = %+v, %v", result, err)
			}
			output := logs.String()
			if !strings.Contains(output, "released="+fmt.Sprint(test.released)) ||
				!strings.Contains(output, "released_task_ids="+fmt.Sprint(test.releasedIDs)) ||
				strings.Contains(output, "level=WARN") != test.wantWarning {
				t.Fatalf("release response not reported correctly: %s", output)
			}
			if test.wantWarning && !strings.Contains(output, "task_run_ids=[task-run-1]") {
				t.Fatalf("warning does not name task run: %s", output)
			}
		})
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
			name:            "stale task recovered after driver run stopped",
			driverRunStatus: domain.DriverRunQueued,
			heartbeatAge:    10 * time.Minute,
			maxAge:          5 * time.Minute,
			sweepWorkspace:  "WS",
			wantRecovered:   1,
			wantTaskStatus:  domain.TaskRunFailed,
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

func TestStaleRemoteCaptureRequiresAttentionAndKeepsSandbox(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	seedSweeperFixture(t, st, "WS", domain.DriverRunRunning, 10*time.Minute)
	metadata := map[string]string{
		"remote_capture_status":  "pending",
		"remote_capture_attempt": "attempt-1",
		"daytona_sandbox_id":     "sandbox-retained",
	}
	if _, err := st.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", store.TaskRunHeartbeat{
		HeartbeatAt: time.Now().UTC().Add(-10 * time.Minute), RuntimeMetadata: metadata,
	}); err != nil {
		t.Fatal(err)
	}
	sweeper := &StaleTaskSweeper{Store: st, WorkspaceKey: "WS", MaxAge: 5 * time.Minute}
	result, err := sweeper.RunOnce(ctx)
	if err != nil || result.Recovered != 1 {
		t.Fatalf("sweep = %+v, %v", result, err)
	}
	taskRun, err := st.TaskRuns().Get(ctx, "WS", "task-run-1")
	if err != nil {
		t.Fatal(err)
	}
	if taskRun.Status != domain.TaskRunFailed || taskRun.ErrorClass != string(loomgit.AttentionRequired) ||
		!strings.Contains(taskRun.ErrorMessage, "sandbox-retained") {
		t.Fatalf("stale capture recovery = %s/%s: %s", taskRun.Status, taskRun.ErrorClass, taskRun.ErrorMessage)
	}
	if taskRun.RuntimeMetadata["daytona_sandbox_id"] != "sandbox-retained" ||
		taskRun.RuntimeMetadata["remote_capture_status"] != "pending" {
		t.Fatalf("retained sandbox capture metadata lost: %+v", taskRun.RuntimeMetadata)
	}
}

func TestStaleTaskSweeperRequiresStore(t *testing.T) {
	if _, err := (&StaleTaskSweeper{}).RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce with nil store: expected error, got nil")
	}
}

func TestStaleTaskSweeperCapturesBeforeOwnershipRelease(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", root)
	source := filepath.Join(root, "source")
	copyPath := filepath.Join(root, "copy")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, source, "init")
	gitCmd(t, source, "config", "user.name", "Test")
	gitCmd(t, source, "config", "user.email", "test@example.test")
	writeTestFile(t, filepath.Join(source, "tracked.txt"), "base\n")
	gitCmd(t, source, "add", "tracked.txt")
	gitCmd(t, source, "commit", "-m", "base")
	base := strings.TrimSpace(testGitOutput(t, source, "rev-parse", "HEAD"))
	journalPath := filepath.Join(root, "loomgit", "store.db")
	if _, err := taskcopy.CreateDetailedAt(ctx, journalPath, source, copyPath, "WS", "attempt-1", "", base); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(copyPath, "untracked.txt"), "agent work\n")
	child := exec.Command(os.Args[0], "-test.run=^TestStaleTaskCopyChild$") //nolint:norawexec // Kill a child against a temporary task copy to prove crash recovery.
	child.Env = append(os.Environ(), "LOOM_STALE_COPY_TEST_PATH="+copyPath)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	childReady := filepath.Join(copyPath, "child-ready")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(childReady); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal("task-copy child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	seedSweeperFixture(t, st, "WS", domain.DriverRunQueued, 10*time.Minute)
	if _, err := st.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", store.TaskRunHeartbeat{
		HeartbeatAt: time.Now().UTC().Add(-10 * time.Minute),
		RuntimeMetadata: map[string]string{
			"task_copy_path": copyPath, "attempt_id": "attempt-1", "attempt_base_sha": base,
			"repo_name": "app", "source_repo_path": source,
		},
	}); err != nil {
		t.Fatal(err)
	}
	gitStore, err := sql.Open("sqlite", journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gitStore.Close() }()
	var outcome, head string
	var ready, capturedBeforeRecovery bool
	now := time.Now().UTC()
	sweeper := &StaleTaskSweeper{Store: captureOrderStore{Store: st, onRecover: func(ctx context.Context, workspace, runID string, recovery store.StaleTaskRunRecovery) {
		if workspace != "WS" || runID != "run-1" || !recovery.StaleBefore.Equal(now.Add(-5*time.Minute)) ||
			recovery.ErrorClass != "stale_task_run" || recovery.ErrorMessage != "task run heartbeat is stale" {
			t.Fatalf("unexpected ownership recovery: %s/%s %+v", workspace, runID, recovery)
		}
		if err := gitStore.QueryRowContext(ctx, `SELECT outcome, head_sha, ready FROM change_revisions WHERE request_id = ?`, "driver:attempt-1").Scan(&outcome, &head, &ready); err != nil || outcome != "failed" || !ready {
			t.Fatalf("ownership released before failed capture: %q/%q/%v, %v", outcome, head, ready, err)
		}
		capturedBeforeRecovery = true
	}}, WorkspaceKey: "WS", MaxAge: 5 * time.Minute, Now: func() time.Time { return now }}
	result, err := sweeper.RunOnce(ctx)
	if err != nil || result.Recovered != 1 || !capturedBeforeRecovery {
		t.Fatalf("sweep = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(filepath.Join(copyPath, "untracked.txt")); err != nil || string(got) != "agent work\n" {
		t.Fatalf("task copy was not retained: %q, %v", got, err)
	}
	if got := strings.TrimSpace(testGitOutput(t, source, "show", head+":untracked.txt")); got != "agent work" {
		t.Fatalf("source revision lost work: %q", got)
	}
	if next, err := (&StaleTaskSweeper{Store: st, WorkspaceKey: "WS", MaxAge: 5 * time.Minute}).RunOnce(ctx); err != nil || next.Recovered != 0 {
		t.Fatalf("cold sweep = %+v, %v", next, err)
	}
	if got, err := os.ReadFile(filepath.Join(copyPath, "untracked.txt")); err != nil || string(got) != "agent work\n" {
		t.Fatalf("cold sweep changed task copy: %q, %v", got, err)
	}
}

func TestStaleTaskCopyChild(t *testing.T) {
	path := os.Getenv("LOOM_STALE_COPY_TEST_PATH")
	if path == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(path, "child-ready"), []byte("crash work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestStaleTaskSweeperUnknownCopyNeedsAttention(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	seedSweeperFixture(t, st, "WS", domain.DriverRunRunning, 10*time.Minute)
	if _, err := st.TaskRuns().Heartbeat(ctx, "WS", "task-run-1", store.TaskRunHeartbeat{
		HeartbeatAt:     time.Now().UTC().Add(-10 * time.Minute),
		RuntimeMetadata: map[string]string{"task_copy_path": t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := (&StaleTaskSweeper{Store: st, WorkspaceKey: "WS", MaxAge: 5 * time.Minute}).RunOnce(ctx)
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.AttentionRequired) {
		t.Fatalf("incomplete copy = %v, want attention_required", err)
	}
	got, err := st.TaskRuns().Get(ctx, "WS", "task-run-1")
	if err != nil || got.Status != domain.TaskRunRunning {
		t.Fatalf("unknown copy mutated task run: %+v, %v", got, err)
	}
}
