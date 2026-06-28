package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func setupParkTestStore(t *testing.T) (context.Context, *Store) {
	t.Helper()
	ctx := t.Context()
	s := New()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
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
		ValidationStatus: domain.DriverVersionValidationPassed,
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

func createParkTestTaskRun(t *testing.T, ctx context.Context, s *Store, taskRunID string) *domain.TaskRun {
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

func TestTaskRunFinishParkTaskMarksTaskParked(t *testing.T) {
	ctx, s := setupParkTestStore(t)
	run := createParkTestTaskRun(t, ctx, s, "task-run-park-1")

	exitCode := 1
	// ParkTask is only valid on failed finishes.
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-park-1", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: run.FencingToken,
		Status:       domain.TaskRunCompleted,
		ExitCode:     &exitCode,
		ParkTask:     true,
	}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("Finish park+completed err = %v, want ErrInvalidTransition", err)
	}
	if s.TaskParked("WS", "WS-2") {
		t.Fatalf("task parked after rejected finish, want not parked")
	}

	finished, err := s.TaskRuns().Finish(ctx, "WS", "task-run-park-1", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: run.FencingToken,
		Status:       domain.TaskRunFailed,
		ExitCode:     &exitCode,
		ErrorClass:   "task_failed",
		ErrorMessage: "attempts exhausted",
		ParkTask:     true,
	})
	if err != nil {
		t.Fatalf("Finish park: %v", err)
	}
	if finished.Status != domain.TaskRunFailed || finished.FinishedAt == nil {
		t.Fatalf("finished = %+v, want failed with finished_at", finished)
	}
	if !s.TaskParked("WS", "WS-2") {
		t.Fatalf("task not marked parked after ParkTask finish")
	}
	if s.TaskParked("WS", "WS-OTHER") || s.TaskParked("OTHER", "WS-2") {
		t.Fatalf("unrelated task/workspace marked parked")
	}

	// Parking again via another run on the same task is an idempotent no-op.
	second := createParkTestTaskRun(t, ctx, s, "task-run-park-2")
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-park-2", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: second.FencingToken,
		Status:       domain.TaskRunFailed,
		ExitCode:     &exitCode,
		ParkTask:     true,
	}); err != nil {
		t.Fatalf("Finish park already-parked task: %v", err)
	}
	if !s.TaskParked("WS", "WS-2") {
		t.Fatalf("task no longer parked after second park")
	}
}

func TestTaskRunFinishWithoutParkTaskLeavesTaskUnparked(t *testing.T) {
	ctx, s := setupParkTestStore(t)
	run := createParkTestTaskRun(t, ctx, s, "task-run-no-park")

	exitCode := 1
	if _, err := s.TaskRuns().Finish(ctx, "WS", "task-run-no-park", store.TaskRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: run.FencingToken,
		Status:       domain.TaskRunFailed,
		ExitCode:     &exitCode,
	}); err != nil {
		t.Fatalf("Finish without park: %v", err)
	}
	if s.TaskParked("WS", "WS-2") {
		t.Fatalf("task parked without ParkTask, want not parked")
	}
}
