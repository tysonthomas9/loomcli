package driverapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// activateWorkflow makes the harness driver startable as a workflow target
// (workflows/start resolves the active passed version).
func activateWorkflow(t *testing.T, h *testHarness) {
	t.Helper()
	active := "version-1"
	if _, err := h.store.Drivers().Update(context.Background(), "WS", "driver-1",
		store.DriverUpdate{ActiveVersionID: &active}); err != nil {
		t.Fatalf("activate driver version: %v", err)
	}
}

func startBody(workflowName, idempotencyKey string) map[string]any {
	body := map[string]any{"workflowName": workflowName}
	if idempotencyKey != "" {
		body["idempotencyKey"] = idempotencyKey
	}
	return body
}

func TestDriverAPIWorkflowsStartIdempotent(t *testing.T) {
	h := newTestHarness(t, "")
	activateWorkflow(t, h)
	body := startBody("driver-1", "deploy")
	body["input"] = map[string]any{"target": "staging"}

	resp, decoded := h.do(t, opRequest{op: "workflows/start", headers: h.ownerHeaders(), body: body})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	wantChild := driverpkg.ChildWorkflowRunID(h.runID, "deploy")
	if decoded["childRunId"] != wantChild || decoded["status"] != string(domain.DriverRunQueued) ||
		decoded["parentRunId"] != h.runID || decoded["workflowName"] != "driver-1" {
		t.Fatalf("response = %v, want queued child %q of %q", decoded, wantChild, h.runID)
	}

	// The replayed start returns the same child, and exactly one child run
	// exists.
	resp, replay := h.do(t, opRequest{op: "workflows/start", headers: h.ownerHeaders(), body: body})
	if resp.StatusCode != http.StatusOK || replay["childRunId"] != wantChild {
		t.Fatalf("replay = %d %v, want same child %q", resp.StatusCode, replay, wantChild)
	}
	child, err := h.store.DriverRuns().Get(context.Background(), "WS", wantChild)
	if err != nil || child.ParentRunID != h.runID {
		t.Fatalf("child = %+v, %v; want persisted child of %s", child, err, h.runID)
	}
	if string(child.Payload) != `{"target":"staging"}` {
		t.Fatalf("child payload = %s, want start input", child.Payload)
	}
}

func TestDriverAPIWorkflowsStartValidation(t *testing.T) {
	cases := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{"missing workflow name", map[string]any{"idempotencyKey": "k"}, http.StatusBadRequest, "invalid"},
		{"missing key and index", startBody("driver-1", ""), http.StatusBadRequest, "invalid"},
		{"unknown workflow", startBody("missing-wf", "k"), http.StatusNotFound, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t, "")
			activateWorkflow(t, h)
			resp, decoded := h.do(t, opRequest{op: "workflows/start", headers: h.ownerHeaders(), body: tc.body})
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d (%v), want %d", resp.StatusCode, decoded, tc.wantStatus)
			}
			if code := errorCode(t, decoded); code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

// TestDriverAPIWorkflowsStartDepthCap drives the composition depth refusal
// over the wire: with the env cap at 1 the root may start a child, but the
// (claimed) child may not start a grandchild.
func TestDriverAPIWorkflowsStartDepthCap(t *testing.T) {
	t.Setenv(driverpkg.CompositionMaxDepthEnvVar, "1")
	h := newTestHarness(t, "")
	activateWorkflow(t, h)
	ctx := context.Background()

	resp, decoded := h.do(t, opRequest{op: "workflows/start", headers: h.ownerHeaders(), body: startBody("driver-1", "depth1")})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("depth-1 start = %d (%v), want 200", resp.StatusCode, decoded)
	}
	childID, _ := decoded["childRunId"].(string)
	claimed, err := h.store.DriverRuns().Claim(ctx, "WS", childID, "node-c", "lease-c")
	if err != nil {
		t.Fatalf("claim child: %v", err)
	}

	resp, decoded = h.do(t, opRequest{op: "workflows/start", headers: runHeaders(claimed), body: startBody("driver-1", "depth2")})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("depth-2 start = %d (%v), want 400", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != domain.CompositionErrCodeDepthExceeded {
		t.Fatalf("error code = %q, want %q", code, domain.CompositionErrCodeDepthExceeded)
	}
}

