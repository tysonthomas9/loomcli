package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestAgentHandlersCRUDValidationAndLifecycle(t *testing.T) {
	svc := newStubAgentService()
	mux := http.NewServeMux()
	NewModule(svc, nil).Register(mux)

	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agents", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body.String())
	}

	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agents", strings.NewReader(`{"name":"spark","role_name":"task","auto":true}`))
	createReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", createRec.Code, createRec.Body.String())
	}
	var created domain.Agent
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.WorkspaceKey != "WS" || created.Name != "spark" || created.RoleName != "task" || !created.Auto {
		t.Fatalf("created agent = %+v", created)
	}

	mismatchRec := httptest.NewRecorder()
	mismatchReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agents", strings.NewReader(`{"workspace_key":"OTHER","name":"bad"}`))
	mismatchReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(mismatchRec, mismatchReq)
	if mismatchRec.Code != http.StatusBadRequest {
		t.Fatalf("mismatch status = %d, want 400; body=%s", mismatchRec.Code, mismatchRec.Body.String())
	}

	patchRec := httptest.NewRecorder()
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/workspaces/WS/agents/spark", strings.NewReader(`{"state":"active","desired_state":"running"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", patchRec.Code, patchRec.Body.String())
	}
	if got := svc.agents["WS/spark"]; got.State != domain.AgentStateActive || got.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("patched agent = %+v", got)
	}

	invalidRec := httptest.NewRecorder()
	invalidReq := httptest.NewRequest(http.MethodPatch, "/api/workspaces/WS/agents/spark", strings.NewReader(`{"state":"broken"}`))
	invalidReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid patch status = %d, want 400; body=%s", invalidRec.Code, invalidRec.Body.String())
	}

	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agents/spark/start", strings.NewReader(`{"payload":{"source":"ui"},"task_id":"TASK-1"}`))
	startReq.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body=%s", startRec.Code, startRec.Body.String())
	}
	if svc.lastLifecycle.CommandType != "start" ||
		svc.lastLifecycle.State != domain.AgentStateActive ||
		svc.lastLifecycle.DesiredState != domain.AgentDesiredRunning ||
		svc.lastLifecycle.Payload["source"] != "ui" ||
		svc.lastLifecycle.Payload["task_id"] != "TASK-1" {
		t.Fatalf("start lifecycle = %+v", svc.lastLifecycle)
	}

	restartRec := httptest.NewRecorder()
	mux.ServeHTTP(restartRec, httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agents/spark/restart", nil))
	if restartRec.Code != http.StatusAccepted {
		t.Fatalf("restart status = %d, want 202; body=%s", restartRec.Code, restartRec.Body.String())
	}

	queueRec := httptest.NewRecorder()
	mux.ServeHTTP(queueRec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agents/spark/queue", nil))
	if queueRec.Code != http.StatusNotImplemented {
		t.Fatalf("queue status = %d, want 501; body=%s", queueRec.Code, queueRec.Body.String())
	}

	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, httptest.NewRequest(http.MethodDelete, "/api/workspaces/WS/agents/spark", nil))
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if _, ok := svc.agents["WS/spark"]; ok {
		t.Fatal("agent was not deleted")
	}
}

