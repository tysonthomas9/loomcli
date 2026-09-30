package driver

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTaskRunRetryBackoff(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		want    time.Duration
	}{
		{name: "negative attempt clamps to base", attempt: -3, want: time.Second},
		{name: "attempt zero", attempt: 0, want: time.Second},
		{name: "attempt one", attempt: 1, want: 2 * time.Second},
		{name: "attempt two", attempt: 2, want: 4 * time.Second},
		{name: "attempt three", attempt: 3, want: 8 * time.Second},
		{name: "attempt four", attempt: 4, want: 16 * time.Second},
		{name: "attempt five capped", attempt: 5, want: 30 * time.Second},
		{name: "large attempt capped without overflow", attempt: 63, want: 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskRunRetryBackoff(tc.attempt); got != tc.want {
				t.Fatalf("taskRunRetryBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
			}
		})
	}
}

func TestRetryCarriesPreviousAttemptID(t *testing.T) {
	claimed := &domain.TaskRun{TaskRunID: "run-1", RuntimeMetadata: map[string]string{"scheduler_attempt": "1", "attempt_id": "run-1-a1"}}
	req := taskExecRequest(claimed, executeClaimedTaskRunOptions{}, claimedTaskRunRefs{})
	if req.SchedulerAttempt != 1 || req.PreviousAttemptID != "run-1-a1" || taskCopyAttemptID(req.TaskRunID, req.SchedulerAttempt) != "run-1-a2" {
		t.Fatalf("retry request = %+v", req)
	}
}

func TestResumeCarriesCaptureAttemptID(t *testing.T) {
	claimed := &domain.TaskRun{TaskRunID: "run-2", RuntimeMetadata: map[string]string{"resume_from_attempt_id": "run-1-a1"}}
	req := taskExecRequest(claimed, executeClaimedTaskRunOptions{}, claimedTaskRunRefs{})
	if req.ResumeAttemptID != "run-1-a1" || req.PreviousAttemptID != "" {
		t.Fatalf("resume request = %+v", req)
	}
}

func TestQueuedResumeKeepsCaptureAttempt(t *testing.T) {
	st := memstore.New()
	run, err := createQueuedTaskRun(context.Background(), st, TaskRunRequestOptions{
		WorkspaceKey: "WS", TaskID: "TASK-1", ResumeAttemptID: "old-attempt",
	}, taskRunRequestRefs{TaskRunID: "new-run"})
	if err != nil || run == nil || run.RuntimeMetadata["resume_from_attempt_id"] != "old-attempt" {
		t.Fatalf("queued resume = %+v, %v", run, err)
	}
}

func TestRequeueClaimedTaskRunSetsNextEligibleAt(t *testing.T) {
	ctx := t.Context()
	s := memstore.New()
	if _, err := s.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "WS",
		NodeID:          "node-1",
		RuntimeProvider: domain.RuntimeProviderLocal,
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Hour,
	}); err != nil {
		t.Fatalf("Create node: %v", err)
	}
	if _, err := s.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    "run-1",
		TaskID:       "WS-1",
		Status:       domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	claimed, err := s.TaskRuns().ClaimQueued(ctx, "WS", store.TaskRunClaim{
		TaskRunID: "run-1",
		NodeID:    "node-1",
		LeaseID:   "lease-1",
	})
	if err != nil {
		t.Fatalf("ClaimQueued: %v", err)
	}

	retry := taskRunRetryDecisionResult{Retry: true, Attempt: 2, MaxAttempts: 3}
	before := time.Now().UTC()
	requeued, err := requeueClaimedTaskRun(ctx, s, claimed, executeClaimedTaskRunOptions{}, TaskExecResult{}, taskExecCompletion{
		Status:     domain.TaskRunFailed,
		ErrorClass: "task_failed",
	}, claimed.RuntimeMetadata, retry)
	if err != nil {
		t.Fatalf("requeueClaimedTaskRun: %v", err)
	}
	after := time.Now().UTC()

	if requeued.Status != domain.TaskRunQueued {
		t.Fatalf("requeued status = %q, want queued", requeued.Status)
	}
	wantMin := before.Add(taskRunRetryBackoff(retry.Attempt))
	wantMax := after.Add(taskRunRetryBackoff(retry.Attempt))
	if requeued.NextEligibleAt.Before(wantMin) || requeued.NextEligibleAt.After(wantMax) {
		t.Fatalf("requeued NextEligibleAt = %v, want within [%v, %v]", requeued.NextEligibleAt, wantMin, wantMax)
	}
}
