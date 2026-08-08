package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	HandleCreate(agentSvc, nil, nil).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s, want 201", rr.Code, rr.Body.String())
	}
	var created domain.Agent
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created agent: %v", err)
	}
	if created.Name != "review-nova" || created.RoleName != "pr-review" {
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

	HandleCreate(agentSvc, nil, nil).ServeHTTP(rr, req)

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

func TestHandleCreateStartsProvisioningAsync(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	provisioner := &fakeLeadProvisioner{calls: make(chan provisionCall, 1)}
	body := []byte(`{
		"name":"review-nova",
		"role_name":"pr-review",
		"kind":"interactive",
		"prompt_file":"builtin:pr-review",
		"backend":"codex"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents", bytes.NewReader(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST2"))
	req.Header.Set("X-Actor", "tester")
	rr := httptest.NewRecorder()

	HandleCreate(agentSvc, nil, provisioner).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s, want 201", rr.Code, rr.Body.String())
	}
	select {
	case got := <-provisioner.calls:
		if got.ws != "TEST2" || got.name != "review-nova" {
			t.Fatalf("provision call = %+v, want TEST2/review-nova", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async provisioning call")
	}
}

func TestHandleCreateProvisionerErrorStillReturnsCreated(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "Test 2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	provisioner := &fakeLeadProvisioner{
		calls: make(chan provisionCall, 1),
		err:   errors.New("provision failed"),
	}
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

	HandleCreate(agentSvc, nil, provisioner).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s, want 201", rr.Code, rr.Body.String())
	}
	select {
	case <-provisioner.calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async provisioning call")
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

type provisionCall struct {
	ws   string
	name string
}

type fakeLeadProvisioner struct {
	calls chan provisionCall
	err   error
}

func (f *fakeLeadProvisioner) ProvisionForAgent(_ context.Context, ws, name string) error {
	f.calls <- provisionCall{ws: ws, name: name}
	return f.err
}
