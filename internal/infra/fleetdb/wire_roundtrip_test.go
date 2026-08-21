package fleetdb

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestAgentWireRuntimeProviderRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(agentWire{
		WorkspaceKey:         "WS",
		Name:                 "nova",
		RoleName:             "lead",
		RuntimeProvider:      string(domain.RuntimeProviderDaytona),
		LastProvisionOutcome: domain.LeadProvisionOutcomeFailed,
		LastProvisionError:   "credentials rejected",
		LastProvisionAt:      &at,
	})
	if err != nil {
		t.Fatalf("marshal agentWire: %v", err)
	}

	var out agentWire
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal agentWire: %v", err)
	}
	agent := out.toDomain()
	if got := agent.RuntimeProvider; got != domain.RuntimeProviderDaytona {
		t.Fatalf("RuntimeProvider = %q, want daytona", got)
	}
	if agent.LastProvisionOutcome != domain.LeadProvisionOutcomeFailed || agent.LastProvisionError != "credentials rejected" || agent.LastProvisionAt == nil || !agent.LastProvisionAt.Equal(at) {
		t.Fatalf("provision attempt = %q/%q/%v, want failed/credentials rejected/%v", agent.LastProvisionOutcome, agent.LastProvisionError, agent.LastProvisionAt, at)
	}
}

func TestDaemonProfileWireRuntimeProviderRoundTrip(t *testing.T) {
	raw, err := json.Marshal(daemonProfileWire{
		WorkspaceKey:    "WS",
		RuntimeProvider: domain.RuntimeProviderDaytona,
	})
	if err != nil {
		t.Fatalf("marshal daemonProfileWire: %v", err)
	}

	var out daemonProfileWire
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal daemonProfileWire: %v", err)
	}
	if got := out.toDomain().RuntimeProvider; got != domain.RuntimeProviderDaytona {
		t.Fatalf("RuntimeProvider = %q, want daytona", got)
	}

	upsertRaw, err := json.Marshal(domainToUpsertWire(&domain.DaemonProfile{
		WorkspaceKey:    "WS",
		RuntimeProvider: domain.RuntimeProviderDaytona,
	}))
	if err != nil {
		t.Fatalf("marshal daemonProfileUpsertWire: %v", err)
	}
	var upsert daemonProfileUpsertWire
	if err := json.Unmarshal(upsertRaw, &upsert); err != nil {
		t.Fatalf("unmarshal daemonProfileUpsertWire: %v", err)
	}
	if upsert.RuntimeProvider != domain.RuntimeProviderDaytona {
		t.Fatalf("upsert RuntimeProvider = %q, want daytona", upsert.RuntimeProvider)
	}
}

func TestNodeWirePlacementRoundTripAndOmit(t *testing.T) {
	firstAttached := time.Date(2026, 8, 6, 13, 0, 0, 0, time.UTC)
	leadStarted := time.Date(2026, 8, 6, 13, 1, 0, 0, time.UTC)
	provisioningDeadline := time.Date(2026, 8, 6, 13, 5, 0, 0, time.UTC)
	nextDelete := time.Date(2026, 8, 6, 13, 10, 0, 0, time.UTC)
	withPlacement := nodeWire{
		WorkspaceKey:    "WS",
		NodeID:          "node-1",
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement: &domain.NodePlacement{
			Generation:             7,
			State:                  domain.PlacementStateProvisioning,
			FirstAttachedAt:        &firstAttached,
			LeadProcessStartedAt:   &leadStarted,
			ProvisioningDeadlineAt: &provisioningDeadline,
			SnapshotRef:            "snapshot-1",
			AbandonedSandboxIDs:    []string{"abandoned-1"},
			DeleteAttempts:         3,
			LastDeleteError:        "still running",
			NextDeleteAt:           nextDelete,
		},
	}
	raw, err := json.Marshal(withPlacement)
	if err != nil {
		t.Fatalf("marshal nodeWire with placement: %v", err)
	}
	var out nodeWire
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal nodeWire with placement: %v", err)
	}
	node := out.toDomain()
	if node.Placement == nil {
		t.Fatal("Placement = nil, want placement")
	}
	if node.Placement.SandboxID != "" || node.Placement.State != domain.PlacementStateProvisioning || node.Placement.Generation != 7 {
		t.Fatalf("Placement = %+v, want provisioning generation 7 with empty sandbox_id", node.Placement)
	}
	if node.Placement.FirstAttachedAt == nil || !node.Placement.FirstAttachedAt.Equal(firstAttached) {
		t.Fatalf("FirstAttachedAt = %v, want %v", node.Placement.FirstAttachedAt, firstAttached)
	}
	if node.Placement.LeadProcessStartedAt == nil || !node.Placement.LeadProcessStartedAt.Equal(leadStarted) {
		t.Fatalf("LeadProcessStartedAt = %v, want %v", node.Placement.LeadProcessStartedAt, leadStarted)
	}
	if node.Placement.ProvisioningDeadlineAt == nil || !node.Placement.ProvisioningDeadlineAt.Equal(provisioningDeadline) {
		t.Fatalf("ProvisioningDeadlineAt = %v, want %v", node.Placement.ProvisioningDeadlineAt, provisioningDeadline)
	}
	if !reflect.DeepEqual(node.Placement.AbandonedSandboxIDs, []string{"abandoned-1"}) {
		t.Fatalf("AbandonedSandboxIDs = %v, want [abandoned-1]", node.Placement.AbandonedSandboxIDs)
	}
	if node.Placement.DeleteAttempts != 3 || node.Placement.LastDeleteError != "still running" || !node.Placement.NextDeleteAt.Equal(nextDelete) {
		t.Fatalf("delete retry fields = %d/%q/%v, want 3/still running/%v",
			node.Placement.DeleteAttempts, node.Placement.LastDeleteError, node.Placement.NextDeleteAt, nextDelete)
	}

	withoutPlacement := nodeWire{
		WorkspaceKey:    "WS",
		NodeID:          "node-host",
		RuntimeProvider: domain.RuntimeProviderLocal,
	}
	raw, err = json.Marshal(withoutPlacement)
	if err != nil {
		t.Fatalf("marshal nodeWire without placement: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("unmarshal nodeWire keys: %v", err)
	}
	if _, ok := keys["placement"]; ok {
		t.Fatalf("node without placement emitted placement key: %s", raw)
	}
	out = nodeWire{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal nodeWire without placement: %v", err)
	}
	if node := out.toDomain(); node.Placement != nil {
		t.Fatalf("Placement = %+v, want nil", node.Placement)
	}
}