// runHeaders builds owner identity headers for an arbitrary claimed run.
func runHeaders(run *domain.DriverRun) map[string]string {
	return map[string]string{
		HeaderDriverRunID:        run.RunID,
		HeaderDriverNodeID:       run.NodeID,
		HeaderDriverLeaseID:      run.LeaseID,
		HeaderDriverLeaseToken:   "driver-test-token",
		HeaderDriverFencingToken: fmt.Sprintf("%d", run.FencingToken),
	}
}

func TestDriverAPIWorkflowsAwaitValidation(t *testing.T) {
	h := newTestHarness(t, "")
	activateWorkflow(t, h)

	resp, decoded := h.do(t, opRequest{op: "workflows/await", headers: h.ownerHeaders(),
		body: map[string]any{"timeoutMs": 60_000, "awaitIndex": 1}})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
		t.Fatalf("missing child = %d %v, want 400 invalid", resp.StatusCode, decoded)
	}
	resp, decoded = h.do(t, opRequest{op: "workflows/await", headers: h.ownerHeaders(),
		body: map[string]any{"childRunId": "run-nope", "timeoutMs": 60_000, "awaitIndex": 1}})
	if resp.StatusCode != http.StatusNotFound || errorCode(t, decoded) != "not_found" {
		t.Fatalf("unknown child = %d %v, want 404 not_found", resp.StatusCode, decoded)
	}

	// RULE 5 / RULE 3 still apply to composition awaits: a real child with a
	// missing timeout or awaitIndex is rejected with the structured codes.
	child := startChildViaAPI(t, h, "await-validation")
	resp, decoded = h.do(t, opRequest{op: "workflows/await", headers: h.ownerHeaders(),
		body: map[string]any{"childRunId": child, "awaitIndex": 1}})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != domain.AwaitErrCodeTimeoutRequired {
		t.Fatalf("missing timeout = %d %v, want 400 %s", resp.StatusCode, decoded, domain.AwaitErrCodeTimeoutRequired)
	}
	resp, decoded = h.do(t, opRequest{op: "workflows/await", headers: h.ownerHeaders(),
		body: map[string]any{"childRunId": child, "timeoutMs": 60_000}})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != domain.AwaitErrCodeInstanceKeyMalformed {
		t.Fatalf("missing awaitIndex = %d %v, want 400 %s", resp.StatusCode, decoded, domain.AwaitErrCodeInstanceKeyMalformed)
	}
}

