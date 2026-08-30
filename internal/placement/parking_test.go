package placement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// nonParkingProvider models a platform with no stop/start -- exe.dev. Its
// SetAutostopInterval FAILS LOUDLY, so any call the capability gate misses
// shows up as a test failure rather than a silent no-op.
type nonParkingProvider struct {
	fakeProvider
}

func (p *nonParkingProvider) SupportsParking() bool { return false }

func (p *nonParkingProvider) SetAutostopInterval(context.Context, string, time.Duration) error {
	p.recordCall("SetAutostopInterval")
	return errors.New("this provider cannot stop or start a sandbox")
}

// TestParkingIsSkippedForNonParkingProviders covers ALL THREE parking call
// sites: initial arming, resume shielding, and post-resume restoration.
//
// The trap is that only the first looks like "parking". Shielding returns its
// error straight out of resumeLivePlacement, so a provider whose
// SetAutostopInterval fails could never revive a lead at all -- a gate on
// arming alone would leave that broken while looking done.
func TestParkingIsSkippedForNonParkingProviders(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	ctx := context.Background()
	st := memstore.New()
	createWorkspace(t, st, "WS")
	provider := &nonParkingProvider{}
	broker := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: provider,
	})

	req := testProvisionRequest("nova", 1, 2)
	// 1. Initial arming must not call through.
	if _, err := broker.Provision(ctx, req); err != nil {
		t.Fatalf("provision on a non-parking provider: %v", err)
	}
	// 2 + 3. A resume shields then restores; both must be skipped, or this
	// second Provision fails.
	if _, err := broker.Provision(ctx, req); err != nil {
		t.Fatalf("resume on a non-parking provider: %v", err)
	}

	for _, call := range provider.allCallsSnapshot() {
		if call == "SetAutostopInterval" {
			t.Fatal("SetAutostopInterval was called on a provider that declared it cannot park")
		}
	}
}

// TestParkingStillAppliesToCapableProviders is the inverse guard. Defaulting a
// provider to "cannot park" would silently stop arming autostop on Daytona --
// leads that never park, billing forever, with nothing to notice.
func TestParkingStillAppliesToCapableProviders(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	ctx := context.Background()
	st := memstore.New()
	createWorkspace(t, st, "WS")
	provider := &fakeProvider{}
	broker := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: provider,
	})

	if _, err := broker.Provision(ctx, testProvisionRequest("nova", 1, 2)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	var armed bool
	for _, call := range provider.allCallsSnapshot() {
		if call == "SetAutostopInterval" {
			armed = true
		}
	}
	if !armed {
		t.Fatal("a parking-capable provider must still have its autostop armed")
	}
}

// TestSupportsParkingDefaultsToCapable pins the default direction, chosen on
// which mistake is worse: assuming capability wrongly fails LOUDLY, assuming
// incapability wrongly leaks money silently.
func TestSupportsParkingDefaultsToCapable(t *testing.T) {
	undeclared := providerHandle{kind: domain.RuntimeProviderDaytona, adapter: &fakeProvider{}}
	if !undeclared.supportsParking() {
		t.Error("a provider that does not implement ParkingCapable must be assumed capable")
	}
	declared := providerHandle{kind: domain.RuntimeProviderExe, adapter: &nonParkingProvider{}}
	if declared.supportsParking() {
		t.Error("a provider declaring SupportsParking() == false must be treated as incapable")
	}
}
