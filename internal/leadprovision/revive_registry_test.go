package leadprovision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

func stoppedProvider() *fakeReviveProvider {
	return &fakeReviveProvider{sandbox: placement.ProviderSandbox{
		ID:       "sandbox-1",
		State:    placement.ProviderSandboxStopped,
		RawState: placement.ProviderSandboxRawStopped,
	}}
}

// TestEnsureAttachableRoutesToOwningProvider is the defect this seam closes. A
// sandbox id is unique only WITHIN a provider, so a coordinator holding one
// adapter and taking a bare id would inspect -- and revive -- whatever sandbox
// carried that id on the wrong platform.
func TestEnsureAttachableRoutesToOwningProvider(t *testing.T) {
	for _, tc := range []struct {
		name     string
		owner    domain.RuntimeProvider
		nonOwner domain.RuntimeProvider
	}{
		{"daytona owns", domain.RuntimeProviderDaytona, domain.RuntimeProviderExe},
		{"exe owns", domain.RuntimeProviderExe, domain.RuntimeProviderDaytona},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, other := stoppedProvider(), stoppedProvider()
			coordinator := NewReviveCoordinator(SandboxStateProviderRegistry{
				tc.owner:    owner,
				tc.nonOwner: other,
			}, newFakeReviveProvisioner())

			err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", tc.owner, "sandbox-1")
			if !errors.Is(err, ErrReviveStarting) {
				t.Fatalf("EnsureAttachable = %v, want ErrReviveStarting", err)
			}
			if owner.callCount() == 0 {
				t.Fatal("owning provider was never consulted")
			}
			if got := other.callCount(); got != 0 {
				t.Fatalf("non-owning provider saw %d Get call(s); a sandbox id must never cross providers", got)
			}
		})
	}
}

// TestEnsureAttachableFailsClosedOnUnknownProvider pins the fail-closed rule.
// Falling back to "the only registered adapter" is exactly how a revive lands
// on another platform's sandbox, so an unregistered or unset provider must be
// an error -- and must not touch any adapter on the way out.
func TestEnsureAttachableFailsClosedOnUnknownProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind domain.RuntimeProvider
	}{
		{"unset", ""},
		{"whitespace", "   "},
		{"unregistered", domain.RuntimeProviderExe},
		{"unknown", domain.RuntimeProvider("fly")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daytona := stoppedProvider()
			coordinator := NewReviveCoordinator(
				SandboxStateProviderRegistry{domain.RuntimeProviderDaytona: daytona},
				newFakeReviveProvisioner())

			err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", tc.kind, "sandbox-1")
			if err == nil {
				t.Fatal("EnsureAttachable error = nil, want a fail-closed error")
			}
			if errors.Is(err, ErrReviveStarting) {
				t.Fatalf("EnsureAttachable started a revive for provider %q", tc.kind)
			}
			if got := daytona.callCount(); got != 0 {
				t.Fatalf("registered provider saw %d Get call(s) for an unresolvable provider", got)
			}
		})
	}
}

func TestReviveCoordinatorSupports(t *testing.T) {
	coordinator := NewReviveCoordinator(
		SandboxStateProviderRegistry{domain.RuntimeProviderDaytona: stoppedProvider()},
		newFakeReviveProvisioner())

	if !coordinator.Supports(domain.RuntimeProviderDaytona) {
		t.Error("Supports(daytona) = false, want true")
	}
	for _, kind := range []domain.RuntimeProvider{"", domain.RuntimeProviderExe, "fly"} {
		if coordinator.Supports(kind) {
			t.Errorf("Supports(%q) = true, want false", kind)
		}
	}
	var nilCoordinator *ReviveCoordinator
	if nilCoordinator.Supports(domain.RuntimeProviderDaytona) {
		t.Error("Supports on a nil coordinator = true, want false")
	}
}

// TestNewReviveCoordinatorDropsUnusableEntries: an entry under the empty
// provider, or a nil adapter, would resolve to something unusable at attach
// time. Dropping them at construction keeps providerFor's error honest.
func TestNewReviveCoordinatorDropsUnusableEntries(t *testing.T) {
	caller := SandboxStateProviderRegistry{
		domain.RuntimeProviderDaytona: stoppedProvider(),
		"":                            stoppedProvider(),
		domain.RuntimeProviderExe:     nil,
	}
	coordinator := NewReviveCoordinator(caller, newFakeReviveProvisioner())

	// Mutating the caller's map afterwards must not change routing.
	caller[domain.RuntimeProviderExe] = stoppedProvider()

	if !coordinator.Supports(domain.RuntimeProviderDaytona) {
		t.Error("daytona should be registered")
	}
	if coordinator.Supports(domain.RuntimeProviderExe) {
		t.Error("a nil adapter, later replaced in the caller's map, must not become routable")
	}
	if coordinator.Supports("") {
		t.Error("the empty runtime provider must never be routable")
	}
	err := coordinator.EnsureAttachable(context.Background(), "WS", "nova", domain.RuntimeProviderExe, "sandbox-1")
	if err == nil || !strings.Contains(err.Error(), "no sandbox state provider registered") {
		t.Fatalf("EnsureAttachable err = %v, want a not-registered error", err)
	}
}
