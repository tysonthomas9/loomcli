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

// totalCalls sums every provider interaction the fake observes. The routing
// tests assert this is ZERO for the non-owning provider: a weaker assertion
// (say, only createCallCount) would miss a stray Get or Delete landing on the
// wrong adapter, which is exactly the bug the registry exists to prevent.
//
// This MUST cover all 12 Provider methods. FindByName, UpdateLastActivity and
// KillPtySession originally recorded nothing, so a misroute of any of them was
// invisible here -- worst of all FindByName, which is the create-seam
// reconcile path and therefore the billing-leak-critical call.
// TestFakeProviderRecordsEveryProviderMethod guards against the gap reopening.
func (f *fakeProvider) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	// allCalls is recorded on ENTRY, so this counts attempted calls, not just
	// successful ones -- a stray call that early-returns still shows up.
	return len(f.allCalls)
}

// TestFakeProviderRecordsEveryProviderMethod calls all 12 Provider methods on a
// fake and asserts each one moves totalCalls. Without this, adding a method to
// the Provider interface (or forgetting to record one) silently weakens every
// "non-owner received zero calls" assertion in this file.
func TestFakeProviderRecordsEveryProviderMethod(t *testing.T) {
	ctx := context.Background()
	for name, call := range map[string]func(f *fakeProvider){
		"Create":              func(f *fakeProvider) { _, _ = f.Create(ctx, CreateRequest{Name: "n"}) },
		"Get":                 func(f *fakeProvider) { _, _ = f.Get(ctx, "s") },
		"FindByName":          func(f *fakeProvider) { _, _ = f.FindByName(ctx, "n") },
		"EnsureRunning":       func(f *fakeProvider) { _, _ = f.EnsureRunning(ctx, "s") },
		"Delete":              func(f *fakeProvider) { _ = f.Delete(ctx, "s") },
		"UpdateLastActivity":  func(f *fakeProvider) { _ = f.UpdateLastActivity(ctx, "s") },
		"SetAutostopInterval": func(f *fakeProvider) { _ = f.SetAutostopInterval(ctx, "s", time.Minute) },
		"PrepareLeadBoot":     func(f *fakeProvider) { _ = f.PrepareLeadBoot(ctx, "s", LeadBootPrep{}) },
		"CreatePty":           func(f *fakeProvider) { _ = f.CreatePty(ctx, "s", ProcessSpec{SessionID: "lead"}) },
		"ListManaged":         func(f *fakeProvider) { _, _ = f.ListManaged(ctx, nil) },
		"ListPtySessions":     func(f *fakeProvider) { _, _ = f.ListPtySessions(ctx, "s") },
		"KillPtySession":      func(f *fakeProvider) { _ = f.KillPtySession(ctx, "s", "lead") },
	} {
		t.Run(name, func(t *testing.T) {
			// Deliberately NOT seeded: every method must be counted even when
			// it early-returns ErrSandboxNotFound, which is exactly the
			// "stray call against the empty non-owner" case.
			f := &fakeProvider{}
			before := f.totalCalls()
			call(f)
			if got := f.totalCalls(); got == before {
				t.Fatalf("%s did not move totalCalls (%d) -- a misroute of it would be invisible", name, got)
			}
		})
	}
}

func mustMultiProviderBroker(t *testing.T, st store.Store, reg ProviderRegistry) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{
		Store:                st,
		Providers:            reg,
		TokenKey:             testTokenKey,
		DeploymentID:         testDeploymentID,
		DeleteConfirmBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	return broker
}

func mustMultiProviderBrokerWithNow(t *testing.T, st store.Store, reg ProviderRegistry, now time.Time) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{
		Store:                st,
		Providers:            reg,
		TokenKey:             testTokenKey,
		DeploymentID:         testDeploymentID,
		DeleteConfirmBackoff: time.Millisecond,
		Now:                  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	return broker
}

