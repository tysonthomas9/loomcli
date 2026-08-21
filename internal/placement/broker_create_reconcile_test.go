package placement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestProvisionAdoptsLivePredecessorSandboxInsteadOfSuccessor(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	// The leak shape: a released row whose create actually made a sandbox that
	// is still alive under the placement's deterministic name and label.
	node := createPlacementNode(t, st, "WS", "lead-placement-leaked", "nova", domain.NodePlacement{
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateReleased,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-leaked",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
		},
		State: ProviderSandboxRunning,
	})
	provider.setSandboxName(sandboxNameForPlacement(node.NodeID), "sandbox-leaked")
	broker := mustBroker(t, st, provider)

	result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.Node.NodeID != node.NodeID {
		t.Fatalf("node id = %q, want the predecessor %q adopted, not a successor", result.Node.NodeID, node.NodeID)
	}
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-leaked")
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 — a successor create doubles the bill", got)
	}
}

func TestProvisionCrashedCreateAdoptsListInvisibleSandboxByName(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	// Crash-after-dispatch shape: serve died between the provider create and
	// the id write, and Daytona's eventually-consistent list does not return
	// the sandbox yet. Only the deterministic-name point read can see it.
	node := createPlacementNode(t, st, "WS", "lead-placement-crashed", "nova", domain.NodePlacement{
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateProvisioning,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addListInvisibleSandbox(ProviderSandbox{
		ID: "sandbox-crashed",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
		},
		State: ProviderSandboxRunning,
	})
	provider.setSandboxName(sandboxNameForPlacement(node.NodeID), "sandbox-crashed")
	broker := mustBroker(t, st, provider)

	result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.Node.NodeID != node.NodeID {
		t.Fatalf("node id = %q, want the crashed row %q adopted", result.Node.NodeID, node.NodeID)
	}
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-crashed")
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0", got)
	}
}

func TestProvisionMultipleLabelMatchesBlocksWithDurableAttention(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	past := time.Now().UTC().Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-multi", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &past,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{}
	for _, id := range []string{"sandbox-a", "sandbox-b"} {
		provider.addSandbox(ProviderSandbox{
			ID: id,
			Labels: map[string]string{
				PlacementLabelKey:   node.NodeID,
				EnvironmentLabelKey: testDeploymentID,
			},
			State: ProviderSandboxRunning,
		})
	}
	broker := mustBroker(t, st, provider)

	if _, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision = %v, want ErrConflict on multiple label matches", err)
	}
	got := getNode(t, st, "WS", node.NodeID)
	assertPlacement(t, got, domain.PlacementStateProvisioning, "")
	if got.Placement.AttentionReason == "" {
		t.Fatal("AttentionReason empty, want the multiple-match block persisted durably")
	}
	if !PlacementNeedsAttention(got) {
		t.Fatal("PlacementNeedsAttention = false, want true for a durably blocked placement")
	}
	if got.Placement.CreateAbsenceConfirmedAt != nil {
		t.Fatal("CreateAbsenceConfirmedAt recorded despite matching sandboxes")
	}
}

func TestProvisionNameCollisionBlocksWithDurableAttention(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	past := time.Now().UTC().Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-collide", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &past,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{}
	// A foreign sandbox holds this placement's deterministic name but carries
	// another placement's label: adopting it would hijack someone else's
	// sandbox, and ignoring it would falsely confirm absence.
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-foreign",
		Labels: map[string]string{
			PlacementLabelKey:   "some-other-placement",
			EnvironmentLabelKey: testDeploymentID,
		},
		State: ProviderSandboxRunning,
	})
	provider.setSandboxName(sandboxNameForPlacement(node.NodeID), "sandbox-foreign")
	broker := mustBroker(t, st, provider)

	if _, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision = %v, want ErrConflict on a name collision", err)
	}
	got := getNode(t, st, "WS", node.NodeID)
	assertPlacement(t, got, domain.PlacementStateProvisioning, "")
	if !strings.Contains(got.Placement.AttentionReason, "name") {
		t.Fatalf("AttentionReason = %q, want the name collision recorded", got.Placement.AttentionReason)
	}
	if got.Placement.CreateAbsenceConfirmedAt != nil {
		t.Fatal("CreateAbsenceConfirmedAt recorded despite a name-visible sandbox")
	}
}

func TestProvisionListErrorIsNotAbsence(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	past := time.Now().UTC().Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-listerr", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &past,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{listErr: errors.New("throttled")}
	broker := mustBroker(t, st, provider)

	if _, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4)); err == nil {
		t.Fatal("Provision succeeded, want the list error surfaced")
	}
	got := getNode(t, st, "WS", node.NodeID)
	assertPlacement(t, got, domain.PlacementStateProvisioning, "")
	// A failed lookup proves nothing about absence: the protocol must not
	// advance, or repeated provider outages walk the row to release.
	if got.Placement.CreateAbsenceConfirmedAt != nil {
		t.Fatal("CreateAbsenceConfirmedAt recorded on a lookup failure")
	}
}

func TestRecordSandboxIDRejectsLostAndReleasingRows(t *testing.T) {
	ctx := context.Background()
	for _, state := range []domain.PlacementState{domain.PlacementStateLost, domain.PlacementStateReleasing} {
		st := memstore.New()
		node := createPlacementNode(t, st, "WS", "lead-placement-midway", "nova", domain.NodePlacement{
			Generation:     1,
			ReservedVCPU:   2,
			ReservedMemGiB: 4,
			State:          state,
			SnapshotRef:    "snapshot://lead",
		})
		broker := mustBroker(t, st, &fakeProvider{})
		if _, err := broker.recordSandboxID(ctx, node, "sandbox-x"); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("recordSandboxID on %s row = %v, want ErrConflict", state, err)
		}
	}
}

func TestRecordSandboxIDRejectsSupersededGeneration(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	old := createPlacementNode(t, st, "WS", "lead-placement-old", "nova", domain.NodePlacement{
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateReleased,
		SnapshotRef:    "snapshot://lead",
	})
	createPlacementNode(t, st, "WS", "lead-placement-new", "nova", domain.NodePlacement{
		Generation:     2,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateProvisioning,
		SnapshotRef:    "snapshot://lead",
	})
	broker := mustBroker(t, st, &fakeProvider{})

	// Resurrecting generation 1 while generation 2 is live would put two
	// active placements under one agent.
	if _, err := broker.recordSandboxID(ctx, old, "sandbox-x"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("recordSandboxID on superseded row = %v, want ErrConflict", err)
	}
}

func TestRecordSandboxIDClearsAmbiguityOnAdoption(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	ambiguousAt := time.Now().UTC().Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-heal", "nova", domain.NodePlacement{
		Generation:               1,
		ReservedVCPU:             2,
		ReservedMemGiB:           4,
		State:                    domain.PlacementStateProvisioning,
		ProvisionAmbiguousAt:     &ambiguousAt,
		ProvisionAmbiguityDetail: "connection reset",
		CreateAbsenceConfirmedAt: &ambiguousAt,
		AttentionReason:          "stale",
		SnapshotRef:              "snapshot://lead",
	})
	broker := mustBroker(t, st, &fakeProvider{})

	updated, err := broker.recordSandboxID(ctx, node, "sandbox-x")
	if err != nil {
		t.Fatalf("recordSandboxID: %v", err)
	}
	p := updated.Placement
	if p.ProvisionAmbiguousAt != nil || p.ProvisionAmbiguityDetail != "" || p.CreateAbsenceConfirmedAt != nil || p.AttentionReason != "" {
		t.Fatalf("ambiguity fields survived adoption: %+v", p)
	}
}
