package placement

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

// TestProvisionRefusesCrossProviderResume covers the seam where a provider
// change actually reaches the broker. An agent's provider is chosen at create
// time; a later change (or a daemon-profile change, for an agent with no
// explicit provider) makes the NEXT provision request name a different
// provider than the live placement carries.
//
// Every provider call routes by the node's stamped provider, so resuming would
// have SUCCEEDED -- returning a result saying the lead is running, while it
// runs on the other platform. That makes a provider change look applied when
// nothing moved.
func TestProvisionRefusesCrossProviderResume(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	ctx := context.Background()
	st := memstore.New()
	createWorkspace(t, st, "WS")
	daytona, exe := &fakeProvider{}, &fakeProvider{}
	broker := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: daytona,
		domain.RuntimeProviderExe:     exe,
	})

	req := testProvisionRequest("nova", 1, 2)
	req.RuntimeProvider = domain.RuntimeProviderDaytona
	if _, err := broker.Provision(ctx, req); err != nil {
		t.Fatalf("initial daytona provision: %v", err)
	}
	exeCallsBefore := len(exe.allCallsSnapshot())

	moved := testProvisionRequest("nova", 1, 2)
	moved.RuntimeProvider = domain.RuntimeProviderExe
	_, err := broker.Provision(ctx, moved)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cross-provider provision err = %v, want ErrConflict", err)
	}
	if got := len(exe.allCallsSnapshot()); got != exeCallsBefore {
		t.Fatalf("exe provider saw %d new call(s); a refused provision must not touch the other provider", got-exeCallsBefore)
	}
	// The live placement must be untouched and still on Daytona.
	list, err := broker.List(ctx, "WS")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Placements) != 1 {
		t.Fatalf("placements = %d, want 1; a refused provision must not create a row", len(list.Placements))
	}
	if got := list.Placements[0].RuntimeProvider; got != domain.RuntimeProviderDaytona {
		t.Fatalf("live placement provider = %q, want daytona", got)
	}
}

// TestProvisionSameProviderStillResumes is the inverse guard: the check must
// refuse only a genuine provider change, not every resume.
func TestProvisionSameProviderStillResumes(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	ctx := context.Background()
	st := memstore.New()
	createWorkspace(t, st, "WS")
	broker := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: &fakeProvider{},
		domain.RuntimeProviderExe:     &fakeProvider{},
	})

	req := testProvisionRequest("nova", 1, 2)
	req.RuntimeProvider = domain.RuntimeProviderDaytona
	first, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("first provision: %v", err)
	}
	second, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("resume on the same provider must succeed: %v", err)
	}
	if first.Node.NodeID != second.Node.NodeID {
		t.Fatalf("resume created a new placement %q, want %q", second.Node.NodeID, first.Node.NodeID)
	}
}

// TestReleaseFenceRejectsProviderMismatch extends generation+sandbox fencing to
// the provider. A sandbox id matching under the WRONG provider is a
// coincidence, not a confirmation -- so the id alone was never an identity.
func TestReleaseFenceRejectsProviderMismatch(t *testing.T) {
	t.Setenv(deploymentIDEnv, testDeploymentID)
	ctx := context.Background()
	st := memstore.New()
	createWorkspace(t, st, "WS")
	daytona := &fakeProvider{}
	broker := mustMultiProviderBroker(t, st, ProviderRegistry{
		domain.RuntimeProviderDaytona: daytona,
		domain.RuntimeProviderExe:     &fakeProvider{},
	})

	req := testProvisionRequest("nova", 1, 2)
	req.RuntimeProvider = domain.RuntimeProviderDaytona
	res, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	node := res.Node

	// Same generation, same sandbox id -- but the caller believes exe owns it.
	_, err = broker.Release(ctx, "WS", node.NodeID, ReleaseFence{
		Generation:      node.Placement.Generation,
		SandboxID:       node.Placement.SandboxID,
		RuntimeProvider: domain.RuntimeProviderExe,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("release with a mismatched provider fence err = %v, want ErrConflict", err)
	}
	if got := daytona.deleteCallCount(); got != 0 {
		t.Fatalf("daytona saw %d Delete call(s) despite a rejected fence", got)
	}

	// The correct provider fence releases.
	if _, err := broker.Release(ctx, "WS", node.NodeID, ReleaseFence{
		Generation:      node.Placement.Generation,
		SandboxID:       node.Placement.SandboxID,
		RuntimeProvider: domain.RuntimeProviderDaytona,
	}); err != nil {
		t.Fatalf("release with the matching provider fence: %v", err)
	}
}
