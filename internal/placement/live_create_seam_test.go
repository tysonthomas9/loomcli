package placement_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestLiveCreateSeamReconcile drives the create-seam reconcile machinery
// against the real Daytona API — the pieces whose behavior only the provider
// can prove: deterministic sandbox naming, the authoritative FindByName point
// read, adoption of a sandbox whose create response was lost, and the
// two-pass absence protocol observing real not-found responses.
//
// It creates real, billable sandboxes and always deletes them.
func TestLiveCreateSeamReconcile(t *testing.T) {
	if os.Getenv("LOOM_DAYTONA_LIVE_TEST") != "1" || os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("live test disabled")
	}

	const snapshot = "loom-lead-poc-v5"
	// A deployment id no deployed serve uses, so this test can never reconcile
	// against (or delete) another environment's sandboxes.
	const deployment = "pr1-create-seam-live"
	const ws, agent = "LIVE-SEAM", "nova"

	provider, err := daytona.New(daytona.Config{SnapshotName: snapshot})
	if err != nil {
		t.Fatalf("daytona.New: %v", err)
	}
	st := memstore.New()
	broker, err := placement.NewBroker(placement.Config{
		Store:        st,
		Provider:     provider,
		TokenKey:     []byte("0123456789abcdef0123456789abcdef"),
		DeploymentID: deployment,
		// Keep the two-pass wait test-sized; production default is minutes.
		CreateAbsenceReconfirmInterval: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	nodeID := liveSeamPlacementID(t)
	var createdIDs []string
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ccancel()
		for _, id := range createdIDs {
			if err := provider.Delete(cctx, id); err != nil && !errors.Is(err, placement.ErrSandboxNotFound) {
				t.Errorf("CLEANUP: delete sandbox %q failed, it may still bill: %v", id, err)
			}
		}
	})

	// --- Step 1: create with the deterministic name, exactly as the broker
	// would, then treat the response as lost (no id is recorded anywhere).
	created, err := provider.Create(ctx, placement.CreateRequest{
		WorkspaceKey: ws,
		AgentName:    agent,
		SnapshotRef:  snapshot,
		Name:         nodeID,
		Labels: map[string]string{
			placement.PlacementLabelKey:   nodeID,
			placement.EnvironmentLabelKey: deployment,
			"loom-workspace":              ws,
			"loom-agent":                  agent,
		},
		Resource: placement.ResourceSize{VCPU: 2, MemGiB: 4},
	})
	if err != nil {
		t.Fatalf("Create: %v (outcome=%q)", err, created.Outcome)
	}
	createdIDs = append(createdIDs, created.SandboxID)
	if created.Outcome != placement.CreateOutcomeCreated || created.SandboxID == "" {
		t.Fatalf("create result = %+v, want created with an id", created)
	}
	t.Logf("STEP 1 created sandbox %s named %s", created.SandboxID, nodeID)

	// --- Step 2: the name resolves as an authoritative point read, and a
	// name nothing holds maps to not-found (not to a lookup error).
	sandbox, err := provider.FindByName(ctx, nodeID)
	if err != nil {
		t.Fatalf("FindByName(%s): %v", nodeID, err)
	}
	if sandbox.ID != created.SandboxID {
		t.Fatalf("FindByName id = %q, want %q", sandbox.ID, created.SandboxID)
	}
	if got := sandbox.Labels[placement.PlacementLabelKey]; got != nodeID {
		t.Fatalf("placement label = %q, want %q", got, nodeID)
	}
	if _, err := provider.FindByName(ctx, "lead-placement-000000000000000000000000"); !errors.Is(err, placement.ErrSandboxNotFound) {
		t.Fatalf("FindByName(absent) = %v, want ErrSandboxNotFound", err)
	}
	t.Logf("STEP 2 FindByName resolves %s -> %s; absent name maps to not-found", nodeID, sandbox.ID)

	// --- Step 3 (platform probe): what does Daytona do with a duplicate name?
	// The broker never sends one on purpose, but a crashed-and-retried create
	// could. Record the platform's answer either way.
	dup, dupErr := provider.Create(ctx, placement.CreateRequest{
		WorkspaceKey: ws,
		AgentName:    agent,
		SnapshotRef:  snapshot,
		Name:         nodeID,
		Labels: map[string]string{
			placement.PlacementLabelKey:   nodeID,
			placement.EnvironmentLabelKey: deployment,
		},
		Resource: placement.ResourceSize{VCPU: 2, MemGiB: 4},
	})
	if dupErr == nil {
		createdIDs = append(createdIDs, dup.SandboxID)
		t.Logf("STEP 3 PLATFORM FACT: duplicate name ACCEPTED (second sandbox %s); deleting it", dup.SandboxID)
		if err := provider.Delete(ctx, dup.SandboxID); err != nil {
			t.Fatalf("delete duplicate sandbox: %v", err)
		}
	} else {
		t.Logf("STEP 3 PLATFORM FACT: duplicate name rejected (outcome=%q): %v", dup.Outcome, dupErr)
		if dup.Outcome == placement.CreateOutcomeNotDispatched {
			t.Errorf("duplicate-name rejection classified NotDispatched — a server-side reject must stay Unknown")
		}
	}

	// --- Step 4: the crash-after-dispatch shape — a provisioning row with no
	// sandbox id — reconciles to the live sandbox by name and adopts it.
	node := liveSeamNode(t, st, ws, nodeID, agent, domain.NodePlacement{
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateProvisioning,
		SnapshotRef:    snapshot,
	})
	got, found, err := broker.ReconcileProviderIdentityForTest(ctx, node)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !found || got.ID != created.SandboxID {
		t.Fatalf("reconcile = (%q, %v), want the live sandbox %q found", got.ID, found, created.SandboxID)
	}
	adopted, err := broker.RecordSandboxIDForTest(ctx, node, got.ID)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if adopted.Placement.State != domain.PlacementStateActive || adopted.Placement.SandboxID != created.SandboxID {
		t.Fatalf("adopted placement = %s/%q, want active/%q", adopted.Placement.State, adopted.Placement.SandboxID, created.SandboxID)
	}
	t.Logf("STEP 4 empty-id row adopted live sandbox %s (no second create)", created.SandboxID)

	// --- Step 5: release through the real path and prove the sandbox is gone.
	released, err := broker.Release(ctx, ws, nodeID, placement.ReleaseFence{
		Generation: 1,
		SandboxID:  created.SandboxID,
	})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if released.Placement.State != domain.PlacementStateReleased {
		t.Fatalf("state = %q, want released", released.Placement.State)
	}
	if sb, err := provider.Get(ctx, created.SandboxID); !errors.Is(err, placement.ErrSandboxNotFound) && sb.State != placement.ProviderSandboxAbsent {
		t.Fatalf("sandbox %s still present after release: state=%q err=%v", created.SandboxID, sb.State, err)
	}
	t.Logf("STEP 5 released; provider confirms %s gone", created.SandboxID)

	// --- Step 6: two-pass absence against real not-founds. A row whose create
	// truly made nothing releases only on the second confirmed zero.
	absentID := liveSeamPlacementID(t)
	past := time.Now().UTC().Add(-time.Minute)
	liveSeamNode(t, st, ws, absentID, "nova2", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &past,
		SnapshotRef:            snapshot,
	})
	if _, err := broker.Release(ctx, ws, absentID, placement.ReleaseFence{Generation: 1}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("first release pass = %v, want ErrConflict awaiting reconfirm", err)
	}
	stamped, err := st.Nodes().Get(ctx, ws, absentID)
	if err != nil || stamped == nil || stamped.Placement.CreateAbsenceConfirmedAt == nil {
		t.Fatalf("first pass did not stamp CreateAbsenceConfirmedAt (node=%+v err=%v)", stamped, err)
	}
	if stamped.Placement.State != domain.PlacementStateProvisioning {
		t.Fatalf("state after first pass = %q, want provisioning held", stamped.Placement.State)
	}
	t.Logf("STEP 6a first pass stamped absence at %s and held the row", stamped.Placement.CreateAbsenceConfirmedAt.Format(time.RFC3339))

	time.Sleep(6 * time.Second)
	released2, err := broker.Release(ctx, ws, absentID, placement.ReleaseFence{Generation: 1})
	if err != nil {
		t.Fatalf("second release pass: %v", err)
	}
	if released2.Placement.State != domain.PlacementStateReleased {
		t.Fatalf("state after second pass = %q, want released", released2.Placement.State)
	}
	if released2.Placement.ReleaseReason != domain.PlacementReleaseReasonCreateConfirmedAbsent {
		t.Fatalf("release reason = %q, want %q", released2.Placement.ReleaseReason, domain.PlacementReleaseReasonCreateConfirmedAbsent)
	}
	t.Logf("STEP 6b second pass released with reason %q", released2.Placement.ReleaseReason)
}

func liveSeamPlacementID(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "lead-placement-" + hex.EncodeToString(raw)
}

func liveSeamNode(t *testing.T, st store.Store, ws, nodeID, agent string, p domain.NodePlacement) *domain.Node {
	t.Helper()
	node, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    ws,
		NodeID:          nodeID,
		OwnerActor:      "agent:" + agent,
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement:       &p,
		Labels: []string{
			"loom-lead-placement",
			"loom-workspace=" + ws,
			"loom-agent=" + agent,
		},
		Capabilities:  []string{placement.CapLeadSession},
		ToolInventory: []string{"loom-lead"},
		DrainState:    domain.NodeDrainDrained,
		TTL:           30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create placement node: %v", err)
	}
	return node
}