func TestNodeWirePreservesEveryDomainField(t *testing.T) {
	firstAttached := time.Date(2026, 8, 6, 13, 0, 0, 0, time.UTC)
	leadStarted := time.Date(2026, 8, 6, 13, 1, 0, 0, time.UTC)
	provisioningDeadline := time.Date(2026, 8, 6, 13, 5, 0, 0, time.UTC)
	nextDelete := time.Date(2026, 8, 6, 13, 10, 0, 0, time.UTC)
	want := &domain.Node{
		WorkspaceKey:    "WS",
		NodeID:          "node-1",
		OwnerActor:      "serve",
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement: &domain.NodePlacement{
			SandboxID:              "sandbox-1",
			Generation:             7,
			ReservedVCPU:           4,
			ReservedMemGiB:         8,
			State:                  domain.PlacementStateActive,
			FirstAttachedAt:        &firstAttached,
			LeadProcessStartedAt:   &leadStarted,
			ProvisioningDeadlineAt: &provisioningDeadline,
			SnapshotRef:            "snapshot-1",
			AbandonedSandboxIDs:    []string{"abandoned-1", "abandoned-2"},
			DeleteAttempts:         4,
			LastDeleteError:        "delete failed",
			NextDeleteAt:           nextDelete,
		},
		Labels:        []string{"role:lead"},
		Capabilities:  []string{"terminal"},
		ToolInventory: []string{"codex"},
		Version:       "v1.2.3",
		Capacity:      2,
		DrainState:    domain.NodeDrainDraining,
		LastHeartbeat: time.Date(2026, 8, 6, 13, 1, 0, 0, time.UTC),
		ExpiresAt:     time.Date(2026, 8, 6, 13, 2, 0, 0, time.UTC),
		CreatedAt:     time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 8, 6, 13, 3, 0, 0, time.UTC),
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal domain.Node: %v", err)
	}
	var wire nodeWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal domain.Node into nodeWire: %v", err)
	}
	if got := wire.toDomain(); !reflect.DeepEqual(got, want) {
		t.Fatalf("nodeWire.toDomain() = %#v, want %#v", got, want)
	}
}

// TestNodeWireCoversDomainNodeFields fails when domain.Node grows a field that
// nodeWire does not mirror. The round-trip test above only exercises fields it
// populates, so it cannot see a field added to both domain.Node and nowhere
// else. Node reads decoded straight into domain.Node before nodeWire existed,
// which made every field survive for free; a hand-maintained wire struct drops
// a forgotten field silently, zeroing it on every read.
func TestNodeWireCoversDomainNodeFields(t *testing.T) {
	wire := reflect.TypeOf(nodeWire{})
	mirrored := make(map[string]bool, wire.NumField())
	for i := range wire.NumField() {
		mirrored[wire.Field(i).Name] = true
	}

	node := reflect.TypeOf(domain.Node{})
	for i := range node.NumField() {
		field := node.Field(i)
		if !mirrored[field.Name] {
			t.Errorf("domain.Node.%s has no nodeWire counterpart: add it to nodeWire and toDomain", field.Name)
		}
	}
}