func TestAgentLifecycleRejectsInvalidRequestBody(t *testing.T) {
	svc := newStubAgentService()
	svc.agents["WS/spark"] = &domain.Agent{WorkspaceKey: "WS", Name: "spark"}
	mux := http.NewServeMux()
	NewModule(svc, nil).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agents/spark/start", strings.NewReader(`{"payload":`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBroadcastAgentRefreshEmitsGenericAgentEvent(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	client := realtime.NewClient(1, realtime.ClientSendBuf, "0", nil, "ws-1")
	otherWorkspace := realtime.NewClient(2, realtime.ClientSendBuf, "0", nil, "ws-2")
	hub.RegisterClient(client)
	hub.RegisterClient(otherWorkspace)
	waitForAgentHubClients(t, hub, 2)

	broadcastAgentRefresh(hub, "ws-1", "agent-alpha", "tester")

	select {
	case got := <-client.Send():
		if got.Type != "refresh" {
			t.Errorf("Type = %q, want %q", got.Type, "refresh")
		}
		if got.EntityType != "agent" {
			t.Errorf("EntityType = %q, want %q", got.EntityType, "agent")
		}
		if got.EntityID != "agent-alpha" {
			t.Errorf("EntityID = %q, want %q", got.EntityID, "agent-alpha")
		}
		if got.Action != "agent.refresh" {
			t.Errorf("Action = %q, want %q", got.Action, "agent.refresh")
		}
		if got.Title != "agent-alpha" {
			t.Errorf("Title = %q, want %q", got.Title, "agent-alpha")
		}
		if got.Actor != "tester" {
			t.Errorf("Actor = %q, want %q", got.Actor, "tester")
		}
		if got.WorkspaceID != "ws-1" {
			t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, "ws-1")
		}
		if got.IssueID != "" {
			t.Errorf("IssueID = %q, want empty", got.IssueID)
		}
		if _, err := time.Parse(time.RFC3339Nano, got.Timestamp); err != nil {
			t.Errorf("Timestamp = %q, want RFC3339Nano: %v", got.Timestamp, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent refresh broadcast")
	}

	select {
	case got := <-otherWorkspace.Send():
		t.Fatalf("other workspace received agent refresh: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForAgentHubClients(t *testing.T, hub *realtime.Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.ClientCount() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("hub ClientCount() = %d, want %d", hub.ClientCount(), want)
}

type stubAgentService struct {
	agents        map[string]*domain.Agent
	lastLifecycle service.AgentLifecycleInput
}

func newStubAgentService() *stubAgentService {
	return &stubAgentService{agents: map[string]*domain.Agent{}}
}

func agentKey(ws, name string) string {
	return ws + "/" + name
}

func (s *stubAgentService) ListAgents(_ context.Context, wsKey string) ([]*domain.Agent, error) {
	var out []*domain.Agent
	for _, agent := range s.agents {
		if agent.WorkspaceKey == wsKey {
			out = append(out, agent)
		}
	}
	return out, nil
}

func (s *stubAgentService) CreateAgent(_ context.Context, in service.AgentCreateInput) (*domain.Agent, error) {
	agent := &domain.Agent{
		WorkspaceKey: in.WorkspaceKey,
		Name:         in.Name,
		RoleName:     in.RoleName,
		Auto:         in.Auto,
		DesiredState: in.DesiredState,
	}
	s.agents[agentKey(agent.WorkspaceKey, agent.Name)] = agent
	return agent, nil
}

func (s *stubAgentService) UpdateAgent(_ context.Context, wsKey, name string, patch service.AgentUpdateInput) (*domain.Agent, error) {
	agent := s.agents[agentKey(wsKey, name)]
	if agent == nil {
		return nil, service.ErrNotFound("agent not found")
	}
	if patch.State != nil {
		agent.State = *patch.State
	}
	if patch.DesiredState != nil {
		agent.DesiredState = *patch.DesiredState
	}
	return agent, nil
}

func (s *stubAgentService) RequestAgentLifecycle(_ context.Context, wsKey, name string, in service.AgentLifecycleInput) (*domain.Agent, error) {
	agent := s.agents[agentKey(wsKey, name)]
	if agent == nil {
		return nil, service.ErrNotFound("agent not found")
	}
	agent.State = in.State
	agent.DesiredState = in.DesiredState
	s.lastLifecycle = in
	return agent, nil
}

func (s *stubAgentService) DeleteAgent(_ context.Context, wsKey, name string) error {
	delete(s.agents, agentKey(wsKey, name))
	return nil
}

func (s *stubAgentService) GetTerminalInfo(context.Context, string, string) (*service.AgentTerminalInfoResult, error) {
	return nil, nil
}

func (s *stubAgentService) GenerateTerminalToken(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (s *stubAgentService) GetLog(context.Context, string, string, int, int64) (*service.AgentLogResult, error) {
	return nil, nil
}

func (s *stubAgentService) GetDiffStat(context.Context, string, string) (*service.AgentDiffStatResult, error) {
	return nil, nil
}

func (s *stubAgentService) GitPush(context.Context, string, string, string) (*ops.GitPushResult, error) {
	return nil, nil
}

func (s *stubAgentService) GitPushAll(context.Context, string) (*service.GitPushAllResult, error) {
	return nil, nil
}

func (s *stubAgentService) GitPull(context.Context, string, string, string) (*ops.GitPullResult, error) {
	return nil, nil
}

func (s *stubAgentService) GitSync(context.Context, string, string) (*service.GitSyncResult, error) {
	return nil, nil
}

func (s *stubAgentService) CreatePR(context.Context, string, string, string) (*ops.GitPRResult, error) {
	return nil, nil
}

func (s *stubAgentService) GitReset(context.Context, string, string, string, bool, bool) (*ops.GitResetResult, error) {
	return nil, nil
}

func (s *stubAgentService) GitStatus(context.Context, string, string) (*ops.GitStatusResult, error) {
	return nil, nil
}

func (s *stubAgentService) SetTargetBranch(context.Context, string, string, string) error {
	return nil
}
