package driver

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type closeRecordingTaskRuns struct {
	store.TaskRunStore
	closeTask []bool
}

func (r *closeRecordingTaskRuns) Complete(ctx context.Context, ws, id string, complete store.TaskRunComplete) (*domain.TaskRun, error) {
	r.closeTask = append(r.closeTask, complete.CloseTask)
	return r.TaskRunStore.Complete(ctx, ws, id, complete)
}

type closeRecordingStore struct {
	store.Store
	runs *closeRecordingTaskRuns
}

func (s closeRecordingStore) TaskRuns() store.TaskRunStore { return s.runs }

func stubAttemptAwaitsReview(t *testing.T, awaits map[string]bool, err error) *[]string {
	t.Helper()
	var asked []string
	prior := attemptAwaitsReview
	attemptAwaitsReview = func(_ context.Context, workspace, attempt string) (bool, error) {
		asked = append(asked, workspace+"/"+attempt)
		return awaits[attempt], err
	}
	t.Cleanup(func() { attemptAwaitsReview = prior })
	return &asked
}

type markedTask struct{ workspace, task string }

func recordingMarker(marked *[]markedTask, err error) TaskReviewMarker {
	return func(_ context.Context, workspace, task string) error {
		*marked = append(*marked, markedTask{workspace, task})
		return err
	}
}

func runWorkerCompletion(t *testing.T, attempt string, marker TaskReviewMarker) (*closeRecordingTaskRuns, error) {
	t.Helper()
	ctx, st, run := setupRunningDriverRun(t)
	createQueuedEventTaskRun(t, ctx, st, run.RunID, "task-run-review")
	runs := &closeRecordingTaskRuns{TaskRunStore: st.TaskRuns()}
	_, err := ClaimAndExecuteTaskRunWithResult(ctx, closeRecordingStore{Store: st, runs: runs}, TaskRunWorkerOptions{
		WorkspaceKey: "TEST", TaskRunID: "task-run-review", NodeID: "node-1",
		SupportedProviders: []string{"local-noop"}, HeartbeatInterval: -1,
		CloseTaskOnSuccess: true, ReviewMarker: marker,
	}, &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted,
		RuntimeMetadata: map[string]string{"attempt_id": attempt}}})
	return runs, err
}

// D29 / P1.26: a successful run whose attempt froze code awaiting review
// leaves its task open in review instead of closing it.
func TestTaskWorkerKeepsTaskWithCodeOpenForReview(t *testing.T) {
	stubAttemptAwaitsReview(t, map[string]bool{"att-code": true}, nil)
	var marked []markedTask
	runs, err := runWorkerCompletion(t, "att-code", recordingMarker(&marked, nil))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(runs.closeTask) != 1 || runs.closeTask[0] {
		t.Fatalf("CloseTask = %v, want one completion that keeps the task open", runs.closeTask)
	}
	if len(marked) != 1 || marked[0] != (markedTask{"TEST", "TEST-EVT-2"}) {
		t.Fatalf("marked = %+v, want TEST-EVT-2 in review", marked)
	}
}

// P1.25 still closes: an empty attempt (or a run with no Loom Git copy)
// closes its task, and no task is put in review.
func TestTaskWorkerClosesTaskWithNoChangesOrNoRevision(t *testing.T) {
	stubAttemptAwaitsReview(t, map[string]bool{}, nil)
	var marked []markedTask
	runs, err := runWorkerCompletion(t, "att-empty", recordingMarker(&marked, nil))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(runs.closeTask) != 1 || !runs.closeTask[0] || len(marked) != 0 {
		t.Fatalf("CloseTask = %v marked = %+v, want a closing completion and no review", runs.closeTask, marked)
	}
}

// A failed review mark or journal read never closes the task: the run fails
// to complete and its lease decides what happens next.
func TestTaskWorkerNeverClosesWhenReviewCannotBeRecorded(t *testing.T) {
	stubAttemptAwaitsReview(t, map[string]bool{"att-code": true}, nil)
	var marked []markedTask
	runs, err := runWorkerCompletion(t, "att-code", recordingMarker(&marked, errors.New("fleet down")))
	if err == nil || len(runs.closeTask) != 0 {
		t.Fatalf("err = %v CloseTask = %v, want an error and no completion", err, runs.closeTask)
	}
	stubAttemptAwaitsReview(t, nil, errors.New("journal locked"))
	runs, err = runWorkerCompletion(t, "att-code", recordingMarker(&marked, nil))
	if err == nil || len(runs.closeTask) != 0 {
		t.Fatalf("err = %v CloseTask = %v, want an error and no completion", err, runs.closeTask)
	}
}

// The deferred completion (driver complete-task / driver-op API) follows the
// same rule, reading the attempt the bridge recorded on the run.
func TestCompleteDriverTaskRunKeepsTaskWithCodeOpenForReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		awaits    bool
		wantClose bool
	}{{"code", true, false}, {"no changes", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			asked := stubAttemptAwaitsReview(t, map[string]bool{"att-deferred": tc.awaits}, nil)
			ctx, st, run := setupRunningDriverRun(t)
			createQueuedEventTaskRun(t, ctx, st, run.RunID, "task-run-deferred")
			outcome, err := ClaimAndExecuteTaskRunWithResult(ctx, st, TaskRunWorkerOptions{
				WorkspaceKey: "TEST", TaskRunID: "task-run-deferred", NodeID: "node-1",
				SupportedProviders: []string{"local-noop"}, HeartbeatInterval: -1, DeferCompletion: true,
			}, &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted,
				RuntimeMetadata: map[string]string{"attempt_id": "att-deferred"}}})
			if err != nil {
				t.Fatalf("defer: %v", err)
			}
			runs := &closeRecordingTaskRuns{TaskRunStore: st.TaskRuns()}
			var marked []markedTask
			if _, err := CompleteDriverTaskRun(ctx, runs, "TEST", "task-run-deferred", DriverTaskRunCompletionOptions{
				LeaseToken: outcome.LeaseToken, ReviewMarker: recordingMarker(&marked, nil),
			}); err != nil {
				t.Fatalf("CompleteDriverTaskRun: %v", err)
			}
			if len(runs.closeTask) != 1 || runs.closeTask[0] != tc.wantClose || (len(marked) == 1) == tc.wantClose {
				t.Fatalf("CloseTask = %v marked = %+v asked = %v, want close=%v", runs.closeTask, marked, *asked, tc.wantClose)
			}
		})
	}
}

// serve's task worker carries its review marker into every run it claims.
func TestTaskWorkerPassesItsReviewMarkerToRuns(t *testing.T) {
	stubAttemptAwaitsReview(t, map[string]bool{"att-worker": true}, nil)
	ctx, st, run := setupRunningDriverRun(t)
	createQueuedEventTaskRun(t, ctx, st, run.RunID, "task-run-worker-review")
	runs := &closeRecordingTaskRuns{TaskRunStore: st.TaskRuns()}
	var marked []markedTask
	if _, err := (&TaskWorker{Store: closeRecordingStore{Store: st, runs: runs}, WorkspaceKey: "TEST", NodeID: "node-1",
		SupportedProviders: []string{"local-noop"}, HeartbeatInterval: -1, ReviewMarker: recordingMarker(&marked, nil),
		Executor: &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted,
			RuntimeMetadata: map[string]string{"attempt_id": "att-worker"}}}}).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(marked) != 1 || len(runs.closeTask) != 1 || runs.closeTask[0] {
		t.Fatalf("marked %+v CloseTask %v, want the task kept in review", marked, runs.closeTask)
	}
}
