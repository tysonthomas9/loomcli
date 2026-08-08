package owneradapters

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type agentProvisioningAutomationOperationsStub struct {
	command agentprovisioning.EnsureBindingCommand
	err     error
}

func (stub *agentProvisioningAutomationOperationsStub) EnsureBinding(
	_ context.Context,
	command agentprovisioning.EnsureBindingCommand,
) error {
	stub.command = command
	return stub.err
}

type agentProvisioningAutomationAuthorityStub struct {
	workspace string
	reason    string
	err       error
}

func (stub *agentProvisioningAutomationAuthorityStub) AuthorityForBinding(
	_ context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	stub.workspace, stub.reason = workspace, reason
	return authority.SystemAuthority{}, stub.err
}

func TestAgentProvisioningAutomationAdapterPreservesExactDurableCommand(t *testing.T) {
	operations := &agentProvisioningAutomationOperationsStub{}
	authorities := &agentProvisioningAutomationAuthorityStub{}
	adapter, err := NewAutomationAdapter(operations, authorities)
	if err != nil {
		t.Fatal(err)
	}
	command := agentprovisioning.EnsureBindingCommand{
		CommandID: "provision-1:binding", WorkspaceKey: "WS", AgentID: "agt-docs",
		ProvisioningID:           "provision-1",
		ProvisioningGenerationID: "0123456789abcdef0123456789abcdef",
		Binding: agentprovisioning.BindingSpec{
			BindingID: "binding-1", Name: "Docs review",
			SourceKind: "internal", SourceConfigRef: "role://docs?backend=codex",
			RouteKey:      "internal:binding-1",
			EventPatterns: []string{"internal.task.review"},
			DriverID:      "prompt-agent", DriverVersionID: "prompt-agent-v1",
			Entrypoint: "run", ConcurrencyPolicy: "one_active_per_epic",
			Enabled: true,
		},
	}
	if err := adapter.EnsureBinding(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if authorities.workspace != "WS" ||
		authorities.reason != "AgentProvisioning provision-1:binding" ||
		!reflect.DeepEqual(operations.command, command) {
		t.Fatalf("authority=%+v command=%+v", authorities, operations.command)
	}
}

func TestAgentProvisioningAutomationAdapterMapsFailuresAndRejectsIncompleteComposition(t *testing.T) {
	if _, err := NewAutomationAdapter(
		nil,
		&agentProvisioningAutomationAuthorityStub{},
	); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil operations = %v", err)
	}
	if _, err := NewAutomationAdapter(
		&agentProvisioningAutomationOperationsStub{},
		nil,
	); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("nil authorities = %v", err)
	}
	for _, testErr := range []error{
		agentprovisioning.ErrInvalid,
		agentprovisioning.ErrConflict,
		agentprovisioning.ErrNotFound,
		agentprovisioning.ErrUnavailable,
	} {
		adapter, err := NewAutomationAdapter(
			&agentProvisioningAutomationOperationsStub{err: testErr},
			&agentProvisioningAutomationAuthorityStub{},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = adapter.EnsureBinding(t.Context(), agentprovisioning.EnsureBindingCommand{
			CommandID: "provision-1:binding", WorkspaceKey: "WS",
		})
		if !errors.Is(err, testErr) {
			t.Fatalf("error = %v, want %v", err, testErr)
		}
	}

	providerFailure := errors.New("issuer unavailable")
	operations := &agentProvisioningAutomationOperationsStub{}
	adapter, err := NewAutomationAdapter(
		operations,
		&agentProvisioningAutomationAuthorityStub{err: providerFailure},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.EnsureBinding(t.Context(), agentprovisioning.EnsureBindingCommand{
		CommandID: "provision-1:binding", WorkspaceKey: "WS",
	})
	if !errors.Is(err, agentprovisioning.ErrUnavailable) ||
		!errors.Is(err, providerFailure) {
		t.Fatalf("authority failure = %v", err)
	}
	if operations.command.CommandID != "" {
		t.Fatal("authority failure reached durable binding operation")
	}
}
