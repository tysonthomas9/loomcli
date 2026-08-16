package taskrunapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

type executionClaimPortStub struct{}

func (executionClaimPortStub) ReplayTaskRunRequest(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (executionClaimPortStub) RequestTaskRun(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (executionClaimPortStub) ClaimTaskRun(context.Context, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (executionClaimPortStub) UpdateTaskRunWorkItemDesign(context.Context, execution.UpdateTaskRunWorkItemDesignCommand) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrUnavailable
}

func (executionClaimPortStub) RequeueTaskRun(context.Context, execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	return execution.RequeueTaskRunResult{}, execution.ErrUnavailable
}

func (executionClaimPortStub) ExhaustTaskRunRetries(context.Context, execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	return execution.ExhaustTaskRunRetriesResult{}, execution.ErrUnavailable
}

type taskRunWorkItemDesignPortStub struct {
	mu       sync.Mutex
	expected execution.Owner
	calls    []execution.UpdateTaskRunWorkItemDesignCommand
	applied  int
}

func (stub *taskRunWorkItemDesignPortStub) UpdateTaskRunWorkItemDesign(
	_ context.Context,
	command execution.UpdateTaskRunWorkItemDesignCommand,
) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.calls = append(stub.calls, command)
	if command.WorkspaceKey != "WS" || command.Owner != stub.expected {
		return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrFenceConflict
	}
	stub.applied++
	return execution.UpdateTaskRunWorkItemDesignResult{
		WorkItemID: "TASK-1",
		ActionID:   "task-run-work-item-design-update:" + command.RequestID,
	}, nil
}

func (stub *taskRunWorkItemDesignPortStub) snapshot() (int, int, execution.UpdateTaskRunWorkItemDesignCommand) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	var last execution.UpdateTaskRunWorkItemDesignCommand
	if len(stub.calls) > 0 {
		last = stub.calls[len(stub.calls)-1]
	}
	return len(stub.calls), stub.applied, last
}

func executionDependenciesForTaskRunAPITest(
	t *testing.T,
	st *memstore.Store,
	designPorts ...execution.TaskRunWorkItemDesignPort,
) appserve.ExecutionDependencies {
	t.Helper()
	mutations := testutil.TaskRunMutationAdapter{TaskRuns: st.TaskRuns()}
	var designs execution.TaskRunWorkItemDesignPort = executionClaimPortStub{}
	if len(designPorts) > 0 && designPorts[0] != nil {
		designs = designPorts[0]
	}
	repairs, ok := st.DriverSteps().(execution.TerminalDriverStepRepairStore)
	if !ok {
		t.Fatal("test DriverStep store lacks terminal repair support")
	}
	return appserve.ExecutionDependencies{
		TaskRuns: st.TaskRuns(), DriverRuns: st.DriverRuns(), DriverSteps: st.DriverSteps(),
		TerminalStepRepairs: repairs, TaskRunEvents: st.TaskRunEvents(), Nodes: st.Nodes(),
		WorkerProfiles: st.WorkerProfiles(), AgentQueries: testutil.StaticAgentQueries{}, Outbox: st.Outbox(), Awaits: st.Awaits(),
		TriggerEvents: st.TriggerEvents(), Workspaces: st.Workspaces(),
		AtomicTaskRunRequests: executionClaimPortStub{}, AtomicTaskRunClaims: executionClaimPortStub{},
		AtomicTaskRunWorkItemDesign: designs,
		AtomicTaskRunRequeues:       executionClaimPortStub{}, AtomicTaskRunRetryExhaustion: executionClaimPortStub{},
		StaleChildTaskRunRecovery: testutil.StaticTaskRunRecoveryPort{},
		TaskRunHeartbeats:         mutations, TaskRunLogs: mutations, TaskRunFinalizer: mutations,
	}
}

type fakeWorkItemQueries struct {
	task    *workitems.IssueDetail
	issueID string
}

func (f *fakeWorkItemQueries) Get(_ context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	f.issueID = query.IssueID
	return f.task, nil
}

type testHarness struct {
	server    *httptest.Server
	store     *memstore.Store
	workItems *fakeWorkItemQueries
	designs   *taskRunWorkItemDesignPortStub
	module    *Module

	taskRunID string
	nodeID    string
	leaseID   string
	token     string
	fence     int64
}

