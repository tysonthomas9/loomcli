package agentsmanagement

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type identityAPIStub struct {
	create    agents.CreateAgentCommand
	update    agents.UpdateAgentCommand
	archive   agents.ArchiveAgentCommand
	desired   agents.SetDesiredStateCommand
	lifecycle agents.ApplyLifecycleCommand
	calls     int
}

func TestWriteMappedErrorPreservesGenericAdmissionForbiddenContract(t *testing.T) {
	for _, reason := range []authority.DenialReason{
		authority.DenialInvalidAuthority,
		authority.DenialExpired,
	} {
		t.Run(string(reason), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeMappedError(recorder, &authority.AdmissionError{Reason: reason})
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body = %s", recorder.Code, recorder.Body.String())
			}
			if body := recorder.Body.String(); !strings.Contains(body, `"code":"forbidden"`) {
				t.Fatalf("body = %s, want forbidden code", body)
			}
		})
	}
}

func (stub *identityAPIStub) GetAgent(_ context.Context, workspace, agentID string) (*agents.Agent, error) {
	return testAgent(workspace, agentID, agents.DesiredRunning), nil
}

func (stub *identityAPIStub) ListAgents(_ context.Context, workspace string, _ agents.AgentFilter) ([]*agents.Agent, error) {
	return []*agents.Agent{testAgent(workspace, "docs", agents.DesiredRunning)}, nil
}

func (stub *identityAPIStub) CreateAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.CreateAgentCommand,
) (*agents.Agent, error) {
	stub.calls++
	stub.create = command
	return testAgent(command.WorkspaceKey, command.AgentID, command.DesiredState), nil
}

func (stub *identityAPIStub) UpdateAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.UpdateAgentCommand,
) (*agents.Agent, error) {
	stub.calls++
	stub.update = command
	record := testAgent(command.WorkspaceKey, command.AgentID, agents.DesiredRunning)
	record.UpdatedAt = command.ExpectedUpdatedAt.Add(time.Second)
	if command.Patch.ProfileName != nil {
		record.ProfileName = *command.Patch.ProfileName
	}
	return record, nil
}

func (stub *identityAPIStub) ArchiveAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.ArchiveAgentCommand,
) (*agents.Agent, error) {
	stub.calls++
	stub.archive = command
	record := testAgent(command.WorkspaceKey, command.AgentID, agents.DesiredStopped)
	deleted := command.ExpectedUpdatedAt.Add(time.Second)
	record.DeletedAt = &deleted
	record.UpdatedAt = deleted
	return record, nil
}

func (stub *identityAPIStub) SetDesiredState(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.SetDesiredStateCommand,
) (*agents.Agent, error) {
	stub.calls++
	stub.desired = command
	record := testAgent(command.WorkspaceKey, command.AgentID, command.DesiredState)
	record.UpdatedAt = command.ExpectedUpdatedAt.Add(time.Second)
	return record, nil
}

func (*identityAPIStub) SetDesiredStateOwned(
	context.Context,
	authority.SystemAuthority,
	agents.OwnershipProof,
	agents.SetDesiredStateOwnedCommand,
) (*agents.Agent, error) {
	panic("unexpected owned desired-state mutation")
}

func (stub *identityAPIStub) ApplyLifecycle(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agents.ApplyLifecycleCommand,
) (*agents.LifecycleResult, error) {
	stub.calls++
	stub.lifecycle = command
	record := testAgent(command.WorkspaceKey, command.AgentID, agents.DesiredPaused)
	if command.Action == agents.LifecycleEnable {
		record.DesiredState = agents.DesiredRunning
	}
	record.UpdatedAt = command.ExpectedUpdatedAt.Add(time.Second)
	return &agents.LifecycleResult{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		IdempotencyKey: command.IdempotencyKey, Action: command.Action,
		Agent: record, CommittedAt: record.UpdatedAt,
	}, nil
}

type operatorResolverStub struct {
	workspace string
	action    authority.Action
	err       error
}

func (stub *operatorResolverStub) ResolveOperatorAuthority(
	_ *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	stub.workspace = workspace
	stub.action = action
	return authority.OperatorAuthority{}, stub.err
}

