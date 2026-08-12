package serve

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type agentProvisioningOwnerOperationsStub struct {
	roleCommand    agentprovisioning.EnsureRoleCommand
	agentCommand   agentprovisioning.EnsureAgentCommand
	bindingCommand agentprovisioning.EnsureBindingCommand
	grantCommand   agentprovisioning.EnsureGrantCommand
	err            error
}

func (stub *agentProvisioningOwnerOperationsStub) EnsureRole(
	_ context.Context,
	command agentprovisioning.EnsureRoleCommand,
) error {
	stub.roleCommand = command
	return stub.err
}

func (stub *agentProvisioningOwnerOperationsStub) EnsureAgent(
	_ context.Context,
	command agentprovisioning.EnsureAgentCommand,
) error {
	stub.agentCommand = command
	return stub.err
}

func (stub *agentProvisioningOwnerOperationsStub) EnsureBinding(
	_ context.Context,
	command agentprovisioning.EnsureBindingCommand,
) error {
	stub.bindingCommand = command
	return stub.err
}

func (stub *agentProvisioningOwnerOperationsStub) EnsureGrant(
	_ context.Context,
	command agentprovisioning.EnsureGrantCommand,
) error {
	stub.grantCommand = command
	return stub.err
}

type agentProvisioningOwnerAuthorityStub struct {
	action    string
	workspace string
	reason    string
	err       error
}

func (stub *agentProvisioningOwnerAuthorityStub) record(
	action, workspace, reason string,
) (authority.SystemAuthority, error) {
	stub.action, stub.workspace, stub.reason = action, workspace, reason
	return authority.SystemAuthority{}, stub.err
}

func (stub *agentProvisioningOwnerAuthorityStub) AuthorityForRole(
	_ context.Context,
	workspace, reason string,
) (authority.SystemAuthority, error) {
	return stub.record("role", workspace, reason)
}

func (stub *agentProvisioningOwnerAuthorityStub) AuthorityForAgent(
	_ context.Context,
	workspace, reason string,
) (authority.SystemAuthority, error) {
	return stub.record("agent", workspace, reason)
}

func (stub *agentProvisioningOwnerAuthorityStub) AuthorityForBinding(
	_ context.Context,
	workspace, reason string,
) (authority.SystemAuthority, error) {
	return stub.record("binding", workspace, reason)
}

func (stub *agentProvisioningOwnerAuthorityStub) AuthorityForGrant(
	_ context.Context,
	workspace, reason string,
) (authority.SystemAuthority, error) {
	return stub.record("grant", workspace, reason)
}

func TestAgentProvisioningOwnerAdaptersPreserveExactCommandsAndAuthority(t *testing.T) {
	operations := &agentProvisioningOwnerOperationsStub{}
	authorities := &agentProvisioningOwnerAuthorityStub{}
	agentsAdapter, err := newAgentProvisioningAgentsAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}
	automationAdapter, err := newAgentProvisioningAutomationAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}
	connectorsAdapter, err := newAgentProvisioningConnectorsAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}

	role := agentprovisioning.EnsureRoleCommand{CommandID: "provision-1:role", WorkspaceKey: "WS"}
	if err := agentsAdapter.EnsureRole(t.Context(), role); err != nil {
		t.Fatal(err)
	}
	assertAgentProvisioningOwnerCall(t, authorities, "role", role.WorkspaceKey, role.CommandID)
	if !reflect.DeepEqual(operations.roleCommand, role) {
		t.Fatalf("role command = %#v", operations.roleCommand)
	}

	agent := agentprovisioning.EnsureAgentCommand{CommandID: "provision-1:agent", WorkspaceKey: "WS"}
	if err := agentsAdapter.EnsureAgent(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	assertAgentProvisioningOwnerCall(t, authorities, "agent", agent.WorkspaceKey, agent.CommandID)
	if !reflect.DeepEqual(operations.agentCommand, agent) {
		t.Fatalf("agent command = %#v", operations.agentCommand)
	}

	binding := agentprovisioning.EnsureBindingCommand{CommandID: "provision-1:binding", WorkspaceKey: "WS"}
	if err := automationAdapter.EnsureBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	assertAgentProvisioningOwnerCall(t, authorities, "binding", binding.WorkspaceKey, binding.CommandID)
	if !reflect.DeepEqual(operations.bindingCommand, binding) {
		t.Fatalf("binding command = %#v", operations.bindingCommand)
	}

	grant := agentprovisioning.EnsureGrantCommand{CommandID: "provision-1:grant:read", WorkspaceKey: "WS"}
	if err := connectorsAdapter.EnsureGrant(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	assertAgentProvisioningOwnerCall(t, authorities, "grant", grant.WorkspaceKey, grant.CommandID)
	if !reflect.DeepEqual(operations.grantCommand, grant) {
		t.Fatalf("grant command = %#v", operations.grantCommand)
	}
}

func assertAgentProvisioningOwnerCall(
	t *testing.T,
	authorities *agentProvisioningOwnerAuthorityStub,
	action, workspace, commandID string,
) {
	t.Helper()
	if authorities.action != action || authorities.workspace != workspace ||
		authorities.reason != "AgentProvisioning "+commandID {
		t.Fatalf("authority call = action:%q workspace:%q reason:%q", authorities.action, authorities.workspace, authorities.reason)
	}
}

func TestAgentProvisioningOwnerAdaptersFailClosed(t *testing.T) {
	operations := &agentProvisioningOwnerOperationsStub{}
	authorities := &agentProvisioningOwnerAuthorityStub{}
	if _, err := newAgentProvisioningAgentsAdapter(nil, authorities); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil Agents operations = %v", err)
	}
	if _, err := newAgentProvisioningAutomationAdapter(operations, nil); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil Automation authority = %v", err)
	}
	if _, err := newAgentProvisioningConnectorsAdapter(nil, authorities); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil Connectors operations = %v", err)
	}

	providerFailure := errors.New("issuer unavailable")
	authorities.err = providerFailure
	agentsAdapter, err := newAgentProvisioningAgentsAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}
	err = agentsAdapter.EnsureRole(t.Context(), agentprovisioning.EnsureRoleCommand{
		CommandID: "provision-1:role", WorkspaceKey: "WS",
	})
	if !errors.Is(err, agentprovisioning.ErrUnavailable) || !errors.Is(err, providerFailure) {
		t.Fatalf("authority failure = %v", err)
	}
	if operations.roleCommand.CommandID != "" {
		t.Fatal("authority failure reached durable operation")
	}

	authorities.err = nil
	operations.err = agentprovisioning.ErrConflict
	automationAdapter, err := newAgentProvisioningAutomationAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}
	err = automationAdapter.EnsureBinding(t.Context(), agentprovisioning.EnsureBindingCommand{
		CommandID: "provision-1:binding", WorkspaceKey: "WS",
	})
	if !errors.Is(err, agentprovisioning.ErrConflict) {
		t.Fatalf("owner command failure = %v", err)
	}

	operations.err = nil
	connectorsAdapter, err := newAgentProvisioningConnectorsAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}
	for _, commandID := range []string{"", " command-1", "command-1\nforged"} {
		err = connectorsAdapter.EnsureGrant(t.Context(), agentprovisioning.EnsureGrantCommand{
			CommandID: commandID, WorkspaceKey: "WS",
		})
		if !errors.Is(err, agentprovisioning.ErrInvalid) {
			t.Fatalf("noncanonical command id %q = %v", commandID, err)
		}
	}
}
