package driverapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestDriverAPIExecTaskPersistsInputPayload is the HTTP-level proof of the
// closed gap: the exec-task driver op reads the optional `input` body field
// (camelCase driver wire — what driver.js requestTaskRun now sends) and persists
// it verbatim onto the created TaskRun.Input, so the runner that later claims
// the run receives the review diff+rubric.
func TestDriverAPIExecTaskPersistsInputPayload(t *testing.T) {
	h := newTestHarness(t, "")
	registerExecTaskWorkerNode(t, h, "task-node-1", "local-noop")

	// The raw JSON object the workflow passed to loom.taskRuns.request({input}).
	reviewInput := map[string]any{
		"kind":     "github-review",
		"repo":     "octo/hello",
		"prNumber": 7,
		"headSha":  "sha-head",
		"baseRef":  "main",
		"diff":     "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n",
		"rubric":   []string{"clarity", "tests"},
	}

	resp, decoded := h.do(t, opRequest{
		op: "exec-task",
		body: map[string]any{
			"taskId":          "REVIEW-1",
			"taskRunId":       "task-run-review-http",
			"providerProfile": "local-noop",
			"enqueueOnly":     true,
			"input":           reviewInput,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%v)", resp.StatusCode, decoded)
	}
	if decoded["taskRunId"] != "task-run-review-http" {
		t.Fatalf("taskRunId = %v, want task-run-review-http", decoded["taskRunId"])
	}

	stored, err := h.store.TaskRuns().Get(context.Background(), "WS", "task-run-review-http")
	if err != nil {
		t.Fatalf("Get stored task run: %v", err)
	}
	if stored.Status != domain.TaskRunQueued {
		t.Fatalf("stored status = %s, want queued", stored.Status)
	}
	if len(stored.Input) == 0 {
		t.Fatal("stored TaskRun.Input is empty: the exec-task handler dropped the input payload")
	}
	// The persisted Input must be the same JSON object the caller sent — decode
	// and compare structurally (key order on the wire is not significant).
	var gotInput map[string]any
	if err := json.Unmarshal(stored.Input, &gotInput); err != nil {
		t.Fatalf("stored Input is not valid JSON: %v (raw=%s)", err, stored.Input)
	}
	wantInput := map[string]any{}
	wantRaw, _ := json.Marshal(reviewInput)
	_ = json.Unmarshal(wantRaw, &wantInput)
	if gotInput["diff"] != wantInput["diff"] {
		t.Fatalf("stored Input diff = %v, want %v", gotInput["diff"], wantInput["diff"])
	}
	if gotInput["repo"] != "octo/hello" || gotInput["headSha"] != "sha-head" || gotInput["baseRef"] != "main" {
		t.Fatalf("stored Input placement = %+v, want the request fields", gotInput)
	}
	if gotInput["prNumber"] != float64(7) {
		t.Fatalf("stored Input prNumber = %v, want 7", gotInput["prNumber"])
	}
	rubric, ok := gotInput["rubric"].([]any)
	if !ok || len(rubric) != 2 || rubric[0] != "clarity" || rubric[1] != "tests" {
		t.Fatalf("stored Input rubric = %v, want [clarity tests]", gotInput["rubric"])
	}
}

// TestDriverAPIExecTaskOmitsInputWhenAbsent confirms back-compat: an exec-task
// request without `input` persists a TaskRun with no Input (the field stays
// nil), so existing callers behave exactly as before.
func TestDriverAPIExecTaskOmitsInputWhenAbsent(t *testing.T) {
	h := newTestHarness(t, "")
	registerExecTaskWorkerNode(t, h, "task-node-2", "local-noop")

	resp, decoded := h.do(t, opRequest{
		op: "exec-task",
		body: map[string]any{
			"taskId":          "REVIEW-2",
			"taskRunId":       "task-run-no-input",
			"providerProfile": "local-noop",
			"enqueueOnly":     true,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%v)", resp.StatusCode, decoded)
	}

	stored, err := h.store.TaskRuns().Get(context.Background(), "WS", "task-run-no-input")
	if err != nil {
		t.Fatalf("Get stored task run: %v", err)
	}
	if stored.Input != nil {
		t.Fatalf("stored TaskRun.Input = %q, want nil for a request without input", stored.Input)
	}
}

func TestDriverAPIExecTaskPreservesRequestedNodeID(t *testing.T) {
	h := newTestHarness(t, "")
	registerExecTaskWorkerNode(t, h, "task-node-target", "local-noop")
	registerExecTaskWorkerNode(t, h, "task-node-other", "local-noop")

	resp, decoded := h.do(t, opRequest{
		op: "exec-task",
		body: map[string]any{
			"taskId":          "REVIEW-3",
			"taskRunId":       "task-run-target-node",
			"providerProfile": "local-noop",
			"nodeId":          "task-node-target",
			"enqueueOnly":     true,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%v)", resp.StatusCode, decoded)
	}

	stored, err := h.store.TaskRuns().Get(context.Background(), "WS", "task-run-target-node")
	if err != nil {
		t.Fatalf("Get stored task run: %v", err)
	}
	if stored.NodeID != "" || stored.TargetNodeID != "task-node-target" {
		t.Fatalf("queued TaskRun owner=%q target=%q, want empty owner and requested target", stored.NodeID, stored.TargetNodeID)
	}
	if _, err := h.store.TaskRuns().ClaimQueued(context.Background(), "WS", store.TaskRunClaim{
		TaskRunID:          "task-run-target-node",
		NodeID:             "task-node-other",
		LeaseID:            "lease-other",
		SupportedProviders: []string{"local-noop"},
	}); err == nil {
		t.Fatal("wrong node claimed node-pinned queued task run")
	}
	claimed, err := h.store.TaskRuns().ClaimQueued(context.Background(), "WS", store.TaskRunClaim{
		TaskRunID:          "task-run-target-node",
		NodeID:             "task-node-target",
		LeaseID:            "lease-target",
		SupportedProviders: []string{"local-noop"},
	})
	if err != nil {
		t.Fatalf("target node ClaimQueued: %v", err)
	}
	if claimed.NodeID != "task-node-target" || claimed.TargetNodeID != "task-node-target" || claimed.Status != domain.TaskRunRunning {
		t.Fatalf("claimed = %+v, want target node running", claimed)
	}
}

func TestDriverAPIExecTaskPinnedUnschedulableNode(t *testing.T) {
	h := newTestHarness(t, "")
	registerExecTaskWorkerNode(t, h, "task-node-target", "other-provider")
	registerExecTaskWorkerNode(t, h, "task-node-eligible", "local-noop")

	resp, decoded := h.do(t, opRequest{
		op: "exec-task",
		body: map[string]any{
			"taskId":          "REVIEW-4",
			"taskRunId":       "task-run-pinned-unschedulable",
			"providerProfile": "local-noop",
			"nodeId":          "task-node-target",
			"enqueueOnly":     true,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%v)", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "unschedulable" {
		t.Fatalf("error code = %q, want unschedulable", code)
	}
	if _, err := h.store.TaskRuns().Get(context.Background(), "WS", "task-run-pinned-unschedulable"); err == nil {
		t.Fatal("created task run for unschedulable pinned node")
	}
}

// registerExecTaskWorkerNode registers a task-runner node so an enqueue-only
// exec-task is schedulable for the given provider.
func registerExecTaskWorkerNode(t *testing.T, h *testHarness, nodeID, provider string) {
	t.Helper()
	if _, err := h.store.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    "WS",
		NodeID:          nodeID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Capabilities:    []string{"task-runner", provider},
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	}); err != nil {
		t.Fatalf("Create task worker node %s: %v", nodeID, err)
	}
}
