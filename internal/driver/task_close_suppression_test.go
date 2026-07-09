//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestResolveCloseTaskOnSuccess covers the per-run override resolver: the
// persisted metadata value wins when present and parseable; otherwise the
// caller's default is preserved byte-for-byte.
func TestResolveCloseTaskOnSuccess(t *testing.T) {
	cases := []struct {
		name     string
		fallback bool
		meta     map[string]string
		want     bool
	}{
		{"nil metadata keeps default true", true, nil, true},
		{"nil metadata keeps default false", false, nil, false},
		{"absent key keeps default true", true, map[string]string{"other": "x"}, true},
		{"explicit false overrides default true", true, map[string]string{TaskRunCloseOnSuccessMetaKey: "false"}, false},
		{"explicit true overrides default false", false, map[string]string{TaskRunCloseOnSuccessMetaKey: "true"}, true},
		{"unparseable value keeps default", true, map[string]string{TaskRunCloseOnSuccessMetaKey: "maybe"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveCloseTaskOnSuccess(tc.fallback, tc.meta); got != tc.want {
				t.Fatalf("resolveCloseTaskOnSuccess(%v, %v) = %v, want %v", tc.fallback, tc.meta, got, tc.want)
			}
		})
	}
}

// TestEnqueueTaskRunPersistsCloseTaskOverride proves a per-request
// CloseTaskOnSuccess=false is persisted on the queued run's RuntimeMetadata so a
// later worker claim can honor it, while a nil override adds NO key (existing
// callers unchanged).
func TestEnqueueTaskRunPersistsCloseTaskOverride(t *testing.T) {
	closeFalse := false
	t.Run("false override persisted", func(t *testing.T) {
		ctx, st, run := setupRunningDriverRun(t)
		outcome, err := EnqueueTaskRunWithResult(ctx, st, TaskRunRequestOptions{
			WorkspaceKey:       "TEST",
			DriverRunID:        run.RunID,
			TaskRunID:          "task-run-plan",
			TaskID:             "TEST-PLAN",
			ProviderProfile:    "codex-default",
			SupportedProviders: []string{"codex-default"},
			ParentNodeID:       run.NodeID,
			ParentLeaseID:      run.LeaseID,
			ParentFence:        run.FencingToken,
			CloseTaskOnSuccess: &closeFalse,
		}, HostBridgeTaskExecutor{Command: []string{"unused"}})
		if err != nil {
			t.Fatalf("EnqueueTaskRunWithResult: %v", err)
		}
		if got := outcome.Run.RuntimeMetadata[TaskRunCloseOnSuccessMetaKey]; got != "false" {
			t.Fatalf("queued metadata[%s] = %q, want \"false\"", TaskRunCloseOnSuccessMetaKey, got)
		}
	})

	t.Run("nil override adds no key", func(t *testing.T) {
		ctx, st, run := setupRunningDriverRun(t)
		outcome, err := EnqueueTaskRunWithResult(ctx, st, TaskRunRequestOptions{
			WorkspaceKey:       "TEST",
			DriverRunID:        run.RunID,
			TaskRunID:          "task-run-coder",
			TaskID:             "TEST-CODER",
			ProviderProfile:    "codex-default",
			SupportedProviders: []string{"codex-default"},
			ParentNodeID:       run.NodeID,
			ParentLeaseID:      run.LeaseID,
			ParentFence:        run.FencingToken,
		}, HostBridgeTaskExecutor{Command: []string{"unused"}})
		if err != nil {
			t.Fatalf("EnqueueTaskRunWithResult: %v", err)
		}
		if _, ok := outcome.Run.RuntimeMetadata[TaskRunCloseOnSuccessMetaKey]; ok {
			t.Fatalf("queued metadata unexpectedly carries %s: %+v", TaskRunCloseOnSuccessMetaKey, outcome.Run.RuntimeMetadata)
		}
	})
}