func newHarness(t *testing.T) *testHarness {
	return newHarnessWithRunner(t, "")
}

func newHarnessWithRunner(t *testing.T, runner string) *testHarness {
	t.Helper()
	st := memstore.New()
	h := &testHarness{
		store:     st,
		workItems: &fakeWorkItemQueries{task: &workitems.IssueDetail{ID: "TASK-1", Title: "Do the work"}},
		taskRunID: "task-run-1",
		nodeID:    "node-1",
		leaseID:   "lease-1",
		token:     "lease-token-1",
		fence:     42,
	}
	h.designs = &taskRunWorkItemDesignPortStub{expected: execution.Owner{
		ResourceKind: execution.ResourceTaskRun,
		ResourceID:   h.taskRunID,
		NodeID:       h.nodeID,
		LeaseID:      h.leaseID,
		LeaseToken:   h.token,
		FencingToken: h.fence,
	}}
	if _, err := st.TaskRuns().Create(context.Background(), execution.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    h.taskRunID,
		TaskID:       "TASK-1",
		Runner:       runner,
		Status:       execution.TaskRunRecordRunning,
		NodeID:       h.nodeID,
		LeaseID:      h.leaseID,
		LeaseToken:   h.token,
		FencingToken: h.fence,
	}); err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	executionCapability, err := appserve.NewExecutionCapability(executionDependenciesForTaskRunAPITest(t, st, h.designs))
	if err != nil {
		t.Fatalf("compose Execution capability: %v", err)
	}
	module := NewModule(Config{
		TaskRuns:    executionCapability.TaskRunQueries(),
		Execution:   executionCapability.TaskRunAPI(),
		Authorities: executionCapability.TaskRunAuthorityResolver(),
		WorkItems:   h.workItems,
	})
	module.artifacts = newTaskRunArtifactAPIForTest(module, st)
	h.module = module
	mux := http.NewServeMux()
	module.Register(mux)
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

// identity is the header tuple a request authenticates with; the zero-value
// fields default to the harness's valid lease.
type identity struct {
	taskRunID string
	nodeID    string
	leaseID   string
	token     string
	fence     string
	noAuth    bool
}

func (h *testHarness) apply(req *http.Request, id identity) {
	if !id.noAuth {
		token := id.token
		if token == "" {
			token = h.token
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	setIfNonEmpty := func(header, explicit, fallback string) {
		value := explicit
		if value == "" {
			value = fallback
		}
		if value != "-" { // "-" means deliberately omit
			req.Header.Set(header, value)
		}
	}
	setIfNonEmpty(HeaderTaskRunID, id.taskRunID, h.taskRunID)
	setIfNonEmpty(HeaderTaskRunNodeID, id.nodeID, h.nodeID)
	setIfNonEmpty(HeaderTaskRunLeaseID, id.leaseID, h.leaseID)
	setIfNonEmpty(HeaderTaskRunFencingToken, id.fence, strconv.FormatInt(h.fence, 10))
}

func (h *testHarness) postOp(t *testing.T, op string, body any, id identity) (*http.Response, map[string]any) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal op body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/WS/task-run/"+op, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	h.apply(req, id)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", op, err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode %s response: %v", op, err)
	}
	return resp, decoded
}

func errorCode(t *testing.T, decoded map[string]any) string {
	t.Helper()
	errObj, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response missing error envelope: %v", decoded)
	}
	code, _ := errObj["code"].(string)
	return code
}