func TestNewBrokerRejectsEmptyRegistry(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	for name, reg := range map[string]ProviderRegistry{
		"nil":            nil,
		"empty":          {},
		"nil provider":   {domain.RuntimeProviderDaytona: nil},
		"empty kind key": {"": &fakeProvider{}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewBroker(Config{Store: memstore.New(), Providers: reg, TokenKey: testTokenKey})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("NewBroker(%s) = %v, want ErrInvalid", name, err)
			}
		})
	}
}

// providerFor must FAIL rather than fall back to the only registered provider.
// A silent fallback is how a placement gets created on one provider and
// released against another, severing the record of a live, billing sandbox.
func TestProviderForFailsClosed(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	daytona := &fakeProvider{}
	broker := mustMultiProviderBroker(t, memstore.New(), ProviderRegistry{
		domain.RuntimeProviderDaytona: daytona,
	})

	for _, kind := range []domain.RuntimeProvider{
		"",                        // unstamped node must not default
		domain.RuntimeProviderExe, // known enum value, not registered here
		domain.RuntimeProviderLocal,
		"totally-unknown",
	} {
		if _, err := broker.providerFor(kind); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("providerFor(%q) = %v, want ErrInvalid", kind, err)
		}
	}
	got, err := broker.providerFor(domain.RuntimeProviderDaytona)
	if err != nil || got.adapter != Provider(daytona) || got.kind != domain.RuntimeProviderDaytona {
		t.Fatalf("providerFor(daytona) = %+v, %v; want the registered fake bound to daytona", got, err)
	}
}

func TestProviderForNodeRequiresNode(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	broker := mustMultiProviderBroker(t, memstore.New(), ProviderRegistry{
		domain.RuntimeProviderDaytona: &fakeProvider{},
	})
	if _, err := broker.providerForNode(nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("providerForNode(nil) = %v, want ErrInvalid", err)
	}
	unstamped := &domain.Node{NodeID: "placement-1"}
	if _, err := broker.providerForNode(unstamped); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("providerForNode(unstamped) = %v, want ErrInvalid", err)
	}
}

// RuntimeProvider is required on the request: per-provider quota is impossible
// without it, and defaulting would reintroduce the silent fallback.
func TestProvisionRequiresRuntimeProvider(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	provider := &fakeProvider{}
	broker := mustBroker(t, memstore.New(), provider)

	req := testProvisionRequest("nova", 1, 2)
	req.RuntimeProvider = ""
	if _, err := broker.Provision(context.Background(), req); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Provision without runtime provider = %v, want ErrInvalid", err)
	}
	if n := provider.totalCalls(); n != 0 {
		t.Fatalf("rejected provision still touched the provider %d time(s)", n)
	}
}

// A provision naming an unregistered provider must fail, and must not fall
// through to a registered one.
func TestProvisionUnregisteredProviderIsRejected(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	daytona := &fakeProvider{}
	broker := mustMultiProviderBroker(t, memstore.New(), ProviderRegistry{
		domain.RuntimeProviderDaytona: daytona,
	})

	req := testProvisionRequest("nova", 1, 2)
	req.RuntimeProvider = domain.RuntimeProviderExe
	if _, err := broker.Provision(context.Background(), req); err == nil {
		t.Fatal("Provision onto an unregistered provider succeeded")
	}
	if n := daytona.totalCalls(); n != 0 {
		t.Fatalf("unregistered provision leaked %d call(s) onto the daytona provider", n)
	}
}

