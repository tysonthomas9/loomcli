package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestEnsureManagedRoleCreatesOnceAndRejectsDivergentReplay(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, workspace, "agent-provisioning", ActionEnsureManagedRole)
	var persisted *Role
	createCalls := 0
	ports.getFullRole = func(_ context.Context, gotWorkspace, name string) (*Role, error) {
		if gotWorkspace != workspace || name != "docs-review" {
			t.Fatalf("GetRole(%q, %q)", gotWorkspace, name)
		}
		if persisted == nil {
			return nil, ErrNotFound
		}
		return cloneRole(persisted), nil
	}
	ports.createRole = func(_ context.Context, gotWorkspace string, definition RoleDefinition) (*Role, error) {
		createCalls++
		if gotWorkspace != workspace {
			t.Fatalf("CreateRole workspace = %q", gotWorkspace)
		}
		persisted = &Role{
			WorkspaceKey: workspace,
			CreatedAt:    now,
			UpdatedAt:    now,
			Name:         definition.Name,
			Kind:         definition.Kind,
			Description:  definition.Description,
			Prompt:       definition.Prompt,
			PromptFile:   definition.PromptFile,
			Model:        definition.Model,
			Backend:      definition.Backend,
			Effort:       definition.Effort,
			ReadOnly:     definition.ReadOnly,
		}
		return cloneRole(persisted), nil
	}
	command := EnsureRoleCommand{
		RequestID: "provision-1:role", WorkspaceKey: workspace,
		Role: RoleDefinition{
			Name: "docs-review", Kind: "worker", Description: "Review docs",
			Prompt: "Review this change.", Model: "gpt-5.6-terra",
			Backend: "codex", Effort: "high", ReadOnly: true,
		},
	}
	first, err := service.EnsureManagedRole(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.EnsureManagedRole(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if createCalls != 1 || first.Name != command.Role.Name || replayed.Prompt != command.Role.Prompt {
		t.Fatalf("createCalls=%d first=%+v replay=%+v", createCalls, first, replayed)
	}

	divergent := command
	divergent.Role.Prompt = "A different authority-bearing prompt."
	if _, err := service.EnsureManagedRole(t.Context(), auth, divergent); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent replay = %v, want ErrConflict", err)
	}
	if createCalls != 1 {
		t.Fatalf("divergent replay called create; calls=%d", createCalls)
	}
}