func TestDriverAPIWorkflowsAwaitRejectsNonChild(t *testing.T) {
	h := newTestHarness(t, "")
	activateWorkflow(t, h)
	if _, err := h.store.DriverRuns().Create(context.Background(), store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-detached", DriverID: "driver-1", DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("create detached run: %v", err)
	}

	resp, decoded := h.do(t, opRequest{op: "workflows/await", headers: h.ownerHeaders(),
		body: map[string]any{"childRunId": "run-detached", "timeoutMs": 60_000, "awaitIndex": 1}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d (%v), want 403", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}
}

// startChildViaAPI starts a child over the wire and returns its run ID.
func startChildViaAPI(t *testing.T, h *testHarness, key string) string {
	t.Helper()
	resp, decoded := h.do(t, opRequest{op: "workflows/start", headers: h.ownerHeaders(), body: startBody("driver-1", key)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workflows/start = %d (%v), want 200", resp.StatusCode, decoded)
	}
	childID, _ := decoded["childRunId"].(string)
	if childID == "" {
		t.Fatalf("workflows/start response = %v, want childRunId", decoded)
	}
	return childID
}

func TestDriverAPIWorkflowsAwaitSuspendsParent(t *testing.T) {
	h := newTestHarness(t, "")
	activateWorkflow(t, h)
	childID := startChildViaAPI(t, h, "slow")

	resp, decoded := h.do(t, opRequest{op: "workflows/await", headers: h.ownerHeaders(),
		body: map[string]any{"childRunId": childID, "timeoutMs": 60_000, "awaitIndex": 1}})
	if resp.StatusCode != http.StatusOK || decoded["status"] != driverpkg.AwaitOutcomeSuspended {
		t.Fatalf("await = %d %v, want suspended", resp.StatusCode, decoded)
	}
	if _, hasChild := decoded["child"]; hasChild {
		t.Fatalf("response = %v, want no child outcome while suspended", decoded)
	}
	parent, err := h.store.DriverRuns().Get(context.Background(), "WS", h.runID)
	if err != nil || parent.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("parent = %+v, %v; want suspended_awaiting_event", parent, err)
	}
}

// TestDriverAPIWorkflowsAwaitTerminalChildInline runs the child to a real
// terminal state through the executor (a missing bundle fails verification),
// then awaits it: the journaled run.finished resolves inline and the response
// carries the child's terminal outcome.
func TestDriverAPIWorkflowsAwaitTerminalChildInline(t *testing.T) {
	h := newTestHarness(t, "")
	activateWorkflow(t, h)
	ctx := context.Background()
	childID := startChildViaAPI(t, h, "fast")

	driverRuns := testDriverRunExecution{DriverRunAPI: h.execution.DriverRunAPI(), store: h.store}
	if _, err := (&driverpkg.Executor{
		Store: h.store, WorkspaceKey: "WS", RunID: childID,
		WorkDir: t.TempDir(), HeartbeatInterval: -1,
		Execution: driverRuns, RunOutcomeQueue: h.execution.DriverRunOutcomeAPI(),
		ExecutionWorkers:     h.execution.TaskRunWorkerAPI(),
		ExecutionAuthorities: h.execution.DriverRunAuthorityResolver(), SystemAuthorities: h.execution.SystemAuthorityResolver(),
	}).RunOnce(ctx); err != nil {
		t.Fatalf("run child to terminal state: %v", err)
	}
	child, err := h.store.DriverRuns().Get(ctx, "WS", childID)
	if err != nil || !child.Status.IsTerminal() {
		t.Fatalf("child = %+v, %v; want terminal", child, err)
	}

	resp, decoded := h.do(t, opRequest{op: "workflows/await", headers: h.ownerHeaders(),
		body: map[string]any{"childRunId": childID, "timeoutMs": 60_000, "awaitIndex": 1}})
	if resp.StatusCode != http.StatusOK || decoded["status"] != string(domain.AwaitSatisfied) {
		t.Fatalf("await = %d %v, want satisfied inline", resp.StatusCode, decoded)
	}
	event, _ := decoded["event"].(map[string]any)
	if event == nil || event["id"] != driverpkg.RunFinishedEventID(childID, child.Status) {
		t.Fatalf("event = %v, want the child's run.finished", event)
	}
	got, _ := decoded["child"].(map[string]any)
	if got == nil || got["runId"] != childID || got["status"] != string(child.Status) ||
		got["errorClass"] != child.ErrorClass {
		t.Fatalf("child outcome = %v, want %s %s/%s", got, childID, child.Status, child.ErrorClass)
	}
	// The await consumed a normal awaitIndex slot and the parent never
	// suspended.
	parent, err := h.store.DriverRuns().Get(ctx, "WS", h.runID)
	if err != nil || parent.Status != domain.DriverRunRunning {
		t.Fatalf("parent = %+v, %v; want still running", parent, err)
	}
	if decoded["instanceKey"] != domain.AwaitInstanceKey(h.runID, 1) {
		t.Fatalf("instanceKey = %v, want %s", decoded["instanceKey"], domain.AwaitInstanceKey(h.runID, 1))
	}
}