func TestAgentIdentityCreateDerivesWorkspaceAndAction(t *testing.T) {
	api := &identityAPIStub{}
	resolver := &operatorResolverStub{}
	mux := http.NewServeMux()
	New(Config{Agents: api, Authority: resolver}).Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/agent-identities",
		strings.NewReader(`{"agent_id":"docs","name":"Docs","kind":"maintenance","behavior":{"role_name":"docs"},"desired_state":"running","max_instances":1}`),
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if api.calls != 1 || api.create.WorkspaceKey != "WS" || api.create.AgentID != "docs" {
		t.Fatalf("create command/calls = %+v/%d", api.create, api.calls)
	}
	if resolver.workspace != "WS" || resolver.action != agents.ActionCreateAgent {
		t.Fatalf("authority scope = %q/%q", resolver.workspace, resolver.action)
	}
}

func TestAgentIdentityDesiredStateAndArchivePreserveCAS(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		path       string
		body       string
		wantAction authority.Action
		assert     func(*testing.T, *identityAPIStub)
	}{
		{
			name:       "desired state",
			path:       "/api/workspaces/WS/agent-identities/docs/desired-state",
			body:       `{"expected_state":"running","desired_state":"stopped","expected_updated_at":"2026-07-30T12:00:00Z"}`,
			wantAction: agents.ActionSetDesiredState,
			assert: func(t *testing.T, api *identityAPIStub) {
				t.Helper()
				if api.desired.AgentID != "docs" || api.desired.ExpectedState != agents.DesiredRunning ||
					api.desired.DesiredState != agents.DesiredStopped ||
					!api.desired.ExpectedUpdatedAt.Equal(now) {
					t.Fatalf("desired command = %+v", api.desired)
				}
			},
		},
		{
			name:       "archive",
			path:       "/api/workspaces/WS/agent-identities/docs/archive",
			body:       `{"expected_updated_at":"2026-07-30T12:00:00Z"}`,
			wantAction: agents.ActionArchiveAgent,
			assert: func(t *testing.T, api *identityAPIStub) {
				t.Helper()
				if api.archive.AgentID != "docs" || !api.archive.ExpectedUpdatedAt.Equal(now) {
					t.Fatalf("archive command = %+v", api.archive)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &identityAPIStub{}
			resolver := &operatorResolverStub{}
			mux := http.NewServeMux()
			New(Config{Agents: api, Authority: resolver}).Register(mux)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request = withCanonicalWorkspace(request, "WS", "WS")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
			}
			if resolver.workspace != "WS" || resolver.action != test.wantAction {
				t.Fatalf("authority scope = %q/%q", resolver.workspace, resolver.action)
			}
			test.assert(t, api)
		})
	}
}

func TestAgentIdentityLifecycleDerivesAuthorityAndPreservesReceiptCoordinates(t *testing.T) {
	api := &identityAPIStub{}
	resolver := &operatorResolverStub{}
	mux := http.NewServeMux()
	New(Config{Agents: api, Authority: resolver}).Register(mux)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/agent-identities/docs/lifecycle",
		strings.NewReader(`{"action":"disable","expected_updated_at":"2026-07-30T12:00:00Z","idempotency_key":"agentdef-disable-1"}`),
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if api.calls != 1 ||
		api.lifecycle.WorkspaceKey != "WS" ||
		api.lifecycle.AgentID != "docs" ||
		api.lifecycle.Action != agents.LifecycleDisable ||
		api.lifecycle.IdempotencyKey != "agentdef-disable-1" ||
		!api.lifecycle.ExpectedUpdatedAt.Equal(now) {
		t.Fatalf("lifecycle command/calls = %+v/%d", api.lifecycle, api.calls)
	}
	if resolver.workspace != "WS" || resolver.action != agents.ActionApplyLifecycle {
		t.Fatalf("authority scope = %q/%q", resolver.workspace, resolver.action)
	}
}

func TestAgentIdentityUpdateDerivesIdentityAuthorityAndPreservesCAS(t *testing.T) {
	api := &identityAPIStub{}
	resolver := &operatorResolverStub{}
	mux := http.NewServeMux()
	New(Config{Agents: api, Authority: resolver}).Register(mux)

	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/workspaces/WS/agent-identities/docs",
		strings.NewReader(`{"expected_updated_at":"2026-07-30T12:00:00Z","patch":{"profile_name":"reviewer","event_sources":["issue.updated"]}}`),
	)
	request = withCanonicalWorkspace(request, "WS", "WS")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if api.calls != 1 || api.update.WorkspaceKey != "WS" || api.update.AgentID != "docs" ||
		!api.update.ExpectedUpdatedAt.Equal(now) {
		t.Fatalf("update command/calls = %+v/%d", api.update, api.calls)
	}
	if api.update.Patch.ProfileName == nil || *api.update.Patch.ProfileName != "reviewer" ||
		api.update.Patch.EventSources == nil || len(*api.update.Patch.EventSources) != 1 ||
		(*api.update.Patch.EventSources)[0] != "issue.updated" {
		t.Fatalf("update patch = %+v", api.update.Patch)
	}
	if resolver.workspace != "WS" || resolver.action != agents.ActionUpdateAgent {
		t.Fatalf("authority scope = %q/%q", resolver.workspace, resolver.action)
	}
}

