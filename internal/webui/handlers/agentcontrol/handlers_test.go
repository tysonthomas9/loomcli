package agentcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/dto"
)

// mockControlFn returns a mock AgentControlFn that records calls and returns
// a canned response. The opRecorder is populated with op, agentName, force.
type callRecord struct {
	op        string
	agentName string
	force     bool
}

func newMockControlFn(result *AgentControlResult, err error) (AgentControlFn, *[]callRecord) {
	var calls []callRecord
	fn := func(op, agentName string, force bool) (*AgentControlResult, error) {
		calls = append(calls, callRecord{op, agentName, force})
		return result, err
	}
	return fn, &calls
}

func serveRequest(handler http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	handler.ServeHTTP(rec, req)
	return rec
}

// --- Stop tests ---

func TestHandleAgentStop_NoForce_SendsYield_Returns202(t *testing.T) {
	fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/stop", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	c := (*calls)[0]
	if c.op != "agent_yield" {
		t.Errorf("op = %q, want agent_yield", c.op)
	}
	if c.agentName != "falcon" {
		t.Errorf("agentName = %q, want falcon", c.agentName)
	}
	if c.force {
		t.Error("force = true, want false")
	}

	var resp dto.MessageResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Error("Success = false")
	}
	if !strings.Contains(resp.Message, "yield requested") {
		t.Errorf("Message = %q, want to contain 'yield requested'", resp.Message)
	}
}

