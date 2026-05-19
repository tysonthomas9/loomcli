package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

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

func TestAgentHandlersCRUDAndLifecycle(t *testing.T) {
	fake := &fakeAgentService{agent: &domain.Agent{WorkspaceKey: "WS", Name: "agent", RoleName: "task"}}
	req := agentRequest(http.MethodGet, "/agents", nil, "WS", "")
	rec := httptest.NewRecorder()
	HandleList(fake).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}

	createBody := []byte(`{"name":"agent","role_name":"task"}`)
	req = agentRequest(http.MethodPost, "/agents", createBody, "WS", "")
	rec = httptest.NewRecorder()
	HandleCreate(fake, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || fake.created.WorkspaceKey != "WS" {
		t.Fatalf("create status=%d created=%+v body=%s", rec.Code, fake.created, rec.Body.String())
	}

	req = agentRequest(http.MethodPatch, "/agents/agent", []byte(`{"state":"active","desired_state":"running"}`), "WS", "agent")
	rec = httptest.NewRecorder()
	HandleUpdate(fake, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fake.updateName != "agent" {
		t.Fatalf("update status=%d name=%q body=%s", rec.Code, fake.updateName, rec.Body.String())
	}

	req = agentRequest(http.MethodDelete, "/agents/agent", nil, "WS", "agent")
	rec = httptest.NewRecorder()
	HandleDelete(fake, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fake.deleted != "agent" {
		t.Fatalf("delete status=%d deleted=%q body=%s", rec.Code, fake.deleted, rec.Body.String())
	}

	req = agentRequest(http.MethodPost, "/agents/agent/start", []byte(`{"task_id":"T-1","payload":{"k":"v"}}`), "WS", "agent")
	rec = httptest.NewRecorder()
	HandleStart(fake, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || fake.lifecycle.CommandType != "start" || fake.lifecycle.Payload["task_id"] != "T-1" || fake.lifecycle.Payload["k"] != "v" {
		t.Fatalf("start status=%d lifecycle=%+v body=%s", rec.Code, fake.lifecycle, rec.Body.String())
	}
	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		want    int
		command string
	}{
		{"stop", HandleStop(fake, nil), http.StatusOK, "stop"},
		{"restart", HandleRestart(fake, nil), http.StatusAccepted, "restart"},
		{"yield", HandleYield(fake, nil), http.StatusAccepted, "yield"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := agentRequest(http.MethodPost, "/agents/agent/"+tt.name, nil, "WS", "agent")
			rec := httptest.NewRecorder()
			tt.handler.ServeHTTP(rec, req)
			if rec.Code != tt.want || fake.lifecycle.CommandType != tt.command {
				t.Fatalf("status=%d command=%q body=%s", rec.Code, fake.lifecycle.CommandType, rec.Body.String())
			}
		})
	}
}

func TestAgentHandlersValidationAndUnsupported(t *testing.T) {
	fake := &fakeAgentService{agent: &domain.Agent{WorkspaceKey: "WS", Name: "agent", RoleName: "task"}}
	rec := httptest.NewRecorder()
	HandleCreate(fake, nil).ServeHTTP(rec, agentRequest(http.MethodPost, "/agents", []byte(`{`), "WS", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	HandleCreate(fake, nil).ServeHTTP(rec, agentRequest(http.MethodPost, "/agents", []byte(`{"workspace_key":"OTHER","name":"agent","role_name":"task"}`), "WS", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("workspace mismatch status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	HandleUpdate(fake, nil).ServeHTTP(rec, agentRequest(http.MethodPatch, "/agents/agent", []byte(`{"state":"bad"}`), "WS", "agent"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	HandleUpdate(fake, nil).ServeHTTP(rec, agentRequest(http.MethodPatch, "/agents/agent", []byte(`{"desired_state":"bad"}`), "WS", "agent"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad desired status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	HandleStart(fake, nil).ServeHTTP(rec, agentRequest(http.MethodPost, "/agents/agent/start", []byte(`{`), "WS", "agent"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad lifecycle status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	HandleQueueUnsupported(rec, httptest.NewRequest(http.MethodGet, "/queue", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("queue status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !validAgentState(domain.AgentStateIdle) || validAgentState("bad") || !validAgentDesiredState(domain.AgentDesiredDraining) || validAgentDesiredState("bad") {
		t.Fatal("state validation mismatch")
	}
}

func agentRequest(method, target string, body []byte, workspace, name string) *http.Request {
	var rbody *bytes.Reader
	if body == nil {
		rbody = bytes.NewReader(nil)
	} else {
		rbody = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rbody)
	req = req.WithContext(middleware.WithWorkspace(context.Background(), workspace))
	if name != "" {
		req.SetPathValue("name", name)
	}
	return req
}

type fakeAgentService struct {
	agent      *domain.Agent
	created    service.AgentCreateInput
	updateName string
	deleted    string
	lifecycle  service.AgentLifecycleInput
}

func (f *fakeAgentService) GetTerminalInfo(context.Context, string, string) (*service.AgentTerminalInfoResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GenerateTerminalToken(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (f *fakeAgentService) GetLog(context.Context, string, string, int, int64) (*service.AgentLogResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GetDiffStat(context.Context, string, string) (*service.AgentDiffStatResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GitPush(context.Context, string, string, string) (*ops.GitPushResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GitPushAll(context.Context, string) (*service.GitPushAllResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GitPull(context.Context, string, string, string) (*ops.GitPullResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GitSync(context.Context, string, string) (*service.GitSyncResult, error) {
	return nil, nil
}
func (f *fakeAgentService) CreatePR(context.Context, string, string, string) (*ops.GitPRResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GitReset(context.Context, string, string, string, bool, bool) (*ops.GitResetResult, error) {
	return nil, nil
}
func (f *fakeAgentService) GitStatus(context.Context, string, string) (*ops.GitStatusResult, error) {
	return nil, nil
}
func (f *fakeAgentService) SetTargetBranch(context.Context, string, string, string) error { return nil }
func (f *fakeAgentService) ListAgents(context.Context, string) ([]*domain.Agent, error) {
	return []*domain.Agent{f.agent}, nil
}
func (f *fakeAgentService) CreateAgent(_ context.Context, in service.AgentCreateInput) (*domain.Agent, error) {
	f.created = in
	return f.agent, nil
}
func (f *fakeAgentService) UpdateAgent(_ context.Context, _ string, name string, _ service.AgentUpdateInput) (*domain.Agent, error) {
	f.updateName = name
	return f.agent, nil
}
func (f *fakeAgentService) RequestAgentLifecycle(_ context.Context, _ string, _ string, in service.AgentLifecycleInput) (*domain.Agent, error) {
	f.lifecycle = in
	return f.agent, nil
}
func (f *fakeAgentService) DeleteAgent(_ context.Context, _ string, name string) error {
	f.deleted = name
	return nil
}

func TestAgentHandlerResponseJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleList(&fakeAgentService{agent: &domain.Agent{Name: "agent"}}).ServeHTTP(rec, agentRequest(http.MethodGet, "/agents", nil, "WS", ""))
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["total"] != float64(1) {
		t.Fatalf("body = %#v", body)
	}
}
