//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// reviewRunnerExecutor stands in for the A1 github-review-agent task runner.
// It proves the closed gap end to end: the diff+rubric the workflow passed to
// loom.taskRuns.request({ input: {...} }) reaches the runner verbatim on
// TaskExecRequest.Input (which the host bridge serializes into
// LOOM_TASK_RUN_REQUEST_JSON), and the runner returns its verdict on
// RuntimeMetadata.review_findings so it flows back to the awaiting caller.
type reviewRunnerExecutor struct {
	sawInput   json.RawMessage
	sawReview  reviewRunnerInput
	decodedErr error
}

type reviewRunnerInput struct {
	Kind     string   `json:"kind"`
	Repo     string   `json:"repo"`
	PRNumber int      `json:"prNumber"`
	HeadSha  string   `json:"headSha"`
	BaseRef  string   `json:"baseRef"`
	Diff     string   `json:"diff"`
	Rubric   []string `json:"rubric"`
}

func (e *reviewRunnerExecutor) ExecuteTask(_ context.Context, req TaskExecRequest) (TaskExecResult, error) {
	e.sawInput = req.Input
	if len(req.Input) > 0 {
		e.decodedErr = json.Unmarshal(req.Input, &e.sawReview)
	}
	// The runner produces review findings keyed off the rubric it received and
	// surfaces them back through runtime metadata, exactly as the A1 agent
	// expects to read off runtimeMetadata.review_findings.
	findings := map[string]any{
		"verdict": "approve",
		"repo":    e.sawReview.Repo,
		"pr":      e.sawReview.PRNumber,
		"checked": e.sawReview.Rubric,
	}
	encoded, _ := json.Marshal(findings)
	return TaskExecResult{
		Status:   domain.TaskRunCompleted,
		ExitCode: 0,
		LogsRef:  "logs://" + req.TaskRunID,
		Logs:     "review completed\n",
		RuntimeMetadata: map[string]string{
			"review_findings": string(encoded),
		},
	}, nil
}

// TestRequestTaskRunDeliversReviewInputAndReturnsFindings is the end-to-end
// proof that the dropped-payload gap is closed: a review diff+rubric handed to
// the request options as Input travels through the real enqueue + claim +
// execute path (memstore), reaches the runner on TaskExecRequest.Input, and the
// runner's review_findings flow back to the awaiting caller — both on the
// returned outcome and on a fresh store read (what taskRuns.get/await observe).
func TestRequestTaskRunDeliversReviewInputAndReturnsFindings(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	registerTaskWorkerNode(t, ctx, st, "node-review", []string{"codex-default"}, nil)

	reviewInput := json.RawMessage(`{"kind":"github-review","repo":"octo/hello","prNumber":7,"headSha":"sha-head","baseRef":"main","diff":"--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n","rubric":["clarity","tests"]}`)
	executor := &reviewRunnerExecutor{}

	outcome, err := RequestTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-review-1",
		TaskID:          "REVIEW-1",
		ProviderProfile: "codex-default",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
		NodeID:          "node-review",
		Input:           reviewInput,
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRunWithResult: %v", err)
	}

	// 1. The runner saw the diff+rubric verbatim on TaskExecRequest.Input.
	if executor.decodedErr != nil {
		t.Fatalf("runner failed to decode review input: %v", executor.decodedErr)
	}
	if !bytes.Equal(executor.sawInput, reviewInput) {
		t.Fatalf("runner Input = %q, want verbatim %q", executor.sawInput, reviewInput)
	}
	if executor.sawReview.Diff != "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n" {
		t.Fatalf("runner diff = %q, want the request diff", executor.sawReview.Diff)
	}
	if executor.sawReview.Repo != "octo/hello" || executor.sawReview.PRNumber != 7 || executor.sawReview.HeadSha != "sha-head" || executor.sawReview.BaseRef != "main" {
		t.Fatalf("runner review fields = %+v, want the request placement", executor.sawReview)
	}
	if len(executor.sawReview.Rubric) != 2 || executor.sawReview.Rubric[0] != "clarity" || executor.sawReview.Rubric[1] != "tests" {
		t.Fatalf("runner rubric = %+v, want [clarity tests]", executor.sawReview.Rubric)
	}

	// 2. The Input is persisted on the created TaskRun (so a claim by a separate
	// worker process would deliver the same payload).
	if !bytes.Equal(outcome.Run.Input, reviewInput) {
		t.Fatalf("persisted TaskRun.Input = %q, want verbatim %q", outcome.Run.Input, reviewInput)
	}

	// 3. review_findings flows back to the awaiting caller on the outcome.
	wantFindings := `{"checked":["clarity","tests"],"pr":7,"repo":"octo/hello","verdict":"approve"}`
	if got := outcome.Run.RuntimeMetadata["review_findings"]; got != wantFindings {
		t.Fatalf("outcome review_findings = %q, want %q", got, wantFindings)
	}

	// 4. And on a fresh store read — what loom.taskRuns.get / await observe.
	stored, err := st.TaskRuns().Get(ctx, "TEST", "task-run-review-1")
	if err != nil {
		t.Fatalf("Get stored task run: %v", err)
	}
	if stored.Status != domain.TaskRunCompleted {
		t.Fatalf("stored status = %s, want completed", stored.Status)
	}
	if got := stored.RuntimeMetadata["review_findings"]; got != wantFindings {
		t.Fatalf("stored review_findings = %q, want %q (must survive the round trip the awaiting caller reads)", got, wantFindings)
	}
	if !bytes.Equal(stored.Input, reviewInput) {
		t.Fatalf("stored TaskRun.Input = %q, want verbatim %q", stored.Input, reviewInput)
	}
}

