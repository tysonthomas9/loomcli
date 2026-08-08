package agents

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type canonicalInteractiveCreatorStub struct {
	roles        map[string]*agentsmodule.Role
	roleCommand  agentsmodule.CreateRoleCommand
	agentCommand agentsmodule.CreateAgentCommand
}

func (stub *canonicalInteractiveCreatorStub) GetRole(
	_ context.Context,
	_, name string,
) (*agentsmodule.Role, error) {
	role := stub.roles[name]
	if role == nil {
		return nil, agentsmodule.ErrNotFound
	}
	copy := *role
	return &copy, nil
}

func (stub *canonicalInteractiveCreatorStub) CreateRole(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.CreateRoleCommand,
) (*agentsmodule.Role, error) {
	stub.roleCommand = command
	if stub.roles == nil {
		stub.roles = make(map[string]*agentsmodule.Role)
	}
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	role := &agentsmodule.Role{
		WorkspaceKey: command.WorkspaceKey, Name: command.Role.Name,
		Kind: command.Role.Kind, Description: command.Role.Description,
		Prompt: command.Role.Prompt, PromptFile: command.Role.PromptFile,
		Backend: command.Role.Backend, CreatedAt: now, UpdatedAt: now,
	}
	stub.roles[role.Name] = role
	copy := *role
	return &copy, nil
}

func (stub *canonicalInteractiveCreatorStub) CreateAgent(
	_ context.Context,
	_ authority.OperatorAuthority,
	command agentsmodule.CreateAgentCommand,
) (*agentsmodule.Agent, error) {
	stub.agentCommand = command
	now := time.Date(2026, 8, 2, 12, 0, 1, 0, time.UTC)
	return &agentsmodule.Agent{
		WorkspaceKey: command.WorkspaceKey, AgentID: command.AgentID,
		GenerationID: "0123456789abcdef0123456789abcdef",
		Name:         command.Name, Kind: command.Kind, Behavior: command.Behavior,
		DesiredState: command.DesiredState, PlacementPolicy: command.PlacementPolicy,
		MaxInstances: command.MaxInstances, Metadata: command.Metadata,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func TestCreateInteractiveAgentUsesCanonicalRoleAndIdentity(t *testing.T) {
	store := newAgentRecordStore(t)
	creator := &canonicalInteractiveCreatorStub{}
	module := newTestAgentsModule(nil, store, nil, agentRecordTestWS)
	module.agentIdentityCreator = creator
	mux := http.NewServeMux()
	module.Register(mux)

	response := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents", `{
		"name":"review-lead","role_name":"review-lead","kind":"interactive",
		"prompt":"Review carefully","backend":"codex","repos":["loom"],
		"repo_groups":["core"],"cross_repo":true
	}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if creator.roleCommand.Role.Name != "review-lead" ||
		creator.roleCommand.Role.Kind != "interactive" ||
		creator.roleCommand.Role.Prompt != "Review carefully" {
		t.Fatalf("role command = %#v", creator.roleCommand)
	}
	command := creator.agentCommand
	if command.AgentID != "review-lead" || command.Name != "review-lead" ||
		command.Kind != agentsmodule.AgentKindLead ||
		command.Behavior.RoleName != "review-lead" ||
		command.DesiredState != agentsmodule.DesiredRunning ||
		command.PlacementPolicy != "interactive" {
		t.Fatalf("agent command = %#v", command)
	}
	runtime, err := agentsmodule.ParseRuntimeMetadata(command.Metadata)
	if err != nil {
		t.Fatalf("runtime metadata: %v", err)
	}
	if runtime.RoleKind != "interactive" || runtime.Backend != "codex" ||
		len(runtime.Repos) != 1 || runtime.Repos[0] != "loom" ||
		len(runtime.RepoGroups) != 1 || runtime.RepoGroups[0] != "core" ||
		!runtime.CrossRepo || runtime.Auto {
		t.Fatalf("runtime metadata = %#v", runtime)
	}
}

func TestCreateInteractiveAgentRejectsNonInteractiveExistingRole(t *testing.T) {
	store := newAgentRecordStore(t)
	creator := &canonicalInteractiveCreatorStub{roles: map[string]*agentsmodule.Role{
		"task": {WorkspaceKey: agentRecordTestWS, Name: "task", Kind: "worker"},
	}}
	module := newTestAgentsModule(nil, store, nil, agentRecordTestWS)
	module.agentIdentityCreator = creator
	mux := http.NewServeMux()
	module.Register(mux)

	response := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents", `{
		"name":"reviewer","role_name":"task","kind":"interactive"
	}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if creator.agentCommand.AgentID != "" {
		t.Fatalf("agent creation ran after role conflict: %#v", creator.agentCommand)
	}
}

func TestCreateAgentRejectsRetiredSupervisedKind(t *testing.T) {
	store := newAgentRecordStore(t)
	creator := &canonicalInteractiveCreatorStub{}
	module := newTestAgentsModule(nil, store, nil, agentRecordTestWS)
	module.agentIdentityCreator = creator
	mux := http.NewServeMux()
	module.Register(mux)

	response := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents", `{
		"name":"legacy","role_name":"task","kind":"supervised"
	}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if creator.agentCommand.AgentID != "" || creator.roleCommand.Role.Name != "" {
		t.Fatalf("retired create mutated canonical state: agent=%#v role=%#v", creator.agentCommand, creator.roleCommand)
	}
}

func TestCreateInteractiveAgentPropagatesCanonicalNotFound(t *testing.T) {
	creator := &canonicalInteractiveCreatorStub{}
	if _, err := creator.GetRole(t.Context(), "WS", "missing"); !errors.Is(err, agentsmodule.ErrNotFound) {
		t.Fatalf("GetRole error = %v", err)
	}
}

func TestParseCanonicalInteractiveAgentCreateUsesBoundedExactOnePolicy(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"name":"lead","role_name":"lead","unexpected":true}`},
		{name: "trailing JSON", body: `{"name":"lead","role_name":"lead"} {}`},
		{
			name: "oversized",
			body: `{"name":"` + strings.Repeat("x", handler.MaxRequestBody) + `","role_name":"lead"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, message := parseCanonicalInteractiveAgentCreate([]byte(test.body), "WS"); message != "invalid request body" {
				t.Fatalf("validation message = %q, want invalid request body", message)
			}
		})
	}
}
