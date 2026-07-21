package memstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTaskRunTerminalConvergenceCheckpointPaginationReplayAndConcurrentUpgrade(t *testing.T) {
	ctx := context.Background()
	st := New()
	checkpoints := st.TaskRuns().(store.TaskRunTerminalConvergenceStore)
	finishedAt := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	for _, id := range []string{"task-a", "task-b"} {
		seedTerminalConvergenceTaskRun(t, ctx, st, id, finishedAt)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-running", TaskID: "TASK-running",
		Status: domain.TaskRunRunning, NodeID: "node", LeaseID: "lease", LeaseToken: "token", FencingToken: 7,
	}); err != nil {
		t.Fatal(err)
	}

	first, err := checkpoints.ListTaskRunTerminalConvergenceCandidates(ctx, store.TaskRunTerminalConvergenceQuery{
		WorkspaceKey: "WS", RequiredVersion: 1, Limit: 1,
	})
	if err != nil || len(first.TaskRunIDs) != 1 || first.TaskRunIDs[0] != "task-a" || first.Next != "task-a" {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	second, err := checkpoints.ListTaskRunTerminalConvergenceCandidates(ctx, store.TaskRunTerminalConvergenceQuery{
		WorkspaceKey: "WS", RequiredVersion: 1, After: first.Next, Limit: 1,
	})
	if err != nil || len(second.TaskRunIDs) != 1 || second.TaskRunIDs[0] != "task-b" || second.Next != "" {
		t.Fatalf("second page=%+v err=%v", second, err)
	}

	completed, err := checkpoints.CompleteTaskRunTerminalConvergence(ctx, store.TaskRunTerminalConvergenceComplete{
		WorkspaceKey: "WS", TaskRunID: "task-a", RequiredVersion: 1, CompletedAt: finishedAt.Add(time.Minute),
	})
	if err != nil || completed.Replayed || completed.TaskRun.TerminalConvergenceVersion != 1 || completed.TaskRun.TerminalConvergedAt == nil {
		t.Fatalf("completion=%+v err=%v", completed, err)
	}
	replay, err := checkpoints.CompleteTaskRunTerminalConvergence(ctx, store.TaskRunTerminalConvergenceComplete{
		WorkspaceKey: "WS", TaskRunID: "task-a", RequiredVersion: 1, CompletedAt: finishedAt.Add(2 * time.Minute),
	})
	if err != nil || !replay.Replayed || !replay.TaskRun.TerminalConvergedAt.Equal(finishedAt.Add(time.Minute)) {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for index := 0; index < 24; index++ {
		version := 1
		if index%2 == 0 {
			version = 2
		}
		wg.Add(1)
		go func(index, version int) {
			defer wg.Done()
			_, commandErr := checkpoints.CompleteTaskRunTerminalConvergence(ctx, store.TaskRunTerminalConvergenceComplete{
				WorkspaceKey: "WS", TaskRunID: "task-a", RequiredVersion: version,
				CompletedAt: finishedAt.Add(time.Duration(index+3) * time.Minute),
			})
			if commandErr != nil {
				errs <- commandErr
			}
		}(index, version)
	}
	wg.Wait()
	close(errs)
	for commandErr := range errs {
		t.Fatalf("concurrent completion: %v", commandErr)
	}
	final, err := st.TaskRuns().Get(ctx, "WS", "task-a")
	if err != nil || final.TerminalConvergenceVersion != 2 || final.TerminalConvergedAt == nil {
		t.Fatalf("final=%+v err=%v", final, err)
	}
	upgrade, err := checkpoints.ListTaskRunTerminalConvergenceCandidates(ctx, store.TaskRunTerminalConvergenceQuery{
		WorkspaceKey: "WS", RequiredVersion: 3, Limit: 10,
	})
	if err != nil || len(upgrade.TaskRunIDs) != 2 {
		t.Fatalf("upgrade candidates=%+v err=%v", upgrade, err)
	}
}

func seedTerminalConvergenceTaskRun(t *testing.T, ctx context.Context, st *Store, id string, finishedAt time.Time) {
	t.Helper()
	token := "token-" + id
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: id, TaskID: "TASK-" + id,
		Status: domain.TaskRunRunning, NodeID: "node", LeaseID: "lease", LeaseToken: token, FencingToken: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TaskRuns().Finish(ctx, "WS", id, store.TaskRunFinish{
		NodeID: "node", LeaseID: "lease", LeaseToken: token, FencingToken: 7,
		Status: domain.TaskRunCompleted, FinishedAt: finishedAt,
	}); err != nil {
		t.Fatal(fmt.Errorf("finish %s: %w", id, err))
	}
}
