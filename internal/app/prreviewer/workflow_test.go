package prreviewer

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type capabilityStub struct {
	roleCommand  agents.EnsureRoleCommand
	agentCommand agents.EnsureAgentCommand
	roleAuth     authority.SystemAuthority
	agentAuth    authority.SystemAuthority
	roleErr      error
	agentErr     error
}

func (stub *capabilityStub) EnsureManagedRole(
	_ context.Context,
	auth authority.SystemAuthority,
	command agents.EnsureRoleCommand,
) (*agents.Role, error) {
	stub.roleAuth = auth
	stub.roleCommand = command
	if stub.roleErr != nil {
		return nil, stub.roleErr
	}
	return &agents.Role{WorkspaceKey: command.WorkspaceKey, Name: command.Role.Name}, nil
}

func (stub *capabilityStub) EnsureManagedAgent(
	_ context.Context,
	auth authority.SystemAuthority,
	command agents.EnsureAgentCommand,
) (*agents.Agent, error) {
	stub.agentAuth = auth
	stub.agentCommand = command
	if stub.agentErr != nil {
		return nil, stub.agentErr
	}
	return &agents.Agent{
		WorkspaceKey: command.WorkspaceKey,
		AgentID:      command.AgentID,
		Name:         command.Name,
		Kind:         command.Kind,
		Behavior:     command.Behavior,
		DesiredState: command.DesiredState,
		MaxInstances: command.MaxInstances,
	}, nil
}

type authorityProviderStub struct {
	role  authority.SystemAuthority
	agent authority.SystemAuthority
}

func (stub authorityProviderStub) AuthorityForReviewerRole(
	context.Context,
	string,
	string,
) (authority.SystemAuthority, error) {
	return stub.role, nil
}

func (stub authorityProviderStub) AuthorityForReviewerAgent(
	context.Context,
	string,
	string,
) (authority.SystemAuthority, error) {
	return stub.agent, nil
}

func TestEnsureReviewerUsesOnlyExactManagedAgentsCommands(t *testing.T) {
	capability := &capabilityStub{}
	workflow, err := New(capability, authorityProviderStub{})
	if err != nil {
		t.Fatal(err)
	}

	result, err := workflow.EnsureReviewer(t.Context(), EnsureCommand{
		WorkspaceKey: "WS",
		AgentID:      "review-octo-repo-abc12345-pr-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.RoleCommitted || result.Agent == nil {
		t.Fatalf("result = %#v", result)
	}
	if got := capability.roleCommand; got.RequestID != "pr-reviewer-role/WS" ||
		got.WorkspaceKey != "WS" ||
		got.Role.Name != RoleName ||
		got.Role.Kind != "interactive" ||
		got.Role.Description != RoleDescription ||
		got.Role.PromptFile != RolePromptFile ||
		got.Role.Prompt != "" {
		t.Fatalf("Role command = %#v", got)
	}
	if got := capability.agentCommand; got.RequestID != "pr-reviewer-agent/review-octo-repo-abc12345-pr-7" ||
		got.WorkspaceKey != "WS" ||
		got.AgentID != "review-octo-repo-abc12345-pr-7" ||
		got.Name != got.AgentID ||
		got.Kind != agents.AgentKindSupport ||
		got.Behavior != (agents.BehaviorReference{RoleName: RoleName}) ||
		got.DesiredState != agents.DesiredRunning ||
		got.MaxInstances != 1 ||
		got.Metadata[agents.MetadataRoleKind] != "interactive" ||
		got.Metadata[agents.MetadataBackend] != "" ||
		got.Metadata[agents.MetadataFallbackBackends] != "[]" ||
		got.Metadata[agents.MetadataRepos] != "[]" ||
		got.Metadata[agents.MetadataRepoGroups] != "[]" ||
		got.Metadata[agents.MetadataCrossRepo] != "false" ||
		got.Metadata[agents.MetadataAuto] != "false" ||
		len(got.Metadata) != 7 {
		t.Fatalf("Agent command = %#v", got)
	}
}

func TestEnsureReviewerRecordsDurableRoleWhenAgentStepFails(t *testing.T) {
	capability := &capabilityStub{agentErr: agents.ErrUnavailable}
	workflow, err := New(capability, authorityProviderStub{})
	if err != nil {
		t.Fatal(err)
	}

	result, err := workflow.EnsureReviewer(t.Context(), EnsureCommand{
		WorkspaceKey: "WS",
		AgentID:      "review-octo-repo-abc12345-pr-7",
	})
	if !errors.Is(err, agents.ErrUnavailable) {
		t.Fatalf("error = %v, want Agents unavailable", err)
	}
	if result == nil || !result.RoleCommitted || result.Agent != nil {
		t.Fatalf("result = %#v, want independently committed Role", result)
	}
}

func TestEnsureReviewerValidatesAgentBeforeCommittingSharedRole(t *testing.T) {
	capability := &capabilityStub{}
	workflow, err := New(capability, authorityProviderStub{})
	if err != nil {
		t.Fatal(err)
	}

	if result, err := workflow.EnsureReviewer(t.Context(), EnsureCommand{
		WorkspaceKey: "WS",
		AgentID:      "bad:reviewer",
	}); result != nil || !errors.Is(err, agents.ErrInvalid) {
		t.Fatalf("EnsureReviewer = (%#v, %v), want invalid", result, err)
	}
	if capability.roleCommand.WorkspaceKey != "" || capability.agentCommand.WorkspaceKey != "" {
		t.Fatal("invalid reviewer committed an Agents command")
	}
}

func TestNewFailsClosedWithoutOwnerCapabilityOrAuthorityProvider(t *testing.T) {
	if workflow, err := New(nil, authorityProviderStub{}); workflow != nil ||
		!errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(nil capability) = (%#v, %v)", workflow, err)
	}
	if workflow, err := New(&capabilityStub{}, nil); workflow != nil ||
		!errors.Is(err, ErrUnavailable) {
		t.Fatalf("New(nil authorities) = (%#v, %v)", workflow, err)
	}
	var workflow *Workflow
	if result, err := workflow.EnsureReviewer(t.Context(), EnsureCommand{
		WorkspaceKey: "WS",
		AgentID:      "reviewer",
	}); result != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil workflow EnsureReviewer = (%#v, %v)", result, err)
	}
}
