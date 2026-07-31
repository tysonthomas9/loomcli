package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type capabilityStub struct {
	roleCommand  agentprovisioning.EnsureRoleCommand
	agentCommand agentprovisioning.EnsureAgentCommand
	err          error
}

func (stub *capabilityStub) EnsureRole(
	_ context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	stub.roleCommand = command
	return stub.err
}

func (stub *capabilityStub) EnsureAgent(
	_ context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	stub.agentCommand = command
	return stub.err
}

type authorityStub struct {
	roleWorkspace  string
	roleReason     string
	agentWorkspace string
	agentReason    string
	err            error
}

func (stub *authorityStub) AuthorityForRole(
	_ context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	stub.roleWorkspace, stub.roleReason = workspace, reason
	return authority.SystemAuthority{}, stub.err
}

func (stub *authorityStub) AuthorityForAgent(
	_ context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	stub.agentWorkspace, stub.agentReason = workspace, reason
	return authority.SystemAuthority{}, stub.err
}

func TestAdapterMapsProvisioningIntentToExactAgentsCommands(t *testing.T) {
	capability := &capabilityStub{}
	authorities := &authorityStub{}
	adapter, err := New(capability, authorities)
	if err != nil {
		t.Fatal(err)
	}
	maxPriority := 8
	maxConcurrency := 3
	maxBudgetUSD := 17.5
	if err := adapter.EnsureRole(t.Context(), agentprovisioning.EnsureRoleCommand{
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
	}); err != nil {
		t.Fatal(err)
	}
	if authorities.roleWorkspace != "WS" ||
		authorities.roleReason != "AgentProvisioning provision-1:role" ||
		capability.roleCommand.CommandID != "provision-1:role" ||
		capability.roleCommand.ProvisioningID != "provision-1" ||
		capability.roleCommand.ProvisioningGenerationID != "0123456789abcdef0123456789abcdef" ||
		capability.roleCommand.Role.Prompt != "Review docs." ||
		capability.roleCommand.Role.TaskFilter != "status=review" ||
		!reflect.DeepEqual(capability.roleCommand.Role.AllowedTools, []string{"Read", "Edit"}) ||
		capability.roleCommand.Role.MaxConcurrency == nil ||
		*capability.roleCommand.Role.MaxConcurrency != 3 ||
		!capability.roleCommand.Role.ReadOnly {
		t.Fatalf("role authority=%+v command=%+v", authorities, capability.roleCommand)
	}

	if err := adapter.EnsureAgent(t.Context(), agentprovisioning.EnsureAgentCommand{
		CommandID: "provision-1:agent", WorkspaceKey: "WS",
		ProvisioningID:           "provision-1",
		ProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
		Agent: agentprovisioning.AgentSpec{
			AgentID: "agt-docs", Name: "Docs", Kind: "event",
			DesiredState: "running", RoleName: "docs",
			BudgetPolicy: "daily:10", Metadata: map[string]string{"backend": "codex"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	got := capability.agentCommand
	if authorities.agentWorkspace != "WS" ||
		authorities.agentReason != "AgentProvisioning provision-1:agent" ||
		got.CommandID != "provision-1:agent" ||
		got.ProvisioningID != "provision-1" ||
		got.Agent.RoleName != "docs" ||
		got.Agent.Metadata["backend"] != "codex" {
		t.Fatalf("agent authority=%+v command=%+v", authorities, got)
	}
}

func TestAdapterMapsAgentsFailuresAndRejectsIncompleteComposition(t *testing.T) {
	if _, err := New(nil, &authorityStub{}); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("New nil capability = %v", err)
	}
	if _, err := New(&capabilityStub{}, nil); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("New nil authority provider = %v", err)
	}

	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "invalid", err: agentprovisioning.ErrInvalid, want: agentprovisioning.ErrInvalid},
		{name: "conflict", err: agentprovisioning.ErrConflict, want: agentprovisioning.ErrConflict},
		{name: "not found", err: agentprovisioning.ErrNotFound, want: agentprovisioning.ErrNotFound},
		{name: "unavailable", err: agentprovisioning.ErrUnavailable, want: agentprovisioning.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := New(&capabilityStub{err: test.err}, &authorityStub{})
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.EnsureRole(t.Context(), agentprovisioning.EnsureRoleCommand{
				CommandID: "provision-1:role", WorkspaceKey: "WS",
				Role: agentprovisioning.RoleSpec{Name: "docs"},
			})
			if !errors.Is(err, test.want) || !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v and original", err, test.want)
			}
		})
	}

	providerFailure := errors.New("issuer unavailable")
	adapter, err := New(&capabilityStub{}, &authorityStub{err: providerFailure})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.EnsureAgent(t.Context(), agentprovisioning.EnsureAgentCommand{
		CommandID: "provision-1:agent", WorkspaceKey: "WS",
	}); !errors.Is(err, agentprovisioning.ErrUnavailable) ||
		!errors.Is(err, providerFailure) {
		t.Fatalf("authority failure = %v", err)
	}
}
