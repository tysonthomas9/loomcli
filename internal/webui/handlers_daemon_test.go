package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// --- Supervisor handler tests ---

func TestHandleDaemonSupervisor_HappyPath(t *testing.T) {
	now := time.Now().Add(-1 * time.Hour)
	fn := func() (*DaemonSupervisorData, error) {
		return &DaemonSupervisorData{
			PID:           12345,
			StartedAt:     now,
			UptimeSeconds: 3600.5,
			Agents: []DaemonAgentEntry{
				{Worktree: "falcon", Role: "planner", PID: 12346, Status: "running"},
				{Worktree: "spark", Role: "coder", PID: 12347, Status: "stopped"},
			},
		}, nil
	}

	h := handleDaemonSupervisor(fn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/daemon/supervisor", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success bool                 `json:"success"`
		Data    DaemonSupervisorData `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Data.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", resp.Data.PID)
	}
	if len(resp.Data.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(resp.Data.Agents))
	}
	if resp.Data.Agents[0].Worktree != "falcon" {
		t.Errorf("expected first agent worktree 'falcon', got %q", resp.Data.Agents[0].Worktree)
	}
}

func TestHandleDaemonSupervisor_FileMissing(t *testing.T) {
	fn := func() (*DaemonSupervisorData, error) {
		return nil, os.ErrNotExist
	}

	h := handleDaemonSupervisor(fn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/daemon/supervisor", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var resp struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if resp.Code != "daemon_not_running" {
		t.Errorf("expected code 'daemon_not_running', got %q", resp.Code)
	}
}

func TestHandleDaemonSupervisor_ParseError(t *testing.T) {
	fn := func() (*DaemonSupervisorData, error) {
		return nil, errors.New("invalid JSON in state file")
	}

	h := handleDaemonSupervisor(fn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/daemon/supervisor", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	var resp struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != "internal_error" {
		t.Errorf("expected code 'internal_error', got %q", resp.Code)
	}
}

func TestHandleDaemonSupervisor_MultipleAgents(t *testing.T) {
	fn := func() (*DaemonSupervisorData, error) {
		return &DaemonSupervisorData{
			PID:           100,
			StartedAt:     time.Now(),
			UptimeSeconds: 10,
			Agents: []DaemonAgentEntry{
				{Worktree: "a", Status: "running"},
				{Worktree: "b", Status: "stopped"},
				{Worktree: "c", Status: "failed"},
			},
		}, nil
	}

	h := handleDaemonSupervisor(fn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/daemon/supervisor", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Data DaemonSupervisorData `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data.Agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(resp.Data.Agents))
	}
}

// --- Config handler tests ---

func TestHandleDaemonConfig_HappyPath(t *testing.T) {
	configJSON := json.RawMessage(`{"backend":"claude","daemon":{"restart_policy":"always"}}`)
	fn := func() (json.RawMessage, error) {
		return configJSON, nil
	}

	h := handleDaemonConfig(fn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/daemon/config", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}

	// Verify data is valid JSON with expected fields
	var data map[string]any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("data is not valid JSON: %v", err)
	}
	if data["backend"] != "claude" {
		t.Errorf("expected backend 'claude', got %v", data["backend"])
	}
}

func TestHandleDaemonConfig_LoadError(t *testing.T) {
	fn := func() (json.RawMessage, error) {
		return nil, errors.New("syntax error in loom.yaml")
	}

	h := handleDaemonConfig(fn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/daemon/config", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var resp struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != "config_error" {
		t.Errorf("expected code 'config_error', got %q", resp.Code)
	}
}

func TestHandleDaemonConfig_MinimalJSON(t *testing.T) {
	fn := func() (json.RawMessage, error) {
		return json.RawMessage(`{"backend":"claude"}`), nil
	}

	h := handleDaemonConfig(fn)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/daemon/config", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify the full response is valid JSON
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

// --- Queue handler tests ---

func TestHandleAgentQueue_HappyPath(t *testing.T) {
	fn := func(name string) ([]AgentQueueEntry, error) {
		return []AgentQueueEntry{
			{IssueID: "proj-1.5", Title: "Feature X", Priority: 0, Score: 170, Reason: "base:100 skills:+50 priority:+20", Labels: []string{"phase-3"}, Parent: "proj-1"},
			{IssueID: "proj-1.8", Title: "Write tests", Priority: 1, Score: 116, Reason: "base:100 priority:+16", Labels: []string{"phase-3"}, Parent: "proj-1"},
			{IssueID: "proj-1.3", Title: "Docs", Priority: 2, Score: 100, Reason: "base:100", Labels: []string{}, Parent: "proj-1"},
		}, nil
	}

	h := handleAgentQueue(fn)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/default/agents/falcon/queue", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success bool              `json:"success"`
		Data    []AgentQueueEntry `json:"data"`
		Total   int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Total != 3 {
		t.Errorf("expected total=3, got %d", resp.Total)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(resp.Data))
	}
	if resp.Data[0].Score != 170 {
		t.Errorf("expected first entry score 170, got %d", resp.Data[0].Score)
	}
}

func TestHandleAgentQueue_EmptyQueue(t *testing.T) {
	fn := func(name string) ([]AgentQueueEntry, error) {
		return nil, nil
	}

	h := handleAgentQueue(fn)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/default/agents/falcon/queue", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Success bool              `json:"success"`
		Data    []AgentQueueEntry `json:"data"`
		Total   int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("expected total=0, got %d", resp.Total)
	}
	if resp.Data == nil {
		t.Error("expected data to be empty array, not null")
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty data, got %d entries", len(resp.Data))
	}
}

func TestHandleAgentQueue_AgentNotFound(t *testing.T) {
	fn := func(name string) ([]AgentQueueEntry, error) {
		return nil, ErrAgentNotFound
	}

	h := handleAgentQueue(fn)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/default/agents/nonexistent/queue", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	var resp struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != "agent_not_found" {
		t.Errorf("expected code 'agent_not_found', got %q", resp.Code)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
}

func TestHandleAgentQueue_ConfigError(t *testing.T) {
	fn := func(name string) ([]AgentQueueEntry, error) {
		return nil, errors.New("load daemon config: syntax error")
	}

	h := handleAgentQueue(fn)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/default/agents/falcon/queue", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestHandleAgentQueue_NameExtraction(t *testing.T) {
	var captured string
	fn := func(name string) ([]AgentQueueEntry, error) {
		captured = name
		return nil, nil
	}

	h := handleAgentQueue(fn)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/queue", h)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/default/agents/my-agent-name/queue", nil)
	mux.ServeHTTP(rec, req)

	if captured != "my-agent-name" {
		t.Errorf("expected agent name 'my-agent-name', got %q", captured)
	}
}

// --- Route registration tests ---

func TestWorkspaceOpsModule_QueueRouteRegistered(t *testing.T) {
	fn := func(name string) ([]AgentQueueEntry, error) {
		return nil, nil
	}
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, &stubErrorPool{}, fn)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/falcon/queue", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Error("queue route not registered (got 404)")
	}
}

func TestWorkspaceOpsModule_QueueRouteNotRegisteredWhenNil(t *testing.T) {
	mod := NewWorkspaceOpsModule(&mockWorkspaceService{}, &stubErrorPool{}, nil)

	mux := http.NewServeMux()
	mod.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/workspaces/test-ws/agents/falcon/queue", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when agentQueueFn is nil, got %d", rec.Code)
	}
}