func TestRepairManagedRolePromptFileUsesRoleRevisionAndConvergesReplay(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, workspace, "workspace-bootstrap", ActionRepairManagedRolePromptFile)
	persisted := &Role{WorkspaceKey: workspace, Name: "plan", CreatedAt: now, UpdatedAt: now}
	ports.getFullRole = func(context.Context, string, string) (*Role, error) {
		return cloneRole(persisted), nil
	}
	updates := 0
	ports.updateRole = func(_ context.Context, mutation UpdateRoleMutation) (*Role, error) {
		updates++
		if mutation.WorkspaceKey != workspace || mutation.RoleName != "plan" ||
			!mutation.ExpectedUpdatedAt.Equal(now) || mutation.UpdatedBy != "workspace-bootstrap" ||
			mutation.Patch.PromptFile == nil || *mutation.Patch.PromptFile != "/workspaces/WS/.loom/roles/plan.md" {
			t.Fatalf("repair mutation = %+v", mutation)
		}
		persisted.PromptFile = *mutation.Patch.PromptFile
		persisted.UpdatedAt = now.Add(time.Second)
		return cloneRole(persisted), nil
	}
	command := RepairManagedRolePromptFileCommand{
		RequestID: "workspace-bootstrap:WS:plan", WorkspaceKey: workspace,
		RoleName: "plan", PromptFile: "/workspaces/WS/.loom/roles/plan.md",
	}
	first, changed, err := service.RepairManagedRolePromptFile(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || first.PromptFile != command.PromptFile || updates != 1 {
		t.Fatalf("first=%+v changed=%t updates=%d", first, changed, updates)
	}
	replay, changed, err := service.RepairManagedRolePromptFile(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if changed || replay.PromptFile != command.PromptFile || updates != 1 {
		t.Fatalf("replay=%+v changed=%t updates=%d", replay, changed, updates)
	}
}

func TestRepairManagedRolePromptFileRejectsExistingDifferentValue(t *testing.T) {
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, "WS", "workspace-bootstrap", ActionRepairManagedRolePromptFile)
	ports.getFullRole = func(context.Context, string, string) (*Role, error) {
		now := time.Now()
		return &Role{WorkspaceKey: "WS", Name: "plan", PromptFile: "/custom/plan.md", CreatedAt: now, UpdatedAt: now}, nil
	}
	if _, _, err := service.RepairManagedRolePromptFile(t.Context(), auth, RepairManagedRolePromptFileCommand{
		RequestID: "workspace-bootstrap:WS:plan", WorkspaceKey: "WS",
		RoleName: "plan", PromptFile: "/default/plan.md",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("different prompt repair = %v, want ErrConflict", err)
	}
}

func TestEnsureManagedAgentCreatesOnceWithBackendMetadata(t *testing.T) {
	const workspace = "WS"
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	auth := issueSystem(t, issuer, workspace, "agent-provisioning", ActionEnsureManagedAgent)
	ports.getRole = func(context.Context, string, string) (*RoleReference, error) {
		return &RoleReference{WorkspaceKey: workspace, RoleName: "docs-review"}, nil
	}
	var persisted *Agent
	createCalls := 0
	ports.getAgent = func(context.Context, string, string) (*Agent, error) {
		if persisted == nil {
			return nil, ErrNotFound
		}
		return cloneAgent(persisted), nil
	}
	ports.createAgent = func(_ context.Context, mutation CreateAgentMutation) (*Agent, error) {
		createCalls++
		persisted = &Agent{
			WorkspaceKey: mutation.WorkspaceKey, AgentID: mutation.AgentID,
			GenerationID: "00112233445566778899aabbccddeeff",
			Name:         mutation.Name, Kind: mutation.Kind, Behavior: mutation.Behavior,
			DesiredState: mutation.DesiredState, PlacementPolicy: mutation.PlacementPolicy,
			MaxInstances: mutation.MaxInstances, RestartPolicy: mutation.RestartPolicy,
			BudgetPolicy: mutation.BudgetPolicy, Metadata: cloneStringMap(mutation.Metadata),
			CreatedBy: mutation.CreatedBy, CreatedAt: now, UpdatedAt: now,
		}
		return cloneAgent(persisted), nil
	}
	command := EnsureAgentCommand{
		RequestID: "provision-1:agent",
		CreateAgentCommand: CreateAgentCommand{
			WorkspaceKey: workspace, AgentID: "agt-docs", Name: "Docs Review",
			Kind: AgentKindEvent, Behavior: BehaviorReference{RoleName: "docs-review"},
			DesiredState: DesiredRunning, Metadata: map[string]string{"backend": "codex"},
		},
	}
	first, err := service.EnsureManagedAgent(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.EnsureManagedAgent(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if createCalls != 1 || first.CreatedBy != "agent-provisioning" ||
		replayed.Metadata["backend"] != "codex" {
		t.Fatalf("createCalls=%d first=%+v replay=%+v", createCalls, first, replayed)
	}
	command.Metadata["backend"] = "mutated-by-caller"
	if persisted.Metadata["backend"] != "codex" {
		t.Fatal("EnsureManagedAgent leaked caller metadata into persistence")
	}

	divergent := command
	divergent.Metadata = map[string]string{"backend": "claude"}
	if _, err := service.EnsureManagedAgent(t.Context(), auth, divergent); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent metadata replay = %v, want ErrConflict", err)
	}
}

func TestManagedProvisioningCommandsRequireExactSystemAction(t *testing.T) {
	ports := &fakePorts{}
	service, issuer := newTestService(t, ports)
	wrong := issueSystem(t, issuer, "WS", "agent-provisioning", ActionAcquireOwnership)

	if _, err := service.EnsureManagedRole(t.Context(), wrong, EnsureRoleCommand{
		RequestID: "provision-1:role", WorkspaceKey: "WS",
		Role: RoleDefinition{Name: "docs"},
	}); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong action EnsureManagedRole = %v, want admission denied", err)
	}
	if _, _, err := service.RepairManagedRolePromptFile(t.Context(), wrong, RepairManagedRolePromptFileCommand{
		RequestID: "provision-1:repair", WorkspaceKey: "WS", RoleName: "docs", PromptFile: "/docs.md",
	}); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong action RepairManagedRolePromptFile = %v, want admission denied", err)
	}
}
