package memstore

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func mustAcquireOwnership(
	t *testing.T,
	st *Store,
	workspaceKey,
	agentID,
	nodeID,
	ownerID string,
) *domain.AgentOwnershipLease {
	t.Helper()
	lease, err := st.AgentOwnershipLeases().Acquire(t.Context(), store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: workspaceKey,
		AgentID:      agentID,
		NodeID:       nodeID,
		OwnerID:      ownerID,
		TTL:          time.Minute,
	})
	if err != nil {
		t.Fatalf("acquire ownership for %s: %v", agentID, err)
	}
	return lease
}

func TestControlPlaneStores(t *testing.T) {
	st := New()
	ctx := t.Context()

	node, err := st.Nodes().Create(ctx, store.NodeCreate{WorkspaceKey: "WS", NodeID: "node-1", RuntimeProvider: domain.RuntimeProviderLocal, TTL: time.Minute})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if node.NodeID != "node-1" || node.ExpiresAt.IsZero() {
		t.Fatalf("node = %+v", node)
	}
	drain := domain.NodeDrainDraining
	updated, err := st.Nodes().Update(ctx, "WS", "node-1", store.NodeUpdate{DrainState: &drain})
	if err != nil {
		t.Fatalf("update node: %v", err)
	}
	if updated.DrainState != drain {
		t.Fatalf("drain state = %q", updated.DrainState)
	}

	session, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "sess-1",
		AgentID:      "agent-1",
		NodeID:       "node-1",
		Status:       domain.AgentSessionRunning,
		TaskID:       "T-1",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.SessionID != "sess-1" {
		t.Fatalf("session = %+v", session)
	}
	sessions, err := st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{NodeID: "node-1", Status: domain.AgentSessionRunning})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "sess-1" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestAgentOwnershipLeaseListUsesEffectiveExpiryStatus(t *testing.T) {
	st := New()
	ctx := t.Context()
	mustAcquireOwnership(t, st, "WS", "agent-expired", "node-1", "owner-1")
	st.ownership.mu.Lock()
	st.ownership.items["WS"]["agent-expired"].ExpiresAt = time.Now().UTC().Add(-time.Minute)
	st.ownership.mu.Unlock()

	active, err := st.AgentOwnershipLeases().List(ctx, "WS", store.AgentOwnershipLeaseFilter{
		Status: domain.AgentLeaseActive,
	})
	if err != nil {
		t.Fatalf("list active leases: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active leases = %+v, want expired lease omitted", active)
	}
	expired, err := st.AgentOwnershipLeases().List(ctx, "WS", store.AgentOwnershipLeaseFilter{
		Status: domain.AgentLeaseExpired,
	})
	if err != nil {
		t.Fatalf("list expired leases: %v", err)
	}
	if len(expired) != 1 || expired[0].Status != domain.AgentLeaseExpired {
		t.Fatalf("expired leases = %+v, want one effective expired lease", expired)
	}
}

// TestAgentSessionFilter_KindAndParent guards the new filter dimensions
// added for the Agent.OrchestratorSessionID migration. Without these,
// callers couldn't ask "give me the orchestration session whose child is
// task session T" via the store interface — they had to list everything
// and filter client-side, which is what motivated keeping the
// denormalized OrchestratorSessionID cache on Agent in the first place.
func TestAgentSessionFilter_KindAndParent(t *testing.T) {
	st := New()
	ctx := t.Context()

	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "orch-1", AgentID: "nova",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create orch: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "task-1a", AgentID: "worker-a",
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-1a: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "task-1b", AgentID: "worker-b",
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-1b: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "task-x", AgentID: "worker-x",
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-other",
		Status: domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task-x: %v", err)
	}

	// Kind-only filter
	got, err := st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{Kind: domain.AgentSessionKindOrchestration})
	if err != nil {
		t.Fatalf("list kind=orch: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "orch-1" {
		t.Fatalf("kind=orch results: want [orch-1], got %v", sessionIDs(got))
	}

	// Parent-only filter
	got, err = st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{ParentSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("list parent=orch-1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parent=orch-1 results: want 2, got %v", sessionIDs(got))
	}

	// Combined: kind + parent
	got, err = st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{
		Kind: domain.AgentSessionKindTask, ParentSessionID: "orch-1",
	})
	if err != nil {
		t.Fatalf("list kind=task,parent=orch-1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("kind+parent results: want 2, got %v", sessionIDs(got))
	}

	// Mismatch returns empty
	got, err = st.AgentSessions().List(ctx, "WS", store.AgentSessionFilter{
		Kind: domain.AgentSessionKindOrchestration, ParentSessionID: "orch-1",
	})
	if err != nil {
		t.Fatalf("list mismatch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("mismatch results: want empty, got %v", sessionIDs(got))
	}
}

func sessionIDs(sessions []*domain.AgentSession) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if s != nil {
			ids = append(ids, s.SessionID)
		}
	}
	return ids
}