func TestTaskRunOpAuthRejections(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name     string
		id       identity
		wantCode string
	}{
		{"missing bearer token", identity{noAuth: true}, "unauthenticated"},
		{"missing task run header", identity{taskRunID: "-"}, "unauthenticated"},
		{"missing fencing header", identity{fence: "-"}, "unauthenticated"},
		{"non-numeric fencing header", identity{fence: "nope"}, "unauthenticated"},
		// Stale/foreign lease material fails the fenced store verification.
		// The lease TOKEN itself is verified by fleet-db's hash check on the
		// same call in production; memstore enforces the fenced tuple.
		{"stale fencing token", identity{fence: "41"}, "lease_denied"},
		{"superseded lease", identity{leaseID: "lease-2"}, "lease_denied"},
		{"foreign node", identity{nodeID: "node-2"}, "lease_denied"},
		{"unknown task run", identity{taskRunID: "task-run-404"}, "lease_denied"},
		{"wrong lease token", identity{token: "wrong-token"}, "lease_denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, decoded := h.postOp(t, "get", map[string]any{}, tt.id)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			if code := errorCode(t, decoded); code != tt.wantCode {
				t.Fatalf("error code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func TestTaskRunOpUnknownOp(t *testing.T) {
	h := newHarness(t)
	resp, decoded := h.postOp(t, "nuke-workspace", map[string]any{}, identity{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unknown_op" {
		t.Fatalf("error code = %q, want unknown_op", code)
	}
}

func TestTaskRunGetAndTaskGet(t *testing.T) {
	h := newHarness(t)
	resp, run := h.postOp(t, "get", map[string]any{}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d: %v", resp.StatusCode, run)
	}
	if run["taskRunId"] != "task-run-1" || run["taskId"] != "TASK-1" || run["status"] != "running" {
		t.Fatalf("get result = %v, want camelCase running task run", run)
	}
	if _, ok := run["leaseToken"]; ok {
		t.Fatalf("get result leaks leaseToken: %v", run)
	}

	resp, out := h.postOp(t, "task-get", map[string]any{}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("task-get status = %d: %v", resp.StatusCode, out)
	}
	task, ok := out["task"].(map[string]any)
	if !ok || task["id"] != "TASK-1" {
		t.Fatalf("task-get task = %v, want TASK-1", out["task"])
	}
	if taskRun, ok := out["taskRun"].(map[string]any); !ok || taskRun["taskRunId"] != "task-run-1" {
		t.Fatalf("task-get taskRun = %v", out["taskRun"])
	}
	if h.workItems.issueID != "TASK-1" {
		t.Fatalf("Work Items query = %q, want TASK-1", h.workItems.issueID)
	}

	resp, out = h.postOp(t, "task-get", map[string]any{"taskId": "TASK-2"}, identity{})
	if resp.StatusCode != http.StatusForbidden || errorCode(t, out) != "not_owner" {
		t.Fatalf("foreign task-get = %d %v, want 403 not_owner", resp.StatusCode, out)
	}
}

func TestTaskRunDesignUpdateIsExactTaskAndFieldRestricted(t *testing.T) {
	h := newHarness(t)
	design := "# Plan\n\nShip it."
	resp, out := h.postOp(t, "task-design-update", map[string]any{
		"requestId": "design-1", "design": design, "designFormat": "markdown",
	}, identity{})
	if resp.StatusCode != http.StatusOK || out["taskId"] != "TASK-1" ||
		out["actionId"] != "task-run-work-item-design-update:design-1" || out["replayed"] != false {
		t.Fatalf("design update = %d %v, want TASK-1 success", resp.StatusCode, out)
	}
	calls, applied, command := h.designs.snapshot()
	if calls != 1 || applied != 1 || command.RequestID != "design-1" || command.Owner.ResourceID != h.taskRunID ||
		command.Design == nil || *command.Design != design || command.DesignFormat == nil || *command.DesignFormat != "markdown" {
		t.Fatalf("design command = %+v calls=%d applied=%d", command, calls, applied)
	}

	tests := []struct {
		name         string
		body         map[string]any
		id           identity
		wantStatus   int
		wantCode     string
		wantPortCall bool
	}{
		{
			name: "caller task ID rejected", body: map[string]any{"requestId": "design-foreign", "taskId": "TASK-2", "design": "nope"},
			wantStatus: http.StatusBadRequest, wantCode: "invalid",
		},
		{
			name: "unknown status field", body: map[string]any{"requestId": "design-field", "design": "nope", "status": "closed"},
			wantStatus: http.StatusBadRequest, wantCode: "invalid",
		},
		{
			name: "missing request ID", body: map[string]any{"design": "nope"},
			wantStatus: http.StatusBadRequest, wantCode: "invalid",
		},
		{
			name: "missing design", body: map[string]any{"requestId": "design-missing", "designFormat": "markdown"},
			wantStatus: http.StatusBadRequest, wantCode: "invalid",
		},
		{
			name: "blank design", body: map[string]any{"requestId": "design-blank", "design": "  "},
			wantStatus: http.StatusBadRequest, wantCode: "invalid",
		},
		{
			name: "invalid format", body: map[string]any{"requestId": "design-format", "design": "nope", "designFormat": "plaintext"},
			wantStatus: http.StatusBadRequest, wantCode: "invalid",
		},
		{
			name: "expired lease", body: map[string]any{"requestId": "design-stale", "design": "nope"}, id: identity{fence: "41"},
			wantStatus: http.StatusForbidden, wantCode: "not_owner", wantPortCall: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeCalls, beforeApplied, _ := h.designs.snapshot()
			resp, decoded := h.postOp(t, "task-design-update", tt.body, tt.id)
			if resp.StatusCode != tt.wantStatus || errorCode(t, decoded) != tt.wantCode {
				t.Fatalf("response = %d %v, want %d %s", resp.StatusCode, decoded, tt.wantStatus, tt.wantCode)
			}
			afterCalls, afterApplied, _ := h.designs.snapshot()
			wantCalls := beforeCalls
			if tt.wantPortCall {
				wantCalls++
			}
			if afterCalls != wantCalls || afterApplied != beforeApplied {
				t.Fatalf("rejected request calls=%d applied=%d, want calls=%d applied=%d", afterCalls, afterApplied, wantCalls, beforeApplied)
			}
		})
	}
}

func TestTaskRunHeartbeatAndLogs(t *testing.T) {
	h := newHarness(t)
	resp, run := h.postOp(t, "heartbeat", map[string]any{
		"runtimeMetadata": map[string]string{"phase": "starting"},
		"logsRef":         "logs://task-run-1",
	}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status = %d: %v", resp.StatusCode, run)
	}
	if run["logsRef"] != "logs://task-run-1" {
		t.Fatalf("heartbeat result = %v, want logsRef applied", run)
	}
	stored, err := h.store.TaskRuns().Get(context.Background(), "WS", h.taskRunID)
	if err != nil {
		t.Fatalf("get stored run: %v", err)
	}
	if stored.RuntimeMetadata["phase"] != "starting" {
		t.Fatalf("stored runtime metadata = %v", stored.RuntimeMetadata)
	}

	logTimestamp := time.Date(2026, 7, 16, 20, 15, 0, 0, time.UTC)
	resp, entry := h.postOp(t, "log-append", map[string]any{
		"request_id": "task-run-log-1", "stream": "stdout", "text": "hello\n", "timestamp": logTimestamp,
	}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("log-append status = %d: %v", resp.StatusCode, entry)
	}
	if entry["taskRunId"] != "task-run-1" || entry["text"] != "hello\n" || entry["sequence"] != float64(1) {
		t.Fatalf("log-append result = %v", entry)
	}
	resp, replayed := h.postOp(t, "log-append", map[string]any{
		"requestId": "task-run-log-1", "stream": "stdout", "text": "hello\n", "timestamp": logTimestamp,
	}, identity{})
	if resp.StatusCode != http.StatusOK || replayed["sequence"] != entry["sequence"] {
		t.Fatalf("log-append replay = %d %v, want committed sequence %v", resp.StatusCode, replayed, entry["sequence"])
	}
	logs, err := h.store.TaskRuns().ListLogs(context.Background(), "WS", h.taskRunID, execution.TaskRunLogFilter{})
	if err != nil || len(logs) != 1 || logs[0].Text != "hello\n" {
		t.Fatalf("stored logs = %v err=%v, want the appended line", logs, err)
	}

	resp, decoded := h.postOp(t, "log-append", map[string]any{
		"requestId": "task-run-log-1", "stream": "stdout", "text": "different\n", "timestamp": logTimestamp,
	}, identity{})
	if resp.StatusCode != http.StatusConflict || errorCode(t, decoded) != "conflict" {
		t.Fatalf("conflicting log replay = %d %v, want 409 conflict", resp.StatusCode, decoded)
	}

	resp, decoded = h.postOp(t, "log-append", map[string]any{
		"requestId": "camel", "request_id": "snake", "text": "line\n", "timestamp": logTimestamp,
	}, identity{})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
		t.Fatalf("disagreeing request aliases = %d %v, want 400 invalid", resp.StatusCode, decoded)
	}

	resp, decoded = h.postOp(t, "log-append", map[string]any{"stream": "stdout"}, identity{})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
		t.Fatalf("log-append without text = %d %v, want 400 invalid", resp.StatusCode, decoded)
	}
	resp, decoded = h.postOp(t, "log-append", map[string]any{"stream": "stdout", "text": "missing identity\n"}, identity{})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
		t.Fatalf("log-append without replay identity = %d %v, want 400 invalid", resp.StatusCode, decoded)
	}
}

