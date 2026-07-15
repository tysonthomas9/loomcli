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
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

func TestHandleInteractivePromptsListsBuiltins(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST2/interactive-prompts", nil)
	rr := httptest.NewRecorder()

	HandleInteractivePrompts().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rr.Code, rr.Body.String())
	}
	var got interactivePromptsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("hidden")) {
		t.Fatalf("interactive prompt wire response leaked hidden field: %s", rr.Body.String())
	}
	if len(got.Prompts) < 2 {
		t.Fatalf("prompts = %#v, want built-ins", got.Prompts)
	}
	seen := map[string]string{}
	for _, prompt := range got.Prompts {
		seen[prompt.ID] = prompt.Label
	}
	if seen["lead"] != "Lead" || seen["pr-review"] != "PR Review" {
		t.Fatalf("prompts = %#v, want lead and pr-review", got.Prompts)
	}
	if _, ok := seen["pr-review-checkout"]; ok {
		t.Fatalf("hidden prompt pr-review-checkout was returned: %#v", got.Prompts)
	}
	if !domain.IsBuiltinInteractivePrompt("pr-review-checkout") {
		t.Fatal("pr-review-checkout must remain registered as a launchable builtin prompt")
	}
}

func TestHandleCreateCarriesInteractiveKindAndPromptFile(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "TEST2",
		Name:          "Test 2",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	body := []byte(`{
		"name":"review-nova",
		"role_name":"pr-review",
		"kind":"interactive",
		"prompt_file":"builtin:pr-review",
		"backend":"codex"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents", bytes.NewReader(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST2"))
	rr := httptest.NewRecorder()

	HandleCreate(agentSvc, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s, want 201", rr.Code, rr.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		RoleName string `json:"role_name"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	if created.ID != "review-nova" || created.Kind != agentRecordKindSupervised || created.Name != "review-nova" || created.RoleName != "pr-review" {
		t.Fatalf("created agent = %#v, want review-nova/pr-review", created)
	}
	role, err := st.Roles().Get(ctx, "TEST2", "pr-review")
	if err != nil {
		t.Fatalf("load created role: %v", err)
	}
	if role.Kind != domain.RoleKindInteractive || role.PromptFile != "builtin:pr-review" {
		t.Fatalf("role = kind:%q prompt:%q, want interactive builtin:pr-review", role.Kind, role.PromptFile)
	}

}

func TestModuleListWithoutRecordStoreUsesUnifiedSupervisedShape(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST2", Name: "Test 2"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "TEST2", Name: "falcon", RoleName: "task"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	mux := http.NewServeMux()
	NewModule(agentSvc, nil, nil).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST2/agents", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	var response struct {
		Data []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "falcon" || response.Data[0].Name != "falcon" || response.Data[0].Kind != agentRecordKindSupervised {
		t.Fatalf("list response = %+v, want one unified supervised falcon", response.Data)
	}
}

func TestModuleCreateRoutesInteractiveKindToAgentService(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           "TEST2",
		Name:          "Test 2",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	mux := http.NewServeMux()
	NewModule(agentSvc, st, nil).Register(mux)
	body := []byte(`{
		"name":"review-nova",
		"role_name":"pr-review",
		"kind":"interactive",
		"prompt_file":"builtin:pr-review",
		"backend":"codex"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s, want 201", rr.Code, rr.Body.String())
	}
	assertSupervisedAgentWireResponse(t, rr, "review-nova")
	role, err := st.Roles().Get(ctx, "TEST2", "pr-review")
	if err != nil {
		t.Fatalf("load created role: %v", err)
	}
	if role.Kind != domain.RoleKindInteractive || role.PromptFile != "builtin:pr-review" {
		t.Fatalf("role = kind:%q prompt:%q, want interactive builtin:pr-review", role.Kind, role.PromptFile)
	}

	inlineBody := []byte(`{
		"name":"custom-review",
		"role_name":"custom-review",
		"kind":"interactive",
		"prompt":"Review literally: {{ marker }}",
		"backend":"codex"
	}`)
	inlineReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents", bytes.NewReader(inlineBody))
	inlineRR := httptest.NewRecorder()
	mux.ServeHTTP(inlineRR, inlineReq)
	if inlineRR.Code != http.StatusCreated {
		t.Fatalf("inline status = %d body = %s, want 201", inlineRR.Code, inlineRR.Body.String())
	}
	assertSupervisedAgentWireResponse(t, inlineRR, "custom-review")
	inlineRole, err := st.Roles().Get(ctx, "TEST2", "custom-review")
	if err != nil {
		t.Fatalf("load inline role: %v", err)
	}
	if inlineRole.Kind != domain.RoleKindInteractive || inlineRole.Prompt != "Review literally: {{ marker }}" {
		t.Fatalf("inline role = kind:%q prompt:%q, want interactive literal prompt", inlineRole.Kind, inlineRole.Prompt)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/workspaces/TEST2/agents/review-nova", bytes.NewBufferString(`{"backend":"claude"}`))
	patchRR := httptest.NewRecorder()
	mux.ServeHTTP(patchRR, patchReq)
	if patchRR.Code != http.StatusOK {
		t.Fatalf("patch status = %d body = %s, want 200", patchRR.Code, patchRR.Body.String())
	}
	assertSupervisedAgentWireResponse(t, patchRR, "review-nova")
}

func TestModuleCreateDispatchesSupportedKindNamespaces(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		agentName  string
		seedWorker bool
		wantStatus int
	}{
		{
			name:       "worker role kind",
			body:       `{"name":"docs-worker","role_name":"docs-worker","kind":"worker","backend":"codex"}`,
			agentName:  "docs-worker",
			seedWorker: true,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "supervised record kind",
			body:       `{"name":"task-worker","role_name":"task","kind":"supervised","backend":"codex"}`,
			agentName:  "task-worker",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "legacy omitted kind",
			body:       `{"name":"legacy-worker","role_name":"task","backend":"codex"}`,
			agentName:  "legacy-worker",
			wantStatus: http.StatusCreated,
		},
		{
			name:       "unknown kind",
			body:       `{"name":"unknown-worker","role_name":"task","kind":"mystery","backend":"codex"}`,
			agentName:  "unknown-worker",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
				Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
			}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			if tt.seedWorker {
				if _, err := st.Roles().Create(ctx, store.RoleCreate{
					WorkspaceKey: "TEST2",
					Name:         "docs-worker",
					Kind:         string(domain.RoleKindWorker),
				}); err != nil {
					t.Fatalf("create worker role: %v", err)
				}
			}
			agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
			mux := http.NewServeMux()
			NewModule(agentSvc, st, nil).Register(mux)
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents", bytes.NewBufferString(tt.body))
			rr := httptest.NewRecorder()

			mux.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", rr.Code, rr.Body.String(), tt.wantStatus)
			}
			_, err := st.Agents().Get(ctx, "TEST2", tt.agentName)
			if tt.wantStatus == http.StatusCreated && err != nil {
				t.Fatalf("load created agent: %v", err)
			}
			if tt.wantStatus == http.StatusCreated {
				assertSupervisedAgentWireResponse(t, rr, tt.agentName)
			}
			if tt.wantStatus != http.StatusCreated && err == nil {
				t.Fatal("unsupported kind unexpectedly persisted an agent")
			}
		})
	}
}

func assertSupervisedAgentWireResponse(t *testing.T, rr *httptest.ResponseRecorder, wantName string) {
	t.Helper()
	var got struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode supervised agent response: %v", err)
	}
	if got.ID != wantName || got.Name != wantName || got.Kind != agentRecordKindSupervised {
		t.Fatalf("supervised response = %+v, want id/name %q and kind %q; body=%s", got, wantName, agentRecordKindSupervised, rr.Body.String())
	}
}

func TestModuleLifecycleHonorsStopAndRestartContract(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST2", Name: "falcon", RoleName: "task",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	mux := http.NewServeMux()
	NewModule(agentSvc, st, nil).Register(mux)

	gracefulReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents/falcon/stop", nil)
	gracefulRR := httptest.NewRecorder()
	mux.ServeHTTP(gracefulRR, gracefulReq)
	if gracefulRR.Code != http.StatusAccepted {
		t.Fatalf("graceful stop status = %d body=%s, want 202", gracefulRR.Code, gracefulRR.Body.String())
	}
	agent, err := st.Agents().Get(ctx, "TEST2", "falcon")
	if err != nil || agent.State != domain.AgentStateIdle || agent.DesiredState != domain.AgentDesiredDraining {
		t.Fatalf("agent after graceful stop = %+v err=%v, want idle/draining", agent, err)
	}

	forceReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents/falcon/stop", bytes.NewBufferString(`{"force":true}`))
	forceReq.Header.Set("Content-Type", "application/json")
	forceRR := httptest.NewRecorder()
	mux.ServeHTTP(forceRR, forceReq)
	if forceRR.Code != http.StatusOK {
		t.Fatalf("force stop status = %d body=%s, want 200", forceRR.Code, forceRR.Body.String())
	}
	agent, err = st.Agents().Get(ctx, "TEST2", "falcon")
	if err != nil || agent.State != domain.AgentStateStopped || agent.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("agent after force stop = %+v err=%v, want stopped/stopped", agent, err)
	}

	restartReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents/falcon/restart", nil)
	restartRR := httptest.NewRecorder()
	mux.ServeHTTP(restartRR, restartReq)
	if restartRR.Code != http.StatusOK {
		t.Fatalf("restart status = %d body=%s, want 200", restartRR.Code, restartRR.Body.String())
	}
	agent, err = st.Agents().Get(ctx, "TEST2", "falcon")
	if err != nil || agent.State != domain.AgentStateActive || agent.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("agent after restart = %+v err=%v, want active/running", agent, err)
	}

	commands, err := st.AgentCommands().List(ctx, "TEST2", store.AgentCommandFilter{
		TargetAgentID: "falcon",
		Status:        domain.AgentCommandQueued,
	})
	if err != nil {
		t.Fatalf("list lifecycle commands: %v", err)
	}
	if len(commands) != 3 {
		t.Fatalf("commands = %+v, want graceful, force, and restart commands", commands)
	}
	if commands[0].Type != "yield" || commands[0].Payload["force"] != "" {
		t.Fatalf("graceful command = %+v, want yield without force", commands[0])
	}
	if commands[1].Type != "stop" || commands[1].Payload["force"] != "true" {
		t.Fatalf("force command = %+v, want stop force=true", commands[1])
	}
	if commands[2].Type != "restart" {
		t.Fatalf("restart command = %+v, want restart", commands[2])
	}
}

func TestHandleCreateCarriesInlinePrompt(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	body := []byte(`{
		"name":"custom-nova",
		"role_name":"custom-nova",
		"kind":"interactive",
		"prompt":"Literal {{ marker }}",
		"backend":"codex"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents", bytes.NewReader(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST2"))
	rr := httptest.NewRecorder()

	HandleCreate(agentSvc, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s, want 201", rr.Code, rr.Body.String())
	}
	role, err := st.Roles().Get(ctx, "TEST2", "custom-nova")
	if err != nil {
		t.Fatalf("load created role: %v", err)
	}
	if role.Prompt != "Literal {{ marker }}" {
		t.Fatalf("role prompt = %q, want literal transport value", role.Prompt)
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
