package connectorsproviders

import (
	"context"
	"errors"
	"testing"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

type providerStub struct {
	call CallSpec
}

func (stub *providerStub) Call(_ context.Context, call CallSpec) (CallResult, error) {
	stub.call = call
	return CallResult{
		Status: 201, Body: map[string]any{"ok": true}, Decision: connectorsmodule.ConnectorCallGranted,
	}, nil
}

func TestRegistryUsesOwnerProviderCall(t *testing.T) {
	registry := NewRegistry()
	stub := &providerStub{}
	if err := registry.Register(connectorsmodule.ConnectorSourceGitHub, stub); err != nil {
		t.Fatal(err)
	}
	provider, err := registry.Get(connectorsmodule.ConnectorSourceGitHub)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Call(t.Context(), connectorsmodule.ProviderCall{
		Action: connectorsmodule.ActionGitHubReviewPost, Resource: "repo:octocat/hello",
		Args: map[string]any{"number": 7}, IdempotencyKey: "call-1", Credential: "secret",
	})
	if err != nil || result.Status != 201 || result.Decision != connectorsmodule.ConnectorCallGranted {
		t.Fatalf("Call = %+v, %v", result, err)
	}
	if stub.call.Credential != "secret" || stub.call.IdempotencyKey != "call-1" {
		t.Fatalf("owner call = %+v", stub.call)
	}
}

func TestRegistryFailsClosed(t *testing.T) {
	var unavailable *Registry
	if _, err := unavailable.Get(connectorsmodule.ConnectorSourceGitHub); !errors.Is(err, connectorsmodule.ErrUnavailable) {
		t.Fatalf("nil registry Get = %v", err)
	}
	registry := NewRegistry()
	if _, err := registry.Get(connectorsmodule.ConnectorSourceGitHub); !errors.Is(err, connectorsmodule.ErrNotFound) {
		t.Fatalf("Get missing = %v", err)
	}
}