func TestTaskRunRuntimeCredentialOperationIsNotRegistered(t *testing.T) {
	h := newHarnessWithRunner(t, "daytona-task-runner")
	resp, decoded := h.postOp(t, "runtime-credential", map[string]any{
		"provider": "daytona",
		"value":    "credential-sentinel",
	}, identity{})
	if resp.StatusCode != http.StatusNotFound || errorCode(t, decoded) != "unknown_op" {
		t.Fatalf("runtime credential operation = %d %v, want 404 unknown_op", resp.StatusCode, decoded)
	}
	if strings.Contains(fmt.Sprint(decoded), "credential-sentinel") {
		t.Fatalf("runtime credential response exposed request material: %v", decoded)
	}
}

func TestTaskRunArtifactLifecycle(t *testing.T) {
	h := newHarness(t)
	resp, artifact := h.postOp(t, "artifact-declare", map[string]any{
		"artifactId":  "artifact-1",
		"sessionId":   "session-1",
		"taskId":      "TASK-1",
		"type":        "patch",
		"uri":         "artifact://artifact-1",
		"checksum":    "sha256:checksum",
		"contentHash": "sha256:declared",
		"sizeBytes":   10,
		"metadata":    map[string]string{"idempotency_key": "artifact-key"},
		// Owner spoofing attempts are ignored: the server force-scopes
		// ownership to the verified task run.
		"ownerType": "agent",
		"ownerId":   "someone-else",
	}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact-declare status = %d: %v", resp.StatusCode, artifact)
	}
	if artifact["artifactId"] != "artifact-1" || artifact["ownerType"] != "task_run" || artifact["ownerId"] != "task-run-1" {
		t.Fatalf("declared artifact = %v, want task-run ownership forced", artifact)
	}
	if artifact["sessionId"] != "session-1" || artifact["taskId"] != "TASK-1" ||
		artifact["uri"] != "artifact://artifact-1" || artifact["sizeBytes"] != float64(10) ||
		artifact["checksum"] != "sha256:checksum" || artifact["contentHash"] != "sha256:declared" {
		t.Fatalf("declared artifact lost semantic create fields: %v", artifact)
	}
	if artifact["durableStatus"] != "declared" {
		t.Fatalf("declared artifact status = %v", artifact["durableStatus"])
	}
	persisted, err := h.store.ArtifactQueries().GetArtifactRecord(context.Background(), "WS", "artifact-1")
	if err != nil {
		t.Fatalf("get declared artifact: %v", err)
	}
	if persisted.SessionID != "session-1" || persisted.TaskID != "TASK-1" || persisted.URI != "artifact://artifact-1" ||
		persisted.SizeBytes != 10 || persisted.Checksum != "sha256:checksum" || persisted.ContentHash != "sha256:declared" {
		t.Fatalf("persisted declaration lost semantic fields: %#v", persisted)
	}

	// Raw content upload.
	req, err := http.NewRequest(http.MethodPut, h.server.URL+"/api/workspaces/WS/task-run/artifacts/artifact-1/content", strings.NewReader("patch body"))
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	req.Header.Set("Content-Type", "text/x-diff")
	h.apply(req, identity{})
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload content: %v", err)
	}
	var uploaded map[string]any
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK || uploaded["artifactId"] != "artifact-1" {
		t.Fatalf("upload = %d %v", uploadResp.StatusCode, uploaded)
	}

	resp, finalized := h.postOp(t, "artifact-finalize", map[string]any{
		"artifactId":  "artifact-1",
		"contentHash": uploaded["contentHash"],
	}, identity{})
	if resp.StatusCode != http.StatusOK || finalized["durableStatus"] != "finalized" {
		t.Fatalf("artifact-finalize = %d %v", resp.StatusCode, finalized)
	}

	resp, got := h.postOp(t, "artifact-get", map[string]any{"artifactId": "artifact-1"}, identity{})
	if resp.StatusCode != http.StatusOK || got["durableStatus"] != "finalized" {
		t.Fatalf("artifact-get = %d %v", resp.StatusCode, got)
	}

	resp, listed := h.postOp(t, "artifact-list", map[string]any{"type": "patch"}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact-list status = %d: %v", resp.StatusCode, listed)
	}
	artifacts, ok := listed["artifacts"].([]any)
	if !ok || len(artifacts) != 1 {
		t.Fatalf("artifact-list = %v, want exactly the declared artifact", listed)
	}
}

