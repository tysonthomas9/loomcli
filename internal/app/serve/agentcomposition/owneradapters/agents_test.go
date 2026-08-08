package owneradapters

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type agentProvisioningAgentsOperationsStub struct {
	roleCommand  agentprovisioning.EnsureRoleCommand
	agentCommand agentprovisioning.EnsureAgentCommand
	err          error
}

func (stub *agentProvisioningAgentsOperationsStub) EnsureRole(
	_ context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	stub.roleCommand = command
	return stub.err
}

func (stub *agentProvisioningAgentsOperationsStub) EnsureAgent(
	_ context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	stub.agentCommand = command
	return stub.err
}

type agentProvisioningAgentsAuthorityStub struct {
	roleWorkspace  string
	roleReason     string
	agentWorkspace string
	agentReason    string
	err            error
}

func (stub *agentProvisioningAgentsAuthorityStub) AuthorityForRole(
	_ context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	stub.roleWorkspace, stub.roleReason = workspace, reason
	return authority.SystemAuthority{}, stub.err
}

func (stub *agentProvisioningAgentsAuthorityStub) AuthorityForAgent(
	_ context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	stub.agentWorkspace, stub.agentReason = workspace, reason
	return authority.SystemAuthority{}, stub.err
}

func TestAgentProvisioningAgentsAdapterPreservesExactDurableCommands(t *testing.T) {
	operations := &agentProvisioningAgentsOperationsStub{}
	authorities := &agentProvisioningAgentsAuthorityStub{}
	adapter, err := NewAgentsAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}
	maxPriority := 8
	maxConcurrency := 3
	maxBudgetUSD := 17.5
	roleCommand := agentprovisioning.EnsureRoleCommand{
		CommandID: "provision-1:role", WorkspaceKey: "WS",
		ProvisioningID:           "provision-1",
		ProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
		Role: agentprovisioning.RoleSpec{
			Name: "docs", Kind: "worker", Prompt: "Review docs.",
			Model: "gpt-5.6-terra", TaskFilter: "status=review",
			Backend: "codex", Effort: "high", PathPatterns: []string{"docs/**"},
			Skills: []string{"documentation"}, MaxPriority: &maxPriority,
			MaxConcurrency: &maxConcurrency, ReadOnly: true,
			AllowedTools: []string{"Read", "Edit"}, DeniedTools: []string{"Shell"},
			MaxBudgetUSD: &maxBudgetUSD,
		},
	}
	if err := adapter.EnsureRole(t.Context(), roleCommand); err != nil {
		t.Fatal(err)
	}
	if authorities.roleWorkspace != "WS" ||
		authorities.roleReason != "AgentProvisioning provision-1:role" ||
		!reflect.DeepEqual(operations.roleCommand, roleCommand) {
		t.Fatalf("role authority=%+v command=%+v", authorities, operations.roleCommand)
	}

	agentCommand := agentprovisioning.EnsureAgentCommand{
		CommandID: "provision-1:agent", WorkspaceKey: "WS",
		ProvisioningID:           "provision-1",
		ProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
		Agent: agentprovisioning.AgentSpec{
			AgentID: "agt-docs", Name: "Docs", Kind: "event",
			DesiredState: "running", RoleName: "docs",
			BudgetPolicy: "daily:10", Metadata: map[string]string{"backend": "codex"},
		},
	}
	if err := adapter.EnsureAgent(t.Context(), agentCommand); err != nil {
		t.Fatal(err)
	}
	if authorities.agentWorkspace != "WS" ||
		authorities.agentReason != "AgentProvisioning provision-1:agent" ||
		!reflect.DeepEqual(operations.agentCommand, agentCommand) {
		t.Fatalf("agent authority=%+v command=%+v", authorities, operations.agentCommand)
	}
}

func TestAgentProvisioningAgentsAdapterMapsFailuresAndRejectsIncompleteComposition(t *testing.T) {
	if _, err := NewAgentsAdapter(
		nil,
		&agentProvisioningAgentsAuthorityStub{},
	); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil operations = %v", err)
	}
	if _, err := NewAgentsAdapter(
		&agentProvisioningAgentsOperationsStub{},
		nil,
	); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil authority provider = %v", err)
	}

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "invalid", err: agentprovisioning.ErrInvalid},
		{name: "conflict", err: agentprovisioning.ErrConflict},
		{name: "not found", err: agentprovisioning.ErrNotFound},
		{name: "unavailable", err: agentprovisioning.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := NewAgentsAdapter(
				&agentProvisioningAgentsOperationsStub{err: test.err},
				&agentProvisioningAgentsAuthorityStub{},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.EnsureRole(t.Context(), agentprovisioning.EnsureRoleCommand{
				CommandID: "provision-1:role", WorkspaceKey: "WS",
			})
			if !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
		})
	}

	providerFailure := errors.New("issuer unavailable")
	operations := &agentProvisioningAgentsOperationsStub{}
	adapter, err := NewAgentsAdapter(
		operations,
		&agentProvisioningAgentsAuthorityStub{err: providerFailure},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.EnsureAgent(t.Context(), agentprovisioning.EnsureAgentCommand{
		CommandID: "provision-1:agent", WorkspaceKey: "WS",
	}); !errors.Is(err, agentprovisioning.ErrUnavailable) ||
		!errors.Is(err, providerFailure) {
		t.Fatalf("authority failure = %v", err)
	}
	if operations.agentCommand.CommandID != "" {
		t.Fatal("authority failure reached durable Agent operation")
	}
}
