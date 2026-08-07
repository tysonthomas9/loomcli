package placement_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/placement"
	"github.com/tysonthomas9/loomcli/internal/placement/daytona"
)

// TestLiveLeadPlacementEndToEnd drives the real broker against the real Daytona
// API: provision a lead sandbox, confirm the durable placement record tracks
// the provider, confirm the lead PTY booted, then release and confirm the
// sandbox is actually gone.
//
// This is the POC acceptance chain at the layer that exists today (steps 1, 2
// and 7). It creates a real, billable sandbox and always releases it.
func TestLiveLeadPlacementEndToEnd(t *testing.T) {
	if os.Getenv("LOOM_DAYTONA_LIVE_TEST") != "1" || os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("live test disabled")
	}

	provider, err := daytona.New(daytona.Config{SnapshotName: "loom-lead-poc-v2"})
	if err != nil {
		t.Fatalf("daytona.New: %v", err)
	}
	store := memstore.New()
	key := []byte("0123456789abcdef0123456789abcdef")

	broker, err := placement.NewBroker(placement.Config{
		Store:        store,
		Provider:     provider,
		TokenKey:     key,
		DeploymentID: "mac-e2e",
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	const ws, agent = "LIVE-E2E", "nova"

	var placedNodeID, placedSandboxID string
	t.Cleanup(func() {
		if placedNodeID == "" {
			return
		}
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer ccancel()
		node, getErr := broker.Get(cctx, ws, placedNodeID)
		if getErr != nil || node == nil || node.Placement == nil {
			return
		}
		if node.Placement.State == domain.PlacementStateReleased {
			return
		}
		_, relErr := broker.Release(cctx, ws, placedNodeID, placement.ReleaseFence{
			Generation: node.Placement.Generation, Force: true,
		})
		if relErr != nil {
			t.Errorf("CLEANUP: force release failed, sandbox %q may still bill: %v", placedSandboxID, relErr)
		} else {
			t.Logf("CLEANUP: force-released %s", placedNodeID)
		}
	})

	// --- Step 1: provision ------------------------------------------------
	start := time.Now()
	res, err := broker.Provision(ctx, placement.ProvisionRequest{
		WorkspaceKey: ws,
		AgentName:    agent,
		SnapshotRef:  "loom-lead-poc-v2",
		Caps:         []string{placement.CapLeadSession},
		Resource:     placement.ResourceSize{VCPU: 2, MemGiB: 4},
		NetworkDomainAllowlist: []string{
			"app.daytona.io", "api.anthropic.com", "registry.npmjs.org", "github.com",
		},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	placedNodeID = res.Node.NodeID
	placedSandboxID = res.Node.Placement.SandboxID
	t.Logf("STEP 1 provision OK in %s", time.Since(start).Round(time.Millisecond))
	t.Logf("  placement node   = %s", placedNodeID)
	t.Logf("  provider sandbox = %s", placedSandboxID)
	t.Logf("  state            = %s (generation %d)", res.Node.Placement.State, res.Node.Placement.Generation)
	t.Logf("  lead started     = %v (err=%q)", res.LeadStarted, res.LeadStartError)

	if res.Node.Placement.State != domain.PlacementStateActive {
		t.Errorf("state = %q, want active at the spend boundary", res.Node.Placement.State)
	}
	if placedSandboxID == "" {
		t.Fatal("no sandbox id recorded -- the spend boundary was not reached")
	}

	// --- The occupant token the sandbox holds -----------------------------
	claims, err := leadtoken.ParseOccupantToken(res.Token, key)
	if err != nil {
		t.Fatalf("occupant token does not parse: %v", err)
	}
	if claims.PlacementID != placedNodeID || claims.Generation != res.Node.Placement.Generation {
		t.Errorf("token claims %s/gen%d do not match record %s/gen%d",
			claims.PlacementID, claims.Generation, placedNodeID, res.Node.Placement.Generation)
	}
	t.Logf("STEP 1b occupant token binds placement=%s gen=%d caps=%v",
		claims.PlacementID, claims.Generation, claims.Caps)

	// --- Step 2: the lead PTY actually exists in the sandbox --------------
	sessions, err := provider.ListPtySessions(ctx, placedSandboxID)
	if err != nil {
		t.Fatalf("ListPtySessions: %v", err)
	}
	t.Logf("STEP 2 pty sessions in sandbox = %v", sessions)
	foundLead := false
	for _, s := range sessions {
		if strings.TrimSpace(s.SessionID) == "lead" {
			foundLead = true
		}
	}
	if !foundLead {
		t.Errorf("no 'lead' PTY session: the lead was never booted")
	}

	// --- Idempotency: a second provision must not create a second sandbox --
	res2, err := broker.Provision(ctx, placement.ProvisionRequest{
		WorkspaceKey: ws, AgentName: agent, SnapshotRef: "loom-lead-poc-v2",
		Caps:     []string{placement.CapLeadSession},
		Resource: placement.ResourceSize{VCPU: 2, MemGiB: 4},
	})
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if res2.Node.Placement.SandboxID != placedSandboxID {
		t.Errorf("second provision made a new sandbox %q, want reuse of %q",
			res2.Node.Placement.SandboxID, placedSandboxID)
	} else {
		t.Logf("STEP 1c get-or-create reused sandbox %s (no double spend)", placedSandboxID)
	}

	// --- Step 7: release, and prove the sandbox is really gone ------------
	relStart := time.Now()
	released, err := broker.Release(ctx, ws, placedNodeID, placement.ReleaseFence{
		Generation: res.Node.Placement.Generation,
		SandboxID:  placedSandboxID,
	})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	t.Logf("STEP 7 release OK in %s -> state=%s", time.Since(relStart).Round(time.Millisecond), released.Placement.State)
	if released.Placement.State != domain.PlacementStateReleased {
		t.Errorf("state = %q, want released", released.Placement.State)
	}

	if _, err := provider.Get(ctx, placedSandboxID); errors.Is(err, placement.ErrSandboxNotFound) {
		t.Logf("STEP 7b provider confirms sandbox %s is gone", placedSandboxID)
	} else {
		sb, _ := provider.Get(ctx, placedSandboxID)
		if sb.State == placement.ProviderSandboxAbsent {
			t.Logf("STEP 7b provider reports sandbox %s absent", placedSandboxID)
		} else {
			t.Errorf("sandbox %s still present after release: state=%s err=%v", placedSandboxID, sb.State, err)
		}
	}
	placedNodeID = ""
}