func TestTaskRunArtifactMutationFailsClosedWithoutCapability(t *testing.T) {
	h := newHarness(t)
	h.module.artifacts = nil
	resp, decoded := h.postOp(t, "artifact-declare", map[string]any{
		"artifactId": "artifact-unavailable", "type": "patch",
	}, identity{})
	if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, decoded) != "unavailable" {
		t.Fatalf("artifact-declare without capability = %d %v, want 503 unavailable", resp.StatusCode, decoded)
	}
	if _, err := h.store.ArtifactQueries().GetArtifactRecord(context.Background(), "WS", "artifact-unavailable"); !errors.Is(err, artifactsmodule.ErrNotFound) {
		t.Fatalf("artifact persisted without capability, get error = %v", err)
	}
}

func TestTaskRunArtifactForeignOwnerHidden(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.SeedArtifact(context.Background(), artifactsmodule.Artifact{
		WorkspaceKey: "WS",
		ArtifactID:   "foreign-1",
		OwnerType:    artifactsmodule.OwnerTaskRun,
		OwnerID:      "task-run-other",
		Type:         "patch",
	}, nil); err != nil {
		t.Fatalf("create foreign artifact: %v", err)
	}
	for _, op := range []string{"artifact-get", "artifact-finalize"} {
		resp, decoded := h.postOp(t, op, map[string]any{"artifactId": "foreign-1"}, identity{})
		if resp.StatusCode != http.StatusNotFound || errorCode(t, decoded) != "not_found" {
			t.Fatalf("%s on foreign artifact = %d %v, want 404 not_found", op, resp.StatusCode, decoded)
		}
	}
	resp, listed := h.postOp(t, "artifact-list", map[string]any{}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact-list status = %d", resp.StatusCode)
	}
	if artifacts, ok := listed["artifacts"].([]any); !ok || len(artifacts) != 0 {
		t.Fatalf("artifact-list leaked foreign artifacts: %v", listed)
	}
}

