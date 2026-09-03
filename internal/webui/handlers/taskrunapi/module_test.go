package taskrunapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// fakeIssueBackend embeds the interface so only Get needs a real
// implementation; anything else panics loudly.
type fakeIssueBackend struct {
	backend.IssueBackend
	task  *backend.IssueDetailData
	actor string
}

type failingOpenAgentSessionStore struct {
	store.AgentSessionStore
}

func (s failingOpenAgentSessionStore) Open(context.Context, store.SessionRunContext, store.SessionDescriptor) (store.SessionRef, error) {
	return store.SessionRef{}, &store.SessionLifecycleTransientError{
		Code: store.SessionLifecycleErrContention,
		Err:  domain.ErrConflict,
	}
}

type agentSessionStoreOverride struct {
	store.Store
	agentSessions store.AgentSessionStore
}

func (s agentSessionStoreOverride) AgentSessions() store.AgentSessionStore {
	return s.agentSessions
}

func (f *fakeIssueBackend) Get(_ context.Context, _ string) (*backend.IssueDetailData, error) {
	return f.task, nil
}

type testHarness struct {
	server  *httptest.Server
	store   store.Store
	backend *fakeIssueBackend

	taskRunID string
	nodeID    string
	leaseID   string
	token     string
	fence     int64
}

func newHarness(t *testing.T) *testHarness {
	return newHarnessWithConfig(t, "", "")
}

func newHarnessWithConfig(t *testing.T, localSettingsDir, runner string) *testHarness {
	t.Helper()
	st := memstore.New()
	h := &testHarness{
		store:     st,
		backend:   &fakeIssueBackend{task: &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-1", Title: "Do the work"}}},
		taskRunID: "task-run-1",
		nodeID:    "node-1",
		leaseID:   "lease-1",
		token:     "lease-token-1",
		fence:     42,
	}
	if _, err := st.TaskRuns().Create(context.Background(), store.TaskRunCreate{
		WorkspaceKey: "WS",
		TaskRunID:    h.taskRunID,
		TaskID:       "TASK-1",
		Runner:       runner,
		Status:       domain.TaskRunRunning,
		NodeID:       h.nodeID,
		LeaseID:      h.leaseID,
		FencingToken: h.fence,
	}); err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	module := NewModule(Config{
		Store:            st,
		LocalSettingsDir: localSettingsDir,
		IssueBackends: func(_, actor string) (backend.IssueBackend, error) {
			h.backend.actor = actor
			return h.backend, nil
		},
	})
	mux := http.NewServeMux()
	module.Register(mux)
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

func TestSessionOpenPostsServeRegistryCallback(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	_, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-callback", TaskID: "TASK-1",
		Status: domain.TaskRunRunning, NodeID: "node-1", LeaseID: "lease-1", FencingToken: 42,
		RuntimeMetadata: map[string]string{"bridge_task_plane": "true"},
	})
	if err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	registry := driverpkg.NewTaskRunSessionOpenRegistry()
	module := NewModule(Config{Store: st, OnSessionOpen: registry.Record})
	body, _ := json.Marshal(map[string]any{"invocationKey": "agent", "backend": "codex", "model": "gpt-5"})
	_, err = module.sessionOpen(ctx, "WS", leaseIdentity{
		TaskRunID: "task-run-callback", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token", FencingToken: 42,
	}, body)
	if err != nil {
		t.Fatalf("sessionOpen: %v", err)
	}
	live := registry.Live(store.SessionRunContext{WorkspaceKey: "WS", TaskRunID: "task-run-callback", Attempt: 1, FencingToken: 42})
	if len(live) != 1 || live[0].SessionID != "task-run-callback-a1-agent" {
		t.Fatalf("registry Live = %+v", live)
	}
	_, err = st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-non-bridge", TaskID: "TASK-2",
		Status: domain.TaskRunRunning, NodeID: "node-1", LeaseID: "lease-2", FencingToken: 43,
	})
	if err != nil {
		t.Fatalf("Create non-bridge task run: %v", err)
	}
	_, err = module.sessionOpen(ctx, "WS", leaseIdentity{
		TaskRunID: "task-run-non-bridge", NodeID: "node-1", LeaseID: "lease-2", LeaseToken: "token", FencingToken: 43,
	}, body)
	if err != nil {
		t.Fatalf("non-bridge sessionOpen: %v", err)
	}
	nonBridgeLive := registry.Live(store.SessionRunContext{
		WorkspaceKey: "WS", TaskRunID: "task-run-non-bridge", Attempt: 1, FencingToken: 43,
	})
	if len(nonBridgeLive) != 0 {
		t.Fatalf("non-bridge session leaked into registry: %+v", nonBridgeLive)
	}
}

