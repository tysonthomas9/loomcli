package placement

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func mustBroker(t *testing.T, st store.Store, provider *fakeProvider) *Broker {
	t.Helper()
	return mustBrokerWithMax(t, st, provider, ResourceSize{})
}

func mustBrokerWithMax(t *testing.T, st store.Store, provider *fakeProvider, max ResourceSize) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{
		Store:        st,
		Provider:     provider,
		TokenKey:     testTokenKey,
		MaxLive:      max,
		DeploymentID: testDeploymentID,
		// The delete-confirm poll is real; keep its sleeps out of unit tests.
		DeleteConfirmBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	return broker
}

func mustBrokerWithNow(t *testing.T, st store.Store, provider *fakeProvider, now time.Time) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{
		Store:                st,
		Provider:             provider,
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

func testProvisionRequest(agent string, vcpu, memGiB int) ProvisionRequest {
	return ProvisionRequest{
		WorkspaceKey:           "WS",
		AgentName:              agent,
		SnapshotRef:            "snapshot://lead",
		Resource:               ResourceSize{VCPU: vcpu, MemGiB: memGiB},
		NetworkDomainAllowlist: []string{"api.loom.invalid"},
		Env:                    map[string]string{"CUSTOM": "value"},
		Labels:                 map[string]string{"custom": "value"},
	}
}

func mustProvision(t *testing.T, ctx context.Context, broker *Broker, agent string, vcpu, memGiB int) *ProvisionResult {
	t.Helper()
	result, err := broker.Provision(ctx, testProvisionRequest(agent, vcpu, memGiB))
	if err != nil {
		t.Fatalf("Provision %s: %v", agent, err)
	}
	return result
}

func releaseFence(node *domain.Node) ReleaseFence {
	return ReleaseFence{
		Generation: node.Placement.Generation,
		SandboxID:  node.Placement.SandboxID,
	}
}

func receiveResult(t *testing.T, results <-chan *ProvisionResult, errs <-chan error) *ProvisionResult {
	t.Helper()
	select {
	case err := <-errs:
		t.Fatalf("Provision error: %v", err)
	case result := <-results:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provision result")
	}
	return nil
}

func receiveCreateEntered(t *testing.T, entered <-chan string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for concurrent provider Create")
	}
}

func receiveState(t *testing.T, states <-chan domain.PlacementState) domain.PlacementState {
	t.Helper()
	select {
	case state := <-states:
		return state
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for provider Delete")
	}
	return ""
}

func parseCreateToken(t *testing.T, call CreateRequest) *leadtoken.OccupantClaims {
	t.Helper()
	token := call.Env[OccupantTokenEnv]
	if token == "" {
		t.Fatalf("create env missing %s", OccupantTokenEnv)
	}
	claims, err := leadtoken.ParseOccupantToken(token, testTokenKey)
	if err != nil {
		t.Fatalf("parse occupant token: %v", err)
	}
	return claims
}

func assertPlacement(t *testing.T, node *domain.Node, state domain.PlacementState, sandboxID string) {
	t.Helper()
	if node == nil || node.Placement == nil {
		t.Fatal("node placement missing")
	}
	if node.Placement.State != state || node.Placement.SandboxID != sandboxID {
		t.Fatalf("placement = state %q sandbox %q, want %q/%q",
			node.Placement.State, node.Placement.SandboxID, state, sandboxID)
	}
}

func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("slice = %v, want %v", got, want)
		}
	}
}

func assertDurationSlicesEqual(t *testing.T, got, want []time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("durations = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("durations = %v, want %v", got, want)
		}
	}
}

func getNodeState(t *testing.T, st store.Store, workspaceKey, nodeID string) domain.PlacementState {
	t.Helper()
	return getNode(t, st, workspaceKey, nodeID).Placement.State
}

func getNode(t *testing.T, st store.Store, workspaceKey, nodeID string) *domain.Node {
	t.Helper()
	node, err := st.Nodes().Get(context.Background(), workspaceKey, nodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	return node
}

func onlyNode(t *testing.T, st store.Store, workspaceKey string) *domain.Node {
	t.Helper()
	nodes, err := st.Nodes().List(context.Background(), workspaceKey)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	return nodes[0]
}

func createWorkspace(t *testing.T, st store.Store, key string) {
	t.Helper()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: key, Name: key}); err != nil {
		t.Fatalf("create workspace %s: %v", key, err)
	}
}

func createRepo(t *testing.T, st store.Store, workspace, name, remoteURL, defaultBranch string) {
	t.Helper()
	if _, err := st.Repos().Create(context.Background(), store.RepoCreate{
		WorkspaceKey:  workspace,
		Name:          name,
		RemoteURL:     remoteURL,
		DefaultBranch: defaultBranch,
	}); err != nil {
		t.Fatalf("create repo %s/%s: %v", workspace, name, err)
	}
}

func createPlacementNode(t *testing.T, st store.Store, workspaceKey, nodeID, agentName string, placement domain.NodePlacement) *domain.Node {
	t.Helper()
	placementPtr := &placement
	node, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    workspaceKey,
		NodeID:          nodeID,
		OwnerActor:      agentOwnerActor(agentName),
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement:       placementPtr,
		Labels: []string{
			"loom-lead-placement",
			"loom-workspace=" + workspaceKey,
			"loom-agent=" + agentName,
		},
		Capabilities:  []string{CapLeadSession},
		ToolInventory: []string{"loom-lead"},
		DrainState:    domain.NodeDrainDrained,
		TTL:           defaultNodeTTL,
	})
	if err != nil {
		t.Fatalf("create placement node: %v", err)
	}
	return node
}

func markPlacementState(t *testing.T, st store.Store, workspaceKey, nodeID string, state domain.PlacementState) {
	t.Helper()
	node := getNode(t, st, workspaceKey, nodeID)
	placement := clonePlacement(node.Placement)
	placement.State = state
	placementPtr := &placement
	if _, err := st.Nodes().Update(context.Background(), workspaceKey, nodeID, store.NodeUpdate{Placement: &placementPtr}); err != nil {
		t.Fatalf("mark placement state: %v", err)
	}
}

func patchSandboxID(patch store.NodeUpdate) string {
	if patch.Placement == nil || *patch.Placement == nil {
		return ""
	}
	return strings.TrimSpace((*patch.Placement).SandboxID)
}