// The core routing property: with two providers registered, every call for a
// placement goes to the provider that owns it and the other sees NOTHING --
// including through release, where a misroute would delete an unrelated
// sandbox that happens to share an id.
func TestProvisionAndReleaseRouteToOwningProviderOnly(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	for _, tc := range []struct {
		name     string
		owner    domain.RuntimeProvider
		nonOwner domain.RuntimeProvider
	}{
		{"daytona owns", domain.RuntimeProviderDaytona, domain.RuntimeProviderExe},
		{"exe owns", domain.RuntimeProviderExe, domain.RuntimeProviderDaytona},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ownerFake, otherFake := &fakeProvider{}, &fakeProvider{}
			broker := mustMultiProviderBroker(t, memstore.New(), ProviderRegistry{
				tc.owner:    ownerFake,
				tc.nonOwner: otherFake,
			})

			req := testProvisionRequest("nova", 1, 2)
			req.RuntimeProvider = tc.owner
			res, err := broker.Provision(ctx, req)
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			if res.Node.RuntimeProvider != tc.owner {
				t.Fatalf("node stamped %q, want %q", res.Node.RuntimeProvider, tc.owner)
			}
			if ownerFake.createCallCount() != 1 {
				t.Fatalf("owner got %d create calls, want 1", ownerFake.createCallCount())
			}
			if n := otherFake.totalCalls(); n != 0 {
				t.Fatalf("non-owner received %d call(s) during provision", n)
			}

			if _, err := broker.Release(ctx, req.WorkspaceKey, res.Node.NodeID, ReleaseFence{Force: true}); err != nil {
				t.Fatalf("Release: %v", err)
			}
			if ownerFake.deleteCallCount() != 1 {
				t.Fatalf("owner got %d delete calls, want 1", ownerFake.deleteCallCount())
			}
			if n := otherFake.totalCalls(); n != 0 {
				t.Fatalf("non-owner received %d call(s) across provision+release", n)
			}
		})
	}
}

// The reaper is destructive, so it must skip placements whose provider this
// deployment cannot act on rather than reaping them against another adapter.
func TestReaperSkipsUnregisteredProviderNodes(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	broker := mustMultiProviderBroker(t, memstore.New(), ProviderRegistry{
		domain.RuntimeProviderDaytona: &fakeProvider{},
	})
	reaper := &PlacementReaper{broker: broker}

	for _, tc := range []struct {
		name string
		node *domain.Node
		want bool
	}{
		{"registered", &domain.Node{RuntimeProvider: domain.RuntimeProviderDaytona, Placement: &domain.NodePlacement{}}, true},
		{"unregistered", &domain.Node{RuntimeProvider: domain.RuntimeProviderExe, Placement: &domain.NodePlacement{}}, false},
		{"unstamped", &domain.Node{Placement: &domain.NodePlacement{}}, false},
		{"no placement", &domain.Node{RuntimeProvider: domain.RuntimeProviderDaytona}, false},
		{"nil", nil, false},
	} {
		if got := reaper.shouldExamineNode(tc.node); got != tc.want {
			t.Fatalf("shouldExamineNode(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRegisteredProvidersIsSorted(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	broker := mustMultiProviderBroker(t, memstore.New(), ProviderRegistry{
		domain.RuntimeProviderExe:     &fakeProvider{},
		domain.RuntimeProviderDaytona: &fakeProvider{},
	})
	got := broker.registeredProviders()
	want := []domain.RuntimeProvider{domain.RuntimeProviderDaytona, domain.RuntimeProviderExe}
	if len(got) != len(want) {
		t.Fatalf("registeredProviders() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("registeredProviders() = %v, want %v (deterministic order)", got, want)
		}
	}
}

// A rejected provider must leave NO durable trace. The original implementation
// resolved the provider inside createSandbox -- i.e. AFTER
// admitProvisioningNode had already persisted the row -- so a rejected request
// returned ErrInvalid but left a live `provisioning` placement that could
// never be resumed (unresolvable provider) and that the reaper deliberately
// skips (unregistered provider). The row was wedged forever.
func TestUnregisteredProvisionCreatesNoPlacementRow(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	ctx := context.Background()
	st := memstore.New()
	daytona := &fakeProvider{}
	broker := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: daytona,
	})

	req := testProvisionRequest("nova", 1, 2)
	req.RuntimeProvider = domain.RuntimeProviderExe
	if _, err := broker.Provision(ctx, req); err == nil {
		t.Fatal("Provision onto an unregistered provider succeeded")
	}

	listed, err := broker.List(ctx, req.WorkspaceKey)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if n := len(listed.Placements); n != 0 {
		t.Fatalf("rejected provision left %d placement row(s); it must leave none", n)
	}
	if n := daytona.totalCalls(); n != 0 {
		t.Fatalf("rejected provision made %d call(s) on the registered provider", n)
	}
}

