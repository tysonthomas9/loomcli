package automation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type capabilityStub struct {
	command agentprovisioning.EnsureBindingCommand
	err     error
}

func (stub *capabilityStub) EnsureBinding(
	_ context.Context,
	command agentprovisioning.EnsureBindingCommand,
) error {
	stub.command = command
	return stub.err
}

type authorityStub struct {
	workspace string
	reason    string
	err       error
}

func (stub *authorityStub) AuthorityForBinding(
	_ context.Context,
	workspace,
	reason string,
) (authority.SystemAuthority, error) {
	stub.workspace, stub.reason = workspace, reason
	return authority.SystemAuthority{}, stub.err
}

func TestAdapterMapsCompleteBindingIntentAndDerivesManagedOwner(t *testing.T) {
	capability := &capabilityStub{}
	authorities := &authorityStub{}
	adapter, err := New(capability, authorities)
	if err != nil {
		t.Fatal(err)
	}
	input := agentprovisioning.EnsureBindingCommand{
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
	if err := adapter.EnsureBinding(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	got := capability.command
	if authorities.workspace != "WS" ||
		authorities.reason != "AgentProvisioning provision-1:binding" ||
		!reflect.DeepEqual(got, input) {
		t.Fatalf("authority=%+v command=%+v", authorities, got)
	}
}

func TestAdapterMapsAutomationFailuresAndRejectsIncompleteComposition(t *testing.T) {
	if _, err := New(nil, &authorityStub{}); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("New nil capability = %v", err)
	}
	if _, err := New(&capabilityStub{}, nil); !errors.Is(err, agentprovisioning.ErrUnavailable) {
		t.Fatalf("New nil authorities = %v", err)
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
			err = adapter.EnsureBinding(t.Context(), agentprovisioning.EnsureBindingCommand{
				CommandID: "provision-1:binding", WorkspaceKey: "WS",
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
	err = adapter.EnsureBinding(t.Context(), agentprovisioning.EnsureBindingCommand{
		CommandID: "provision-1:binding", WorkspaceKey: "WS",
	})
	if !errors.Is(err, agentprovisioning.ErrUnavailable) ||
		!errors.Is(err, providerFailure) {
		t.Fatalf("authority failure = %v", err)
	}
}
