package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestLocalLockBridgeDelegatesToLockFileHelpers(t *testing.T) {
	dir := t.TempDir()
	if err := AcquireLock(dir, "worker", "nova"); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	t.Cleanup(func() { _ = ReleaseLock(dir) })

	bridge := &LocalLockBridge{WorktreePath: dir}
	if err := bridge.UpdateState("ignored", StateIdle); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	if err := bridge.UpdateTask("ignored", "ISSUE-1", "write tests"); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := bridge.UpdateClaudeSessionID("ignored", "session-123"); err != nil {
		t.Fatalf("UpdateClaudeSessionID: %v", err)
	}

	info, err := bridge.ReadLock("ignored")
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if info.State != StateIdle || info.TaskID != "ISSUE-1" || info.ClaudeSessionID != "session-123" {
		t.Fatalf("lock info = %+v, want state/task/session updates", info)
	}

	if err := bridge.ClearTaskID("ignored"); err != nil {
		t.Fatalf("ClearTaskID: %v", err)
	}
	if err := bridge.ClearClaudeSessionID("ignored"); err != nil {
		t.Fatalf("ClearClaudeSessionID: %v", err)
	}
	info, err = bridge.ReadLock("ignored")
	if err != nil {
		t.Fatalf("ReadLock after clear: %v", err)
	}
	if info.TaskID != "" || info.TaskTitle != "" || !info.TaskStartedAt.IsZero() || info.ClaudeSessionID != "" {
		t.Fatalf("cleared lock info = %+v, want task/session fields cleared", info)
	}
}

type lockBridgeRoundTripper struct {
	t        *testing.T
	status   int
	body     string
	requests []lockStateRequest
	headers  []http.Header
}

func (rt *lockBridgeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.t.Helper()
	if req.Method != http.MethodPost {
		rt.t.Fatalf("method = %s, want POST", req.Method)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		rt.t.Fatalf("Content-Type = %q, want application/json", got)
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		rt.t.Fatalf("read request body: %v", err)
	}
	var decoded lockStateRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		rt.t.Fatalf("decode request body %q: %v", data, err)
	}
	rt.requests = append(rt.requests, decoded)
	rt.headers = append(rt.headers, req.Header.Clone())

	status := rt.status
	if status == 0 {
		status = http.StatusOK
	}
	body := rt.body
	if decoded.Action == "read" && body == "" {
		body = `{"pid":123,"command":"worker","agent_name":"nova","task_id":"ISSUE-2","state":"active"}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestHTTPLockBridgeSendsExpectedActions(t *testing.T) {
	rt := &lockBridgeRoundTripper{t: t}
	bridge := &HTTPLockBridge{
		ControlPlaneURL: "http://control.test",
		WorkerID:        "worker-1",
		Token:           "secret",
		HTTPClient:      &http.Client{Transport: rt},
	}

	calls := []struct {
		name string
		run  func() error
		want lockStateRequest
	}{
		{
			name: "state",
			run:  func() error { return bridge.UpdateState("nova", StateIdle) },
			want: lockStateRequest{Action: "update_state", AgentName: "nova", State: StateIdle},
		},
		{
			name: "task",
			run:  func() error { return bridge.UpdateTask("nova", "ISSUE-2", "ship coverage") },
			want: lockStateRequest{Action: "update_task", AgentName: "nova", TaskID: "ISSUE-2", TaskTitle: "ship coverage"},
		},
		{
			name: "clear task",
			run:  func() error { return bridge.ClearTaskID("nova") },
			want: lockStateRequest{Action: "clear_task", AgentName: "nova"},
		},
		{
			name: "session",
			run:  func() error { return bridge.UpdateClaudeSessionID("nova", "sid-1") },
			want: lockStateRequest{Action: "update_claude_session_id", AgentName: "nova", ClaudeSessionID: "sid-1"},
		},
		{
			name: "clear session",
			run:  func() error { return bridge.ClearClaudeSessionID("nova") },
			want: lockStateRequest{Action: "clear_claude_session_id", AgentName: "nova"},
		},
	}

	for _, tc := range calls {
		if err := tc.run(); err != nil {
			t.Fatalf("%s returned error: %v", tc.name, err)
		}
	}
	info, err := bridge.ReadLock("nova")
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if info.AgentName != "nova" || info.TaskID != "ISSUE-2" || info.State != "active" {
		t.Fatalf("ReadLock info = %+v, want decoded response", info)
	}

	wantActions := append([]lockStateRequest{}, func() []lockStateRequest {
		out := make([]lockStateRequest, 0, len(calls)+1)
		for _, tc := range calls {
			out = append(out, tc.want)
		}
		out = append(out, lockStateRequest{Action: "read", AgentName: "nova"})
		return out
	}()...)
	if len(rt.requests) != len(wantActions) {
		t.Fatalf("requests = %d, want %d", len(rt.requests), len(wantActions))
	}
	for i, want := range wantActions {
		if rt.requests[i] != want {
			t.Fatalf("request %d = %+v, want %+v", i, rt.requests[i], want)
		}
		if got := rt.headers[i].Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("request %d Authorization = %q, want bearer token", i, got)
		}
	}
}

func TestHTTPLockBridgeErrorBranches(t *testing.T) {
	statusBridge := &HTTPLockBridge{
		ControlPlaneURL: "http://control.test",
		WorkerID:        "worker-1",
		HTTPClient:      &http.Client{Transport: &lockBridgeRoundTripper{t: t, status: http.StatusTeapot}},
	}
	if err := statusBridge.UpdateState("nova", StateActive); err == nil || !strings.Contains(err.Error(), "418") {
		t.Fatalf("UpdateState status error = %v, want 418", err)
	}
	if _, err := statusBridge.ReadLock("nova"); err == nil || !strings.Contains(err.Error(), "418") {
		t.Fatalf("ReadLock status error = %v, want 418", err)
	}

	badJSONBridge := &HTTPLockBridge{
		ControlPlaneURL: "http://control.test",
		WorkerID:        "worker-1",
		HTTPClient:      &http.Client{Transport: &lockBridgeRoundTripper{t: t, body: "not-json"}},
	}
	if _, err := badJSONBridge.ReadLock("nova"); err == nil || !strings.Contains(err.Error(), "decode lock info") {
		t.Fatalf("ReadLock decode error = %v, want decode lock info", err)
	}

	noSchemeBridge := &HTTPLockBridge{ControlPlaneURL: "", WorkerID: "worker-1"}
	if err := noSchemeBridge.UpdateTask("nova", "ISSUE-3", "title"); err == nil {
		t.Fatalf("UpdateTask without scheme succeeded, want client error")
	}

	defaultClientBridge := &HTTPLockBridge{}
	if defaultClientBridge.client() == nil {
		t.Fatalf("default client is nil")
	}
	explicitClient := &http.Client{}
	explicitClientBridge := &HTTPLockBridge{HTTPClient: explicitClient}
	if explicitClientBridge.client() != explicitClient {
		t.Fatalf("client did not return explicit HTTPClient")
	}
}

func TestLocalLockBridgeMissingLockSurfacesErrors(t *testing.T) {
	dir := t.TempDir()
	bridge := &LocalLockBridge{WorktreePath: dir}
	if err := bridge.UpdateState("nova", StateIdle); err == nil {
		t.Fatalf("UpdateState without lock succeeded")
	}
	if _, err := bridge.ReadLock("nova"); err == nil || !os.IsNotExist(err) {
		t.Fatalf("ReadLock error = %v, want not exist", err)
	}
}