func TestTaskRunCompleteRejectsBridgeLeafSelfComplete(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	_, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-bridge", TaskID: "TASK-1",
		Status: domain.TaskRunRunning, NodeID: "node-1", LeaseID: "lease-1", FencingToken: 42,
		RuntimeMetadata: map[string]string{"bridge_task_plane": "true"},
	})
	if err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	module := NewModule(Config{Store: st})
	_, err = module.complete(ctx, "WS", leaseIdentity{
		TaskRunID: "task-run-bridge", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token", FencingToken: 42,
	}, []byte(`{"status":"completed"}`))
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("complete err = %v, want ErrInvalidTransition", err)
	}
	_, err = module.complete(ctx, "WS", leaseIdentity{
		TaskRunID: "task-run-bridge", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token", FencingToken: 41,
	}, []byte(`{"status":"completed"}`))
	if !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("stale bridge complete err = %v, want ErrNotOwner", err)
	}
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

func TestTaskRunSessionOpenAuthRejectedWithoutLease(t *testing.T) {
	h := newHarness(t)
	resp, decoded := h.postOp(t, "session-open", map[string]any{
		"invocationKey": "agent",
		"backend":       "codex",
		"model":         "gpt-5",
	}, identity{noAuth: true})
	if resp.StatusCode != http.StatusUnauthorized || errorCode(t, decoded) != "unauthenticated" {
		t.Fatalf("session-open without lease = %d %v, want 401 unauthenticated", resp.StatusCode, decoded)
	}
}

func TestTaskRunSessionOpenProjectsLifecycleErrors(t *testing.T) {
	h := newHarness(t)
	open := func(body map[string]any) (int, string, map[string]any) {
		resp, decoded := h.postOp(t, "session-open", body, identity{})
		if resp.StatusCode == http.StatusOK {
			return resp.StatusCode, "", decoded
		}
		return resp.StatusCode, errorCode(t, decoded), decoded
	}
	valid := map[string]any{
		"invocationKey": "agent",
		"backend":       "codex",
		"model":         "gpt-5",
		"kind":          "judge",
		"tags":          []string{"eval"},
		"metadata":      map[string]string{"judged_session_id": "target-1"},
	}
	status, code, opened := open(valid)
	if status != http.StatusOK || code != "" || opened["sessionId"] != "task-run-1-a1-agent" || opened["attempt"] != float64(1) {
		t.Fatalf("session-open = %d %q %v, want composed id and attempt", status, code, opened)
	}

	conflict := maps.Clone(valid)
	conflict["backend"] = "claude"
	status, code, decoded := open(conflict)
	if status != http.StatusConflict || code != store.SessionLifecycleErrDescriptorConflict {
		t.Fatalf("descriptor conflict = %d %q %v", status, code, decoded)
	}

	status, code, decoded = open(map[string]any{
		"invocationKey": "Bad Key",
		"backend":       "codex",
		"model":         "gpt-5",
	})
	if status != http.StatusBadRequest || code != "invalid" {
		t.Fatalf("invalid invocation key = %d %q %v", status, code, decoded)
	}
}

func TestTaskRunSessionOpenOnTerminalRunProjectsLifecycleCode(t *testing.T) {
	h := newHarness(t)
	resp, completed := h.postOp(t, "complete", map[string]any{"status": "completed"}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete task run = %d %v", resp.StatusCode, completed)
	}
	resp, decoded := h.postOp(t, "session-open", map[string]any{
		"invocationKey": "agent",
		"backend":       "codex",
		"model":         "gpt-5",
	}, identity{})
	if resp.StatusCode != http.StatusConflict || errorCode(t, decoded) != store.SessionLifecycleErrTaskRunTerminal {
		t.Fatalf("terminal session-open = %d %v, want 409 task_run_terminal", resp.StatusCode, decoded)
	}
}

func TestTaskRunSessionOpenProjectsTransientLifecycleError(t *testing.T) {
	h := newHarness(t)
	override := agentSessionStoreOverride{
		Store:         h.store,
		agentSessions: failingOpenAgentSessionStore{AgentSessionStore: h.store.AgentSessions()},
	}
	module := NewModule(Config{Store: override})
	mux := http.NewServeMux()
	module.Register(mux)
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)

	resp, decoded := h.postOp(t, "session-open", map[string]any{
		"invocationKey": "agent",
		"backend":       "codex",
		"model":         "gpt-5",
	}, identity{})
	if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, decoded) != store.SessionLifecycleErrContention {
		t.Fatalf("transient session-open = %d %v, want 503 session_lifecycle_contention", resp.StatusCode, decoded)
	}
}