func TestHandleAgentStop_Force_SendsStop_Returns200(t *testing.T) {
	fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/stop",
		strings.NewReader(`{"force": true}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	c := (*calls)[0]
	if c.op != "agent_stop" {
		t.Errorf("op = %q, want agent_stop", c.op)
	}
	if !c.force {
		t.Error("force = false, want true")
	}

	var resp dto.MessageResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Message, "force-stopped") {
		t.Errorf("Message = %q, want to contain 'force-stopped'", resp.Message)
	}
}

func TestHandleAgentStop_EmptyBody_DefaultsNoForce(t *testing.T) {
	fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/stop", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	if (*calls)[0].op != "agent_yield" {
		t.Errorf("op = %q, want agent_yield", (*calls)[0].op)
	}
}

func TestHandleAgentStop_InvalidJSON_Returns400(t *testing.T) {
	fn, _ := newMockControlFn(&AgentControlResult{Success: true}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/stop",
		strings.NewReader(`{"invalid`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var resp dto.ErrorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Code != "bad_request" {
		t.Errorf("code = %q, want bad_request", resp.Code)
	}
}

// --- Start test ---

func TestHandleAgentStart_Success(t *testing.T) {
	fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", handleAgentStart(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/start", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	if (*calls)[0].op != "agent_start" {
		t.Errorf("op = %q, want agent_start", (*calls)[0].op)
	}
	if (*calls)[0].agentName != "falcon" {
		t.Errorf("agentName = %q, want falcon", (*calls)[0].agentName)
	}
}

// --- Restart test ---

func TestHandleAgentRestart_Success(t *testing.T) {
	fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", handleAgentRestart(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/restart", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	if (*calls)[0].op != "agent_restart" {
		t.Errorf("op = %q, want agent_restart", (*calls)[0].op)
	}
}

// --- Yield test ---

func TestHandleAgentYield_Success(t *testing.T) {
	fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", handleAgentYield(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/yield", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	if (*calls)[0].op != "agent_yield" {
		t.Errorf("op = %q, want agent_yield", (*calls)[0].op)
	}
	if (*calls)[0].agentName != "falcon" {
		t.Errorf("agentName = %q, want falcon", (*calls)[0].agentName)
	}

	var resp dto.MessageResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !strings.Contains(resp.Message, "yield requested") {
		t.Errorf("Message = %q, want to contain 'yield requested'", resp.Message)
	}
}

// --- List tests ---

func TestHandleAgentList_Success(t *testing.T) {
	entries := []AgentControlEntry{
		{Name: "falcon", Role: "planner", Status: "running"},
		{Name: "eagle", Role: "coder", Status: "stopped"},
	}
	data, _ := json.Marshal(entries)
	fn, _ := newMockControlFn(&AgentControlResult{Success: true, Data: data}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", handleAgentList(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws1/agents", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp dto.ListResponse[AgentControlEntry]
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Success {
		t.Error("Success = false")
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2", resp.Total)
	}
	if len(resp.Data) != 2 {
		t.Errorf("Data length = %d, want 2", len(resp.Data))
	}
	if resp.Data[0].Name != "falcon" {
		t.Errorf("Data[0].Name = %q, want falcon", resp.Data[0].Name)
	}
}

func TestHandleAgentList_Empty(t *testing.T) {
	data, _ := json.Marshal([]AgentControlEntry{})
	fn, _ := newMockControlFn(&AgentControlResult{Success: true, Data: data}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents", handleAgentList(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/ws1/agents", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp dto.ListResponse[AgentControlEntry]
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Total != 0 {
		t.Errorf("Total = %d, want 0", resp.Total)
	}
	if resp.Data == nil {
		t.Error("Data is nil, want empty slice")
	}
}

// --- Error mapping tests ---

func TestErrorMapping_NotFound(t *testing.T) {
	fn, _ := newMockControlFn(&AgentControlResult{
		Success: false,
		Error:   `agent "foo" not found in daemon config`,
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/foo/stop",
		strings.NewReader(`{"force": true}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	var resp dto.ErrorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Code != "agent_not_found" {
		t.Errorf("code = %q, want agent_not_found", resp.Code)
	}
}

func TestErrorMapping_AlreadyStopped(t *testing.T) {
	fn, _ := newMockControlFn(&AgentControlResult{
		Success: false,
		Error:   `agent "foo" is already stopped`,
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/yield", handleAgentYield(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/foo/yield", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	var resp dto.ErrorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Code != "agent_conflict" {
		t.Errorf("code = %q, want agent_conflict", resp.Code)
	}
}

func TestErrorMapping_NotStopped(t *testing.T) {
	fn, _ := newMockControlFn(&AgentControlResult{
		Success: false,
		Error:   `agent "foo" is not stopped (use restart to reset a running agent)`,
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", handleAgentStart(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/foo/start", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestErrorMapping_GenericDaemonError(t *testing.T) {
	fn, _ := newMockControlFn(&AgentControlResult{
		Success: false,
		Error:   "failed to stop agent",
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/foo/stop",
		strings.NewReader(`{"force": true}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	var resp dto.ErrorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Code != "daemon_error" {
		t.Errorf("code = %q, want daemon_error", resp.Code)
	}
}

// --- Daemon unreachable / timeout ---

func TestDaemonUnavailable_Returns503(t *testing.T) {
	fn, _ := newMockControlFn(nil, fmt.Errorf("daemon is not running"))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/start", handleAgentStart(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/start", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	var resp dto.ErrorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Code != "daemon_unavailable" {
		t.Errorf("code = %q, want daemon_unavailable", resp.Code)
	}
}

func TestDaemonTimeout_Returns504(t *testing.T) {
	fn, _ := newMockControlFn(nil, fmt.Errorf("read response: i/o timeout"))

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/restart", handleAgentRestart(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/restart", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", rec.Code)
	}
	var resp dto.ErrorResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Code != "daemon_timeout" {
		t.Errorf("code = %q, want daemon_timeout", resp.Code)
	}
}

// --- classifyAgentControlError ---

func TestClassifyAgentControlError(t *testing.T) {
	tests := []struct {
		err        string
		wantStatus int
		wantCode   string
	}{
		{`agent "x" not found in daemon config`, http.StatusNotFound, "agent_not_found"},
		{`agent "x" is already stopped`, http.StatusConflict, "agent_conflict"},
		{`agent "x" already running`, http.StatusConflict, "agent_conflict"},
		{`agent "x" is not stopped`, http.StatusConflict, "agent_conflict"},
		{`something went wrong`, http.StatusBadGateway, "daemon_error"},
	}
	for _, tt := range tests {
		status, code := classifyAgentControlError(&AgentControlResult{Error: tt.err})
		if status != tt.wantStatus {
			t.Errorf("error %q: status = %d, want %d", tt.err, status, tt.wantStatus)
		}
		if code != tt.wantCode {
			t.Errorf("error %q: code = %q, want %q", tt.err, code, tt.wantCode)
		}
	}
}

// --- Repo query-param / compound-key tests ---

func TestHandleAgentControl_RepoQueryParam_BuildsCompoundKey(t *testing.T) {
	// Each control handler should forward "repo/worktree" to the daemon when
	// the ?repo= query parameter is present, and bare worktree otherwise.
	tests := []struct {
		name    string
		handler func(AgentControlFn) http.HandlerFunc
		method  string
		pattern string
		path    string
		wantOp  string
		wantKey string
	}{
		{"stop with repo", handleAgentStop, "POST", "POST /api/workspaces/{ws}/agents/{name}/stop",
			"/api/workspaces/ws1/agents/falcon/stop?repo=backend", "agent_yield", "backend/falcon"},
		{"stop without repo", handleAgentStop, "POST", "POST /api/workspaces/{ws}/agents/{name}/stop",
			"/api/workspaces/ws1/agents/falcon/stop", "agent_yield", "falcon"},
		{"start with repo", handleAgentStart, "POST", "POST /api/workspaces/{ws}/agents/{name}/start",
			"/api/workspaces/ws1/agents/falcon/start?repo=backend", "agent_start", "backend/falcon"},
		{"restart with repo", handleAgentRestart, "POST", "POST /api/workspaces/{ws}/agents/{name}/restart",
			"/api/workspaces/ws1/agents/falcon/restart?repo=backend", "agent_restart", "backend/falcon"},
		{"yield with repo", handleAgentYield, "POST", "POST /api/workspaces/{ws}/agents/{name}/yield",
			"/api/workspaces/ws1/agents/falcon/yield?repo=backend", "agent_yield", "backend/falcon"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)
			mux := http.NewServeMux()
			mux.HandleFunc(tc.pattern, tc.handler(fn))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code >= 400 {
				t.Fatalf("status = %d, want < 400; body = %s", rec.Code, rec.Body.String())
			}
			if len(*calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(*calls))
			}
			c := (*calls)[0]
			if c.op != tc.wantOp {
				t.Errorf("op = %q, want %q", c.op, tc.wantOp)
			}
			if c.agentName != tc.wantKey {
				t.Errorf("agentName = %q, want %q", c.agentName, tc.wantKey)
			}
		})
	}
}

// TestHandleAgentStop_Force_RepoQueryParam_BuildsCompoundKey covers the
// force-stop branch specifically: with {"force": true} the handler takes the
// agent_stop path (not agent_yield) and must still pass the compound key.
func TestHandleAgentStop_Force_RepoQueryParam_BuildsCompoundKey(t *testing.T) {
	fn, calls := newMockControlFn(&AgentControlResult{Success: true}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/stop", handleAgentStop(fn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/workspaces/ws1/agents/falcon/stop?repo=backend", strings.NewReader(`{"force":true}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	c := (*calls)[0]
	if c.op != "agent_stop" {
		t.Errorf("op = %q, want agent_stop", c.op)
	}
	if c.agentName != "backend/falcon" {
		t.Errorf("agentName = %q, want backend/falcon", c.agentName)
	}
	if !c.force {
		t.Error("force = false, want true")
	}
}