func TestAgentIdentityRoutesRejectCallerWorkspaceAndFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		body   string
		status int
	}{
		{
			name:   "caller workspace field",
			config: Config{Agents: &identityAPIStub{}, Authority: &operatorResolverStub{}},
			body:   `{"workspace_key":"OTHER","agent_id":"docs","name":"Docs","kind":"maintenance","behavior":{"role_name":"docs"},"desired_state":"running","max_instances":1}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "authority unavailable",
			config: Config{Agents: &identityAPIStub{}},
			body:   `{"agent_id":"docs","name":"Docs","kind":"maintenance","behavior":{"role_name":"docs"},"desired_state":"running","max_instances":1}`,
			status: http.StatusServiceUnavailable,
		},
		{
			name:   "capability unavailable",
			config: Config{Authority: &operatorResolverStub{}},
			body:   `{"agent_id":"docs","name":"Docs","kind":"maintenance","behavior":{"role_name":"docs"},"desired_state":"running","max_instances":1}`,
			status: http.StatusServiceUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux := http.NewServeMux()
			New(test.config).Register(mux)
			request := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agent-identities", strings.NewReader(test.body))
			request = withCanonicalWorkspace(request, "WS", "WS")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status/body = %d/%s, want %d", response.Code, response.Body.String(), test.status)
			}
		})
	}
}

func TestAgentIdentityRoutesUseCanonicalWorkspaceAndFailClosedWithoutResolution(t *testing.T) {
	body := `{"agent_id":"docs","name":"Docs","kind":"maintenance","behavior":{"role_name":"docs"},"desired_state":"running","max_instances":1}`

	t.Run("alias resolves to canonical workspace", func(t *testing.T) {
		api := &identityAPIStub{}
		resolver := &operatorResolverStub{}
		mux := http.NewServeMux()
		New(Config{Agents: api, Authority: resolver}).Register(mux)
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/ALIAS/agent-identities",
			strings.NewReader(body),
		)
		request = withCanonicalWorkspace(request, "ALIAS", "CANONICAL")
		response := httptest.NewRecorder()

		mux.ServeHTTP(response, request)

		if response.Code != http.StatusCreated {
			t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
		}
		if api.create.WorkspaceKey != "CANONICAL" || resolver.workspace != "CANONICAL" {
			t.Fatalf("command/authority workspaces = %q/%q", api.create.WorkspaceKey, resolver.workspace)
		}
	})

	t.Run("missing canonical workspace fails closed", func(t *testing.T) {
		api := &identityAPIStub{}
		resolver := &operatorResolverStub{}
		mux := http.NewServeMux()
		New(Config{Agents: api, Authority: resolver}).Register(mux)
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/workspaces/WS/agent-identities",
			strings.NewReader(body),
		)
		response := httptest.NewRecorder()

		mux.ServeHTTP(response, request)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
		}
		if api.calls != 0 || resolver.workspace != "" {
			t.Fatalf("capability/authority invoked = %d/%q", api.calls, resolver.workspace)
		}
	})
}

func withCanonicalWorkspace(request *http.Request, requested, canonical string) *http.Request {
	ref := middleware.WorkspaceRef{RequestedID: requested, CanonicalID: canonical}
	return request.WithContext(middleware.WithWorkspaceRef(request.Context(), ref))
}

func testAgent(workspace, agentID string, desired agents.DesiredState) *agents.Agent {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return &agents.Agent{
		WorkspaceKey: workspace, AgentID: agentID, Name: agentID,
		Kind: agents.AgentKindMaintenance, Behavior: agents.BehaviorReference{RoleName: "docs"},
		DesiredState: desired, MaxInstances: 1, CreatedAt: now, UpdatedAt: now,
	}
}