func TestTaskRunSessionCloseFirstTerminalWins(t *testing.T) {
	h := newHarness(t)
	resp, opened := h.postOp(t, "session-open", map[string]any{
		"invocationKey": "agent",
		"backend":       "codex",
		"model":         "gpt-5",
	}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session-open = %d %v", resp.StatusCode, opened)
	}
	sessionID, _ := opened["sessionId"].(string)
	close := map[string]any{
		"sessionId":     sessionID,
		"status":        "completed",
		"exitCode":      0,
		"summary":       "done",
		"transcriptRef": "artifact://transcript-task-run-1-a1-agent",
		"metadata": map[string]string{
			"driver_runner_session_id": "backend-session-1",
		},
	}
	resp, closed := h.postOp(t, "session-close", close, identity{})
	if resp.StatusCode != http.StatusOK || closed["status"] != "completed" {
		t.Fatalf("session-close = %d %v", resp.StatusCode, closed)
	}
	// Same outcome is a no-op success.
	resp, replay := h.postOp(t, "session-close", close, identity{})
	if resp.StatusCode != http.StatusOK || replay["status"] != "completed" {
		t.Fatalf("session-close replay = %d %v", resp.StatusCode, replay)
	}

	conflict := maps.Clone(close)
	conflict["status"] = "failed"
	resp, decoded := h.postOp(t, "session-close", conflict, identity{})
	if resp.StatusCode != http.StatusConflict || errorCode(t, decoded) != store.SessionLifecycleErrOutcomeConflict {
		t.Fatalf("conflicting session-close = %d %v", resp.StatusCode, decoded)
	}

	session, err := h.store.AgentSessions().Get(context.Background(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get finalized session: %v", err)
	}
	if got := session.Metadata[store.SessionMetadataDriverRunnerSessionID]; got != "backend-session-1" {
		t.Fatalf("driver runner session stamp = %q, want backend-session-1", got)
	}
	if _, ok := session.Metadata[store.SessionMetadataUsageTokens]; ok {
		t.Fatalf("missing usage was written as tokens=0: %v", session.Metadata)
	}
	for _, key := range []string{"input_tokens", "output_tokens", "estimated_cost_usd"} {
		if _, ok := session.Metadata[key]; ok {
			t.Fatalf("missing usage was written as %s=0: %v", key, session.Metadata)
		}
	}
}

func TestTaskRunSessionCloseProjectsSplitUsage(t *testing.T) {
	h := newHarness(t)
	_, opened := h.postOp(t, "session-open", map[string]any{
		"invocationKey": "judge", "backend": "codex", "model": "gpt-5.6-sol",
	}, identity{})
	sessionID := opened["sessionId"].(string)
	resp, closed := h.postOp(t, "session-close", map[string]any{
		"sessionId": sessionID, "status": "completed",
		"usage": map[string]any{
			"tokens": 16949, "inputTokens": 15755, "cacheReadTokens": 0, "outputTokens": 1194, "cost": 1.25,
		},
	}, identity{})
	if resp.StatusCode != http.StatusOK || closed["status"] != "completed" {
		t.Fatalf("session-close = %d %v", resp.StatusCode, closed)
	}
	session, err := h.store.AgentSessions().Get(t.Context(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get finalized session: %v", err)
	}
	want := map[string]string{
		store.SessionMetadataUsageTokens: "16949", "input_tokens": "15755", "cache_read_tokens": "0",
		"output_tokens": "1194", "estimated_cost_usd": "1.25",
	}
	for key, value := range want {
		if got := session.Metadata[key]; got != value {
			t.Errorf("metadata[%q] = %q, want %q", key, got, value)
		}
	}
}

func TestTaskRunSessionCloseAuthRejected(t *testing.T) {
	h := newHarness(t)
	resp, decoded := h.postOp(t, "session-close", map[string]any{
		"sessionId": "task-run-1-a1-agent",
		"status":    "completed",
	}, identity{noAuth: true})
	if resp.StatusCode != http.StatusUnauthorized || errorCode(t, decoded) != "unauthenticated" {
		t.Fatalf("session-close without lease = %d %v, want 401 unauthenticated", resp.StatusCode, decoded)
	}
}

func TestTaskRunSessionCloseUnknownSession(t *testing.T) {
	h := newHarness(t)
	resp, decoded := h.postOp(t, "session-close", map[string]any{
		"sessionId": "task-run-1-a1-missing",
		"status":    "completed",
	}, identity{})
	if resp.StatusCode != http.StatusNotFound || errorCode(t, decoded) != "not_found" {
		t.Fatalf("unknown session-close = %d %v, want 404 not_found", resp.StatusCode, decoded)
	}
}

func TestTaskRunSessionCloseRejectsCrossRunOwnership(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.TaskRuns().Create(t.Context(), store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-2", TaskID: "TASK-2", Status: domain.TaskRunRunning,
	}); err != nil {
		t.Fatalf("create foreign task run: %v", err)
	}
	foreign, err := h.store.AgentSessions().Open(t.Context(), store.SessionRunContext{
		WorkspaceKey: "WS", TaskRunID: "task-run-2", Attempt: 1, FencingToken: 43,
	}, store.SessionDescriptor{InvocationKey: "agent", Backend: "codex", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("open foreign session: %v", err)
	}
	resp, decoded := h.postOp(t, "session-close", map[string]any{
		"sessionId": foreign.SessionID,
		"status":    "completed",
	}, identity{})
	if resp.StatusCode != http.StatusNotFound || errorCode(t, decoded) != "not_found" {
		t.Fatalf("cross-run session-close = %d %v, want 404 not_found", resp.StatusCode, decoded)
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
	if h.backend.actor != "task-run:task-run-1" {
		t.Fatalf("issue backend actor = %q, want task-run scoped actor", h.backend.actor)
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

	resp, entry := h.postOp(t, "log-append", map[string]any{"stream": "stdout", "text": "hello\n"}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("log-append status = %d: %v", resp.StatusCode, entry)
	}
	if entry["taskRunId"] != "task-run-1" || entry["text"] != "hello\n" || entry["sequence"] != float64(1) {
		t.Fatalf("log-append result = %v", entry)
	}
	logs, err := h.store.TaskRuns().ListLogs(context.Background(), "WS", h.taskRunID, store.TaskRunLogFilter{})
	if err != nil || len(logs) != 1 || logs[0].Text != "hello\n" {
		t.Fatalf("stored logs = %v err=%v, want the appended line", logs, err)
	}

	resp, decoded := h.postOp(t, "log-append", map[string]any{"stream": "stdout"}, identity{})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
		t.Fatalf("log-append without text = %d %v, want 400 invalid", resp.StatusCode, decoded)
	}
}

func TestTaskRunRuntimeCredentialRequiresDaytonaRunner(t *testing.T) {
	dir := t.TempDir()
	settings := runtimesettings.Default()
	daytona, err := runtimesettings.SealRuntimeCredential(dir, runtimesettings.RuntimeCredentialProviderDaytona, "dtn-secret", time.Now())
	if err != nil {
		t.Fatalf("seal daytona credential: %v", err)
	}
	github, err := runtimesettings.SealRuntimeCredential(dir, runtimesettings.RuntimeCredentialProviderGitHub, "gh-secret", time.Now())
	if err != nil {
		t.Fatalf("seal github credential: %v", err)
	}
	settings.RuntimeCredentials.Daytona = daytona
	settings.RuntimeCredentials.GitHub = github
	if err := runtimesettings.Save(dir, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	h := newHarnessWithConfig(t, dir, "daytona-task-runner")
	resp, decoded := h.postOp(t, "runtime-credential", map[string]any{"provider": "daytona"}, identity{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runtime-credential status = %d: %v", resp.StatusCode, decoded)
	}
	if decoded["value"] != "dtn-secret" || decoded["provider"] != "daytona" {
		t.Fatalf("runtime credential = %v, want daytona secret", decoded)
	}

	local := newHarnessWithConfig(t, dir, "local-task-runner")
	resp, decoded = local.postOp(t, "runtime-credential", map[string]any{"provider": "github"}, identity{})
	if resp.StatusCode != http.StatusForbidden || errorCode(t, decoded) != "not_owner" {
		t.Fatalf("local runtime credential = %d %v, want 403 not_owner", resp.StatusCode, decoded)
	}
}

func TestTaskRunArtifactLifecycle(t *testing.T) {
	h := newHarness(t)
	resp, artifact := h.postOp(t, "artifact-declare", map[string]any{
		"artifactId":  "artifact-1",
		"type":        "patch",
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
	if artifact["durableStatus"] != "declared" {
		t.Fatalf("declared artifact status = %v", artifact["durableStatus"])
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

func TestTaskRunArtifactForeignOwnerHidden(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.Artifacts().Create(context.Background(), store.ArtifactCreate{
		WorkspaceKey: "WS",
		ArtifactID:   "foreign-1",
		OwnerType:    taskRunOwnerType,
		OwnerID:      "task-run-other",
		Type:         "patch",
	}); err != nil {
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
	if err != nil || stored.Status != domain.TaskRunCompleted {
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
	if err != nil || stored.Status != domain.TaskRunRunning {
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
