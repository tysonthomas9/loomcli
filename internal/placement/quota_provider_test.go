package placement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// quotaFixture builds a two-provider broker with an explicit account-wide cap
// and optional per-provider caps.
func quotaFixture(t *testing.T, maxLive ResourceSize, perProvider map[domain.RuntimeProvider]ResourceSize) (store.Store, *Broker) {
	t.Helper()
	t.Setenv(deploymentIDEnv, testDeploymentID)
	st := memstore.New()
	createWorkspace(t, st, "WS")
	broker, err := NewBroker(Config{
		Store: st,
		Providers: ProviderRegistry{
			domain.RuntimeProviderDaytona: &fakeProvider{},
			domain.RuntimeProviderExe:     &fakeProvider{},
		},
		TokenKey:             testTokenKey,
		DeploymentID:         testDeploymentID,
		DeleteConfirmBackoff: time.Millisecond,
		MaxLive:              maxLive,
		MaxLiveByProvider:    perProvider,
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	return st, broker
}

func provisionOn(t *testing.T, broker *Broker, kind domain.RuntimeProvider, agent string, vcpu, memGiB int) error {
	t.Helper()
	req := testProvisionRequest(agent, vcpu, memGiB)
	req.RuntimeProvider = kind
	_, err := broker.Provision(context.Background(), req)
	return err
}

// TestPerProviderCapDoesNotStarveOtherProviders is the defect item 6 exists to
// fix. Under a single global budget, one provider consuming it blocked every
// other provider -- even though their sandbox pools are physically unrelated.
func TestPerProviderCapDoesNotStarveOtherProviders(t *testing.T) {
	_, broker := quotaFixture(t,
		ResourceSize{VCPU: 8, MemGiB: 16},
		map[domain.RuntimeProvider]ResourceSize{
			domain.RuntimeProviderDaytona: {VCPU: 4, MemGiB: 8},
			domain.RuntimeProviderExe:     {VCPU: 4, MemGiB: 8},
		})

	// Fill Daytona's cap exactly.
	if err := provisionOn(t, broker, domain.RuntimeProviderDaytona, "nova", 4, 8); err != nil {
		t.Fatalf("first daytona provision: %v", err)
	}
	// Daytona is now full...
	err := provisionOn(t, broker, domain.RuntimeProviderDaytona, "nova2", 1, 2)
	if !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("second daytona provision err = %v, want ErrUnschedulable", err)
	}
	// ...but exe has its own headroom and must still be admitted.
	if err := provisionOn(t, broker, domain.RuntimeProviderExe, "falcon", 4, 8); err != nil {
		t.Fatalf("exe provision starved by daytona's usage: %v", err)
	}
}

// TestAccountWideCapStillBindsAcrossProviders is the other half, and the reason
// MaxLive stayed global. Registering a second provider must not silently double
// total capacity: MaxLive bounds the shared-OAuth blast radius, which every lead
// shares regardless of which provider hosts it.
func TestAccountWideCapStillBindsAcrossProviders(t *testing.T) {
	_, broker := quotaFixture(t,
		ResourceSize{VCPU: 6, MemGiB: 12},
		map[domain.RuntimeProvider]ResourceSize{
			domain.RuntimeProviderDaytona: {VCPU: 4, MemGiB: 8},
			domain.RuntimeProviderExe:     {VCPU: 4, MemGiB: 8},
		})

	if err := provisionOn(t, broker, domain.RuntimeProviderDaytona, "nova", 4, 8); err != nil {
		t.Fatalf("daytona provision: %v", err)
	}
	// Within exe's own 4/8 cap, but 4+4 > the account-wide 6.
	err := provisionOn(t, broker, domain.RuntimeProviderExe, "falcon", 4, 8)
	if !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("exe provision err = %v, want ErrUnschedulable from the account-wide cap", err)
	}
	// A request that fits under the account-wide remainder is admitted.
	if err := provisionOn(t, broker, domain.RuntimeProviderExe, "falcon2", 2, 4); err != nil {
		t.Fatalf("exe provision within account remainder: %v", err)
	}
}

// TestNoPerProviderCapBehavesLikeBefore pins backward compatibility: with
// MaxLiveByProvider unset, admission is byte-identical to the single-budget
// behavior that shipped.
func TestNoPerProviderCapBehavesLikeBefore(t *testing.T) {
	_, broker := quotaFixture(t, ResourceSize{VCPU: 4, MemGiB: 8}, nil)

	if err := provisionOn(t, broker, domain.RuntimeProviderDaytona, "nova", 3, 6); err != nil {
		t.Fatalf("daytona provision: %v", err)
	}
	// The global budget is shared, so exe is blocked by daytona's usage.
	if err := provisionOn(t, broker, domain.RuntimeProviderExe, "falcon", 2, 4); !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("exe provision err = %v, want ErrUnschedulable under the shared global budget", err)
	}
}

// TestListAttributesReservationsToOwningProvider proves the reported breakdown
// matches the total it is derived from. Reporting one global number made a
// second provider's consumption indistinguishable from the first's.
func TestListAttributesReservationsToOwningProvider(t *testing.T) {
	_, broker := quotaFixture(t,
		ResourceSize{VCPU: 16, MemGiB: 32},
		map[domain.RuntimeProvider]ResourceSize{})

	if err := provisionOn(t, broker, domain.RuntimeProviderDaytona, "nova", 2, 4); err != nil {
		t.Fatalf("daytona provision: %v", err)
	}
	if err := provisionOn(t, broker, domain.RuntimeProviderExe, "falcon", 1, 2); err != nil {
		t.Fatalf("exe provision: %v", err)
	}

	list, err := broker.List(context.Background(), "WS")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantDaytona := ResourceSize{VCPU: 2, MemGiB: 4}
	wantExe := ResourceSize{VCPU: 1, MemGiB: 2}
	if got := list.LiveReservedByProvider[domain.RuntimeProviderDaytona]; got != wantDaytona {
		t.Errorf("daytona reserved = %+v, want %+v", got, wantDaytona)
	}
	if got := list.LiveReservedByProvider[domain.RuntimeProviderExe]; got != wantExe {
		t.Errorf("exe reserved = %+v, want %+v", got, wantExe)
	}
	// The breakdown must reconcile with the total, or the two views have drifted.
	var summed ResourceSize
	for _, size := range list.LiveReservedByProvider {
		summed.VCPU += size.VCPU
		summed.MemGiB += size.MemGiB
	}
	if summed != list.LiveReserved {
		t.Fatalf("per-provider breakdown %+v does not sum to LiveReserved %+v", summed, list.LiveReserved)
	}
}

// TestPerProviderCapsAreSnapshotAtConstruction matches the provider registry:
// a budget the caller can still mutate is not a budget.
func TestPerProviderCapsAreSnapshotAtConstruction(t *testing.T) {
	caps := map[domain.RuntimeProvider]ResourceSize{
		domain.RuntimeProviderDaytona: {VCPU: 2, MemGiB: 4},
	}
	_, broker := quotaFixture(t, ResourceSize{VCPU: 16, MemGiB: 32}, caps)

	// Raise the cap through the caller's map after construction.
	caps[domain.RuntimeProviderDaytona] = ResourceSize{VCPU: 99, MemGiB: 99}

	if err := provisionOn(t, broker, domain.RuntimeProviderDaytona, "nova", 2, 4); err != nil {
		t.Fatalf("provision at the original cap: %v", err)
	}
	if err := provisionOn(t, broker, domain.RuntimeProviderDaytona, "nova2", 1, 2); !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("provision err = %v, want ErrUnschedulable; the post-construction cap raise must not apply", err)
	}
}