// TestEnqueueThenClaimDeliversReviewInputToWorker proves the split worker path:
// EnqueueTaskRunWithResult persists Input on the queued run, and a separate
// ClaimAndExecuteTaskRunWithResult (a distinct worker process) delivers that
// same payload to its runner — the diff+rubric is not lost when enqueue and
// execution are different processes.
func TestEnqueueThenClaimDeliversReviewInputToWorker(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	registerTaskWorkerNode(t, ctx, st, "node-review-worker", []string{"local-noop"}, nil)

	reviewInput := json.RawMessage(`{"kind":"github-review","repo":"octo/hello","prNumber":11,"headSha":"sha-x","baseRef":"main","diff":"patch-body","rubric":["security"]}`)

	queuedOutcome, err := EnqueueTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-review-enqueue",
		TaskID:          "REVIEW-2",
		ProviderProfile: "local-noop",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
		Input:           reviewInput,
	}, nil)
	if err != nil {
		t.Fatalf("EnqueueTaskRunWithResult: %v", err)
	}
	if queuedOutcome.Run.Status != domain.TaskRunQueued {
		t.Fatalf("queued status = %s, want queued", queuedOutcome.Run.Status)
	}
	if !bytes.Equal(queuedOutcome.Run.Input, reviewInput) {
		t.Fatalf("queued TaskRun.Input = %q, want verbatim %q", queuedOutcome.Run.Input, reviewInput)
	}

	executor := &reviewRunnerExecutor{}
	finalOutcome, err := ClaimAndExecuteTaskRunWithResult(ctx, st, TaskRunWorkerOptions{
		WorkspaceKey:       "TEST",
		TaskRunID:          "task-run-review-enqueue",
		NodeID:             "node-review-worker",
		RunnerID:           "runner-review",
		SupportedProviders: []string{"local-noop"},
		HeartbeatInterval:  -1,
	}, executor)
	if err != nil {
		t.Fatalf("ClaimAndExecuteTaskRunWithResult: %v", err)
	}

	// The claiming worker's runner received the payload that was enqueued by a
	// separate request, surviving the queue/claim round trip.
	if executor.decodedErr != nil {
		t.Fatalf("worker runner failed to decode review input: %v", executor.decodedErr)
	}
	if !bytes.Equal(executor.sawInput, reviewInput) {
		t.Fatalf("worker runner Input = %q, want verbatim %q", executor.sawInput, reviewInput)
	}
	if executor.sawReview.Diff != "patch-body" || executor.sawReview.PRNumber != 11 {
		t.Fatalf("worker runner review = %+v, want the enqueued diff/pr", executor.sawReview)
	}
	wantFindings := `{"checked":["security"],"pr":11,"repo":"octo/hello","verdict":"approve"}`
	if got := finalOutcome.Run.RuntimeMetadata["review_findings"]; got != wantFindings {
		t.Fatalf("worker review_findings = %q, want %q", got, wantFindings)
	}
}