func TestTaskRunComplete(t *testing.T) {
	h := newHarness(t)
	resp, out := h.postOp(t, "complete", map[string]any{
		"completionId": "completion-1",
		"status":       "completed",
		"exitCode":     0,
		"inputTokens":  11,
		"outputTokens": 7,
		"closeTask":    true,
		"closeReason":  "done",
	}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d: %v", resp.StatusCode, out)
	}
	completion, ok := out["completion"].(map[string]any)
	if !ok || completion["completionId"] != "completion-1" {
		t.Fatalf("complete completion = %v", out["completion"])
	}
	taskRun, ok := out["taskRun"].(map[string]any)
	if !ok || taskRun["status"] != "completed" {
		t.Fatalf("complete taskRun = %v", out["taskRun"])
	}
	stored, err := h.store.TaskRuns().Get(context.Background(), "WS", h.taskRunID)
	if err != nil || stored.Status != execution.TaskRunRecordCompleted {
		t.Fatalf("stored run = %+v err=%v, want completed", stored, err)
	}

	// Post-terminal lease verification rejects: completion revokes the
	// credential regardless of any expiry.
	resp, decoded := h.postOp(t, "get", map[string]any{}, identity{})
	if resp.StatusCode != http.StatusUnauthorized || errorCode(t, decoded) != "lease_denied" {
		t.Fatalf("get after complete = %d %v, want 401 lease_denied", resp.StatusCode, decoded)
	}
}

