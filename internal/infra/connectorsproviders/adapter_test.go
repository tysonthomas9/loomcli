package connectorsproviders

import (
	"context"
	"errors"
	"testing"

	legacyproviders "github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

type providerStub struct {
	call legacyproviders.CallSpec
}

func (stub *providerStub) Call(_ context.Context, call legacyproviders.CallSpec) (legacyproviders.CallResult, error) {
	stub.call = call
	return legacyproviders.CallResult{
		Status: 201, Body: map[string]any{"ok": true}, Decision: domain.ConnectorCallGranted,
	}, nil
}

func TestRegistryAdaptsOwnerProviderCall(t *testing.T) {
	legacy := legacyproviders.NewRegistry()
	stub := &providerStub{}
	if err := legacy.Register(domain.ConnectorSourceGitHub, stub); err != nil {
		t.Fatal(err)
	}
	registry, err := New(legacy)
	if err != nil {
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
		t.Fatalf("legacy call = %+v", stub.call)
	}
}

func TestRegistryFailsClosed(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, connectorsmodule.ErrUnavailable) {
		t.Fatalf("New(nil) = %v", err)
	}
	registry, err := New(legacyproviders.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get(connectorsmodule.ConnectorSourceGitHub); !errors.Is(err, connectorsmodule.ErrNotFound) {
		t.Fatalf("Get missing = %v", err)
	}
}
