package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func setupBlockTestStore(t *testing.T) (context.Context, *Store) {
	t.Helper()
	ctx := t.Context()
	s := New()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    workflowcatalog.DriverOwnerSystem,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "WS-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	return ctx, s
}

func createBlockTestTaskRun(t *testing.T, ctx context.Context, s *Store, taskRunID string) *domain.TaskRun {
	t.Helper()
	run, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "WS",
		TaskRunID:       taskRunID,
		DriverRunID:     "run-1",
		TaskID:          "WS-2",
		ProviderProfile: "codex-default",
		Status:          domain.TaskRunRunning,
		NodeID:          "node-1",
		LeaseID:         "lease-1",
	})
	if err != nil {
		t.Fatalf("Create task run %s: %v", taskRunID, err)
	}
	return run
}

func TestTaskRunFinishBlockTaskMarksTaskBlocked(t *testing.T) {
	ctx, s := setupBlockTestStore(t)
	run := createBlockTestTaskRun(t, ctx, s, "task-run-block-1")

	exitCode := 1
	// BlockTask is only valid on failed finishes.
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-block-1", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: run.FencingToken,
		Status:       domain.TaskRunCompleted,
		ExitCode:     &exitCode,
		BlockTask:    true,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Finish block+completed err = %v, want ErrInvalidTransition", err)
	}
	if s.TaskBlocked("WS", "WS-2") {
		t.Fatalf("task blocked after rejected finish, want not blocked")
	}

	finished, err := s.TaskRuns().Finish(ctx, "WS", "task-run-block-1", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: run.FencingToken,
		Status:       domain.TaskRunFailed,
		ExitCode:     &exitCode,
		ErrorClass:   "task_failed",
		ErrorMessage: "attempts exhausted",
		BlockTask:    true,
	})
	if err != nil {
		t.Fatalf("Finish block: %v", err)
	}
	if finished.Status != domain.TaskRunFailed || finished.FinishedAt == nil {
		t.Fatalf("finished = %+v, want failed with finished_at", finished)
	}
	if !s.TaskBlocked("WS", "WS-2") {
		t.Fatalf("task not marked blocked after BlockTask finish")
	}
	if s.TaskBlocked("WS", "WS-OTHER") || s.TaskBlocked("OTHER", "WS-2") {
		t.Fatalf("unrelated task/workspace marked blocked")
	}

	// Blocking again via another run on the same task is an idempotent no-op.
	second := createBlockTestTaskRun(t, ctx, s, "task-run-block-2")
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-block-2", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: second.FencingToken,
		Status:       domain.TaskRunFailed,
		ExitCode:     &exitCode,
		BlockTask:    true,
	}); err != nil {
		t.Fatalf("Finish block already-blocked task: %v", err)
	}
	if !s.TaskBlocked("WS", "WS-2") {
		t.Fatalf("task no longer blocked after second block")
	}
}

func TestTaskRunFinishWithoutBlockTaskLeavesTaskUnblocked(t *testing.T) {
	ctx, s := setupBlockTestStore(t)
	run := createBlockTestTaskRun(t, ctx, s, "task-run-no-block")

	exitCode := 1
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-no-block", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: run.FencingToken,
		Status:       domain.TaskRunFailed,
		ExitCode:     &exitCode,
	}); err != nil {
		t.Fatalf("Finish without block: %v", err)
	}
	if s.TaskBlocked("WS", "WS-2") {
		t.Fatalf("task blocked without BlockTask, want not blocked")
	}
}
