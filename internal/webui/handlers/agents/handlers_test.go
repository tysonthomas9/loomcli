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
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

// seedLifecycleAgent creates a workspace + one lead agent so lifecycle handlers
// have a real target to transition.
func seedLifecycleAgent(t *testing.T, ctx context.Context, st store.Store, agentSvc service.AgentService, ws, name string) {
	t.Helper()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: ws, Name: ws, DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := agentSvc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey:    ws,
		Name:            name,
		RoleName:        "lead",
		RuntimeProvider: domain.RuntimeProviderDaytona,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
}

// TestHandleStartRedrivesProvisioning locks the fix for the stranded-lead bug:
// a start must re-drive provisioning so a lead orphaned by a transient
// create-time provision failure recovers instead of wedging at desired=running.
func TestHandleStartRedrivesProvisioning(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	seedLifecycleAgent(t, ctx, st, agentSvc, "TEST2", "lead-nova")
	provisioner := &fakeLeadProvisioner{calls: make(chan provisionCall, 1)}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents/lead-nova/start", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST2"))
	req.SetPathValue("name", "lead-nova")
	rr := httptest.NewRecorder()

	HandleStart(agentSvc, nil, provisioner).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rr.Code, rr.Body.String())
	}
	select {
	case got := <-provisioner.calls:
		if got.ws != "TEST2" || got.name != "lead-nova" {
			t.Fatalf("provision call = %+v, want TEST2/lead-nova", got)
		}
	case <-time.After(time.Second):
		t.Fatal("start did not re-drive provisioning")
	}
}

// TestHandleLifecycleStopDoesNotProvision guards the desired==running gate: a
// stop transition must never provision, even with a provisioner wired.
func TestHandleLifecycleStopDoesNotProvision(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	seedLifecycleAgent(t, ctx, st, agentSvc, "TEST2", "lead-nova")
	provisioner := &fakeLeadProvisioner{calls: make(chan provisionCall, 1)}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents/lead-nova/stop", nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST2"))
	req.SetPathValue("name", "lead-nova")
	rr := httptest.NewRecorder()

	handleLifecycle(agentSvc, nil, provisioner, lifecyclePatch{
		state:       domain.AgentStateStopped,
		desired:     domain.AgentDesiredStopped,
		commandType: "stop",
		status:      http.StatusOK,
		message:     "stopped",
	}).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rr.Code, rr.Body.String())
	}
	select {
	case got := <-provisioner.calls:
		t.Fatalf("stop transition provisioned: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

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

func TestHandleCreateRuntimeProvider(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantStatus   int
		wantProvider domain.RuntimeProvider
	}{
		{
			name:         "daytona",
			body:         `{"name":"lead-daytona","role_name":"lead","runtime_provider":"daytona"}`,
			wantStatus:   http.StatusCreated,
			wantProvider: domain.RuntimeProviderDaytona,
		},
		{
			name:       "workspace default",
			body:       `{"name":"lead-default","role_name":"lead","runtime_provider":""}`,
			wantStatus: http.StatusCreated,
		},
		{
			name:       "invalid",
			body:       `{"name":"lead-invalid","role_name":"lead","runtime_provider":"unknown"}`,
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
			agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents", bytes.NewBufferString(tt.body))
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST2"))
			rr := httptest.NewRecorder()

			HandleCreate(agentSvc, nil, nil).ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", rr.Code, rr.Body.String(), tt.wantStatus)
			}
			if tt.wantStatus != http.StatusCreated {
				return
			}
			var created domain.Agent
			if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
				t.Fatalf("decode created agent: %v", err)
			}
			if created.RuntimeProvider != tt.wantProvider {
				t.Fatalf("created.RuntimeProvider = %q, want %q", created.RuntimeProvider, tt.wantProvider)
			}
		})
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
	deadline := time.Now().Add(time.Second)
	for {
		agent, err := st.Agents().Get(ctx, "TEST2", "review-nova")
		if err != nil {
			t.Fatalf("get provisioned agent: %v", err)
		}
		if agent.LastProvisionOutcome == domain.LeadProvisionOutcomeFailed {
			if agent.LastProvisionError != "provision failed" {
				t.Fatalf("LastProvisionError = %q, want provision failed", agent.LastProvisionError)
			}
			if agent.LastProvisionAt == nil {
				t.Fatal("LastProvisionAt = nil, want failed-at timestamp")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("LastProvisionOutcome = %q, want failed", agent.LastProvisionOutcome)
		}
		time.Sleep(time.Millisecond)
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

// TestHandleCreateRejectsNonSelectableRuntimeProviderFromRawJSON exercises the
// boundary through the ACTUAL request path, because that is where the earlier
// reasoning went wrong.
//
// It is tempting to believe a provider is unreachable because it is absent from
// the OpenAPI schema and the UI. Neither enforces anything at runtime: this
// handler decodes the request body into a hand-written Go struct, and
// encoding/json ignores unknown fields rather than rejecting them, so the only
// thing that can refuse a provider is server-side validation. A curl command is
// the whole threat model.
//
// "exe" moved to the ACCEPTED side when it was signed off as client-selectable.
// The negative case is deliberately kept -- an undeclared provider name, which
// is what a caller probing this endpoint actually sends.
func TestHandleCreateRejectsNonSelectableRuntimeProviderFromRawJSON(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: "TEST2", Name: "TEST2", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agentSvc := svcimpl.NewAgentService(nil, nil, nil, st)
	provisioner := &fakeLeadProvisioner{calls: make(chan provisionCall, 4)}

	for _, tc := range []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "exe is accepted now that it is client-selectable",
			body:     `{"name":"lead-exe","role_name":"lead","runtime_provider":"exe"}`,
			wantCode: http.StatusCreated,
		},
		{
			name:     "unknown provider is refused",
			body:     `{"name":"lead-bogus","role_name":"lead","runtime_provider":"fly"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "daytona is still accepted",
			body:     `{"name":"lead-daytona","role_name":"lead","runtime_provider":"daytona"}`,
			wantCode: http.StatusCreated,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST2/agents",
				bytes.NewBufferString(tc.body))
			req = req.WithContext(middleware.WithWorkspace(req.Context(), "TEST2"))
			rr := httptest.NewRecorder()

			HandleCreate(agentSvc, nil, provisioner).ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Fatalf("status = %d body = %s, want %d", rr.Code, rr.Body.String(), tc.wantCode)
			}
		})
	}

	// The refused agents must not have been persisted -- a rejected create that
	// still writes the row is the failure mode that matters.
	for _, name := range []string{"lead-bogus"} {
		if _, err := st.Agents().Get(ctx, "TEST2", name); err == nil {
			t.Fatalf("agent %q was persisted despite a rejected create", name)
		}
	}
}