func TestTaskRunCompleteStaleFenceRejected(t *testing.T) {
	h := newHarness(t)
	resp, decoded := h.postOp(t, "complete", map[string]any{"completionId": "completion-1"}, identity{fence: "41"})
	if resp.StatusCode != http.StatusForbidden || errorCode(t, decoded) != "not_owner" {
		t.Fatalf("stale-fence complete = %d %v, want 403 not_owner", resp.StatusCode, decoded)
	}
	stored, err := h.store.TaskRuns().Get(context.Background(), "WS", h.taskRunID)
	if err != nil || stored.Status != execution.TaskRunRecordRunning {
		t.Fatalf("stored run = %+v err=%v, want still running", stored, err)
	}
}

func TestModuleRegisterNilStore(t *testing.T) {
	mux := http.NewServeMux()
	NewModule(Config{}).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/task-run/get", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nil-store module registered routes: %d", rec.Code)
	}
}

func TestExecutionDesignErrorsKeepStableTaskRunAPIStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "not found", err: fmt.Errorf("update task design: %w", execution.ErrNotFound), status: http.StatusNotFound, code: "not_found"},
		{name: "invalid transition", err: fmt.Errorf("update task design: %w", execution.ErrInvalidTransition), status: http.StatusConflict, code: "invalid_transition"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeDomainOpError(recorder, test.err)
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if recorder.Code != test.status || errorCode(t, body) != test.code {
				t.Fatalf("response = %d %v, want %d %s", recorder.Code, body, test.status, test.code)
			}
		})
	}
}

func TestTaskRunOpBodyDefaultsToEmptyObject(t *testing.T) {
	h := newHarness(t)
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/WS/task-run/get", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	h.apply(req, identity{})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := json.Marshal(resp.Header)
		t.Fatalf("empty-body get = %d %s", resp.StatusCode, body)
	}
}

// TestTaskRunWrongWorkspaceRejected pins workspace scoping: the lease only
// verifies inside the workspace that owns the task run.
func TestTaskRunWrongWorkspaceRejected(t *testing.T) {
	h := newHarness(t)
	payload := bytes.NewReader([]byte("{}"))
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/OTHER/task-run/get", payload)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	h.apply(req, identity{})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-workspace get = %d, want 401", resp.StatusCode)
	}
}

func TestDecodeStrictParamsUsesExactOnePolicy(t *testing.T) {
	type params struct {
		TaskID string `json:"taskId"`
	}

	got, err := decodeStrictParams[params]([]byte(`{"taskId":"TASK-1"}`))
	if err != nil || got.TaskID != "TASK-1" {
		t.Fatalf("decodeStrictParams(valid) = (%+v, %v), want TASK-1", got, err)
	}

	for name, body := range map[string]string{
		"unknown field": `{"taskId":"TASK-1","authority":"forged"}`,
		"trailing JSON": `{"taskId":"TASK-1"} {"taskId":"TASK-2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeStrictParams[params]([]byte(body))
			if err == nil || !errors.Is(err, persistence.ErrInvalid) {
				t.Fatalf("decodeStrictParams(%s) error = %T %v, want persistence.ErrInvalid", name, err, err)
			}
		})
	}
}

func TestDecodeParamsUsesExactOnePolicy(t *testing.T) {
	type params struct {
		TaskID string `json:"taskId"`
	}

	got, err := decodeParams[params]([]byte(`{"taskId":"TASK-1"}`))
	if err != nil || got.TaskID != "TASK-1" {
		t.Fatalf("decodeParams(valid) = (%+v, %v), want TASK-1", got, err)
	}

	_, err = decodeParams[params]([]byte(`{"taskId":"TASK-1"} {"taskId":"TASK-2"}`))
	if err == nil || !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("decodeParams(trailing) error = %T %v, want persistence.ErrInvalid", err, err)
	}
}
