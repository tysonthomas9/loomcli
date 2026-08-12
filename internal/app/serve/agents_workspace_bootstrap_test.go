package serve

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type workspaceBootstrapAgentsStub struct {
	agents.API
	ensureAuth authority.SystemAuthority
	repairAuth authority.SystemAuthority
}

func (stub *workspaceBootstrapAgentsStub) EnsureManagedRole(
	_ context.Context,
	auth authority.SystemAuthority,
	command agents.EnsureRoleCommand,
) (*agents.Role, error) {
	stub.ensureAuth = auth
	return &agents.Role{WorkspaceKey: command.WorkspaceKey, Name: command.Role.Name}, nil
}

func (stub *workspaceBootstrapAgentsStub) RepairManagedRolePromptFile(
	_ context.Context,
	auth authority.SystemAuthority,
	command agents.RepairManagedRolePromptFileCommand,
) (*agents.Role, bool, error) {
	stub.repairAuth = auth
	return &agents.Role{
		WorkspaceKey: command.WorkspaceKey,
		Name:         command.RoleName,
		PromptFile:   command.PromptFile,
	}, true, nil
}

func TestAgentsCapabilityIssuesExactWorkspaceBootstrapAuthority(t *testing.T) {
	stub := &workspaceBootstrapAgentsStub{}
	capability := &AgentsCapability{api: stub, issuer: authority.NewIssuer()}
	if _, err := capability.EnsureRole(t.Context(), agents.EnsureRoleCommand{
		RequestID: "workspace-bootstrap:WS:plan", WorkspaceKey: "WS",
		Role: agents.RoleDefinition{Name: "plan"},
	}); err != nil {
		t.Fatal(err)
	}
	if stub.ensureAuth.Subject() != "workspace-bootstrap" ||
		stub.ensureAuth.Workspace() != "WS" ||
		stub.ensureAuth.Action() != agents.ActionEnsureManagedRole {
		t.Fatalf("ensure authority = subject=%q workspace=%q action=%q",
			stub.ensureAuth.Subject(), stub.ensureAuth.Workspace(), stub.ensureAuth.Action())
	}
	if _, changed, err := capability.RepairRolePromptFile(t.Context(), agents.RepairManagedRolePromptFileCommand{
		RequestID: "builtin-role-prompt-backfill:WS:plan", WorkspaceKey: "WS",
		RoleName: "plan", PromptFile: "/workspaces/WS/.loom/roles/plan.md",
	}); err != nil || !changed {
		t.Fatalf("repair changed=%t error=%v", changed, err)
	}
	if stub.repairAuth.Subject() != "workspace-bootstrap" ||
		stub.repairAuth.Workspace() != "WS" ||
		stub.repairAuth.Action() != agents.ActionRepairManagedRolePromptFile {
		t.Fatalf("repair authority = subject=%q workspace=%q action=%q",
			stub.repairAuth.Subject(), stub.repairAuth.Workspace(), stub.repairAuth.Action())
	}
}