// Release must likewise resolve before mutating: a failure after markReleasing
// strands the row in `releasing`, and the reaper skips unregistered providers,
// so nothing can ever finish it.
func TestReleaseWithUnregisteredProviderDoesNotMutate(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	ctx := context.Background()
	st := memstore.New()
	exeFake := &fakeProvider{}

	// Provision while exe IS registered.
	full := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: &fakeProvider{},
		domain.RuntimeProviderExe:     exeFake,
	})
	req := testProvisionRequest("nova", 1, 2)
	req.RuntimeProvider = domain.RuntimeProviderExe
	res, err := full.Provision(ctx, req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	stateBefore := res.Node.Placement.State

	// A broker that no longer registers exe (deployment reconfigured) must
	// refuse the release rather than half-perform it.
	partial := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: &fakeProvider{},
	})
	for _, fence := range []ReleaseFence{
		{Generation: res.Node.Placement.Generation},
		{Force: true},
	} {
		if _, err := partial.Release(ctx, req.WorkspaceKey, res.Node.NodeID, fence); err == nil {
			t.Fatalf("Release with unregistered provider (fence %+v) succeeded", fence)
		}
		after, err := partial.Get(ctx, req.WorkspaceKey, res.Node.NodeID)
		if err != nil {
			t.Fatalf("Get after failed release: %v", err)
		}
		if after.Placement.State != stateBefore {
			t.Fatalf("failed release mutated state %q -> %q; the row is now stranded",
				stateBefore, after.Placement.State)
		}
	}
	if n := exeFake.deleteCallCount(); n != 0 {
		t.Fatalf("unregistered release still deleted via the exe adapter (%d calls)", n)
	}
}

// TestNewBrokerRejectsTypedNilProvider covers the nil that `p == nil` misses. A
// (*fakeProvider)(nil) stored in a Provider interface is a NON-nil interface
// holding a nil pointer, so a plain nil check accepts it at construction and
// the process panics on the first provider call -- mid-provision, after the
// placement row is already durable.
func TestNewBrokerRejectsTypedNilProvider(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	// staticcheck SA4023 confirms the premise at compile time: comparing this
	// against nil "is never true" once it is boxed in the interface.
	var typedNil *fakeProvider
	_, err := NewBroker(Config{
		Store:        memstore.New(),
		Providers:    ProviderRegistry{domain.RuntimeProviderDaytona: typedNil},
		TokenKey:     testTokenKey,
		DeploymentID: testDeploymentID,
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("NewBroker with typed-nil provider err = %v, want domain.ErrInvalid", err)
	}
}

// TestRegistryIsSnapshotAtConstruction proves the broker copies the registry.
// Sharing the caller's map would let anything still holding it register a
// provider -- or swap an adapter -- on a running broker, changing where live
// placements' Delete calls land without going through NewBroker's validation.
func TestRegistryIsSnapshotAtConstruction(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	reg := ProviderRegistry{domain.RuntimeProviderDaytona: &fakeProvider{}}
	broker := mustMultiProviderBroker(t, memstore.New(), reg)

	// Mutate the caller's map after construction, both ways.
	reg[domain.RuntimeProviderExe] = &fakeProvider{}
	delete(reg, domain.RuntimeProviderDaytona)

	if _, err := broker.providerFor(domain.RuntimeProviderExe); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("providerFor(exe) after post-construction insert err = %v, want domain.ErrInvalid", err)
	}
	if _, err := broker.providerFor(domain.RuntimeProviderDaytona); err != nil {
		t.Fatalf("providerFor(daytona) after post-construction delete: %v", err)
	}
	if got := broker.registeredProviders(); len(got) != 1 || got[0] != domain.RuntimeProviderDaytona {
		t.Fatalf("registeredProviders() = %v, want [daytona]", got)
	}
}