// TestWorkerHonorsPersistedCloseTaskOverride is the end-to-end assertion: a run
// enqueued with closeTask=false is later claimed by a worker whose default is
// CloseTaskOnSuccess=true (task_worker.go), and the persisted override wins —
// the run finishes WITHOUT closing the task (TaskRuns().Finish, not
// Complete{CloseTask:true}). The companion default case DOES close.
func TestWorkerHonorsPersistedCloseTaskOverride(t *testing.T) {
	run := func(t *testing.T, override *bool) *spyTaskRunStore {
		t.Helper()
		ctx, base, driverRun := setupRunningDriverRun(t)
		spyTR := &spyTaskRunStore{TaskRunStore: base.TaskRuns()}
		st := &spyStore{Store: base, taskRuns: spyTR}

		if _, err := EnqueueTaskRunWithResult(ctx, st, TaskRunRequestOptions{
			WorkspaceKey:       "TEST",
			DriverRunID:        driverRun.RunID,
			TaskRunID:          "task-run-close",
			TaskID:             "TEST-CLOSE",
			ProviderProfile:    "codex-default",
			SupportedProviders: []string{"codex-default"},
			ParentNodeID:       driverRun.NodeID,
			ParentLeaseID:      driverRun.LeaseID,
			ParentFence:        driverRun.FencingToken,
			CloseTaskOnSuccess: override,
		}, HostBridgeTaskExecutor{Command: []string{"unused"}}); err != nil {
			t.Fatalf("EnqueueTaskRunWithResult: %v", err)
		}

		// The serve task worker always passes CloseTaskOnSuccess:true; the
		// persisted per-run override is what must win.
		if _, err := ClaimAndExecuteTaskRunWithResult(ctx, st, TaskRunWorkerOptions{
			WorkspaceKey:       "TEST",
			TaskRunID:          "task-run-close",
			NodeID:             "node-1",
			SupportedProviders: []string{"codex-default"},
			CloseTaskOnSuccess: true,
			HeartbeatInterval:  -1,
		}, &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted}}); err != nil {
			t.Fatalf("ClaimAndExecuteTaskRunWithResult: %v", err)
		}
		return spyTR
	}

	t.Run("closeTask=false suppresses the close", func(t *testing.T) {
		closeFalse := false
		spyTR := run(t, &closeFalse)
		if spyTR.closeTaskComplete != nil {
			t.Fatalf("Complete{CloseTask:%v} was called; override false must take the non-closing Finish path", *spyTR.closeTaskComplete)
		}
		if !spyTR.finishCalled {
			t.Fatal("Finish was not called; expected the non-closing completion path")
		}
	})

	t.Run("default closes the task", func(t *testing.T) {
		spyTR := run(t, nil)
		if spyTR.closeTaskComplete == nil || *spyTR.closeTaskComplete != true {
			t.Fatalf("Complete{CloseTask:true} not observed; got %v", spyTR.closeTaskComplete)
		}
		if spyTR.finishCalled {
			t.Fatal("Finish was called; expected the closing Complete path for the default")
		}
	})
}

// spyStore wraps a real store.Store, swapping only the TaskRunStore so a test
// can observe which completion path (Complete vs Finish) a run takes.
type spyStore struct {
	store.Store
	taskRuns *spyTaskRunStore
}

func (s *spyStore) TaskRuns() store.TaskRunStore { return s.taskRuns }

// spyTaskRunStore delegates to the real TaskRunStore, recording whether the
// closing Complete path or the non-closing Finish path was taken.
type spyTaskRunStore struct {
	store.TaskRunStore
	closeTaskComplete *bool
	finishCalled      bool
}

func (s *spyTaskRunStore) Complete(ctx context.Context, ws, id string, complete store.TaskRunComplete) (*domain.TaskRun, error) {
	v := complete.CloseTask
	s.closeTaskComplete = &v
	return s.TaskRunStore.Complete(ctx, ws, id, complete)
}

func (s *spyTaskRunStore) Finish(ctx context.Context, ws, id string, finish store.TaskRunFinish) (*domain.TaskRun, error) {
	s.finishCalled = true
	return s.TaskRunStore.Finish(ctx, ws, id, finish)
}