// TestOtherProviderRecordDoesNotSuppressOrphanDeletion is the leak the bound
// handle exists to close. A placement id names a RECORD, not a provider, so the
// same id can legitimately exist on two providers. While the orphan check only
// asked "does a node with this id exist?", a live Daytona record answered yes
// for an exe sandbox carrying the same id -- suppressing its deletion forever,
// since nothing else sweeps provider-side orphans. The sandbox billed silently.
func TestOtherProviderRecordDoesNotSuppressOrphanDeletion(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st := memstore.New()
	createWorkspace(t, st, "WS")
	daytona, exe := &fakeProvider{}, &fakeProvider{}
	broker := mustMultiProviderBrokerWithNow(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: daytona,
		domain.RuntimeProviderExe:     exe,
	}, now)

	// A healthy, live DAYTONA placement whose id an exe sandbox also carries.
	// createPlacementNode stamps RuntimeProviderDaytona.
	createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
		Generation: 1,
		State:      domain.PlacementStateActive,
		SandboxID:  "daytona-sb",
	})
	daytona.addSandbox(reaperSandbox("daytona-sb", "placement-1", "WS", "nova", ProviderSandboxRunning, now.Add(-time.Hour)))
	exe.addSandbox(reaperSandbox("exe-sb", "placement-1", "WS", "nova", ProviderSandboxRunning, now.Add(-time.Hour)))

	reaper := NewPlacementReaper(broker, ReaperConfig{
		Enforce: true,
		Grace:   2 * time.Minute,
		Now:     func() time.Time { return now },
	})
	result, err := reaper.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	assertStringSlicesEqual(t, exe.deleteCallsSnapshot(), []string{"exe-sb"})
	// The Daytona sandbox is claimed by a live record and must be untouched.
	assertStringSlicesEqual(t, daytona.deleteCallsSnapshot(), nil)
	assertReaperAction(t, result, reaperActionDeleteOrphan, "placement-1")
	for _, action := range result.Actions {
		if action.Action == reaperActionDeleteOrphan && action.RuntimeProvider != domain.RuntimeProviderExe {
			t.Fatalf("delete-orphan action reported provider %q, want exe: %#v", action.RuntimeProvider, action)
		}
	}
}

// TestOwnProviderRecordStillSuppressesOrphanDeletion is the inverse guard: the
// ownership comparison must not turn every claimed sandbox into an orphan.
// Reaping is destructive, so this pins the suppressing case as tightly as the
// leaking one above.
func TestOwnProviderRecordStillSuppressesOrphanDeletion(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st := memstore.New()
	createWorkspace(t, st, "WS")
	daytona := &fakeProvider{}
	broker := mustMultiProviderBrokerWithNow(t, st, daytonaOnly(daytona), now)

	createPlacementNode(t, st, "WS", "placement-1", "nova", domain.NodePlacement{
		Generation: 1,
		State:      domain.PlacementStateActive,
		SandboxID:  "daytona-sb",
	})
	daytona.addSandbox(reaperSandbox("daytona-sb", "placement-1", "WS", "nova", ProviderSandboxRunning, now.Add(-time.Hour)))

	reaper := NewPlacementReaper(broker, ReaperConfig{
		Enforce: true,
		Grace:   2 * time.Minute,
		Now:     func() time.Time { return now },
	})
	if _, err := reaper.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	assertStringSlicesEqual(t, daytona.deleteCallsSnapshot(), nil)
}
