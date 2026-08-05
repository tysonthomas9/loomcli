package memstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func prooflessCommandAck(nodeID, ownerID string) store.AgentCommandAck {
	return store.AgentCommandAck{NodeID: nodeID, OwnerID: ownerID}
}

func commandOwnershipProof(
	lease *domain.AgentOwnershipLease,
) store.AgentCommandOwnershipProof {
	return store.AgentCommandOwnershipProof{
		OwnershipLeaseID:      lease.LeaseID,
		OwnershipToken:        lease.Token,
		OwnershipFencingToken: lease.FencingToken,
	}
}

func commandAckWithOwnership(lease *domain.AgentOwnershipLease) store.AgentCommandAck {
	return store.AgentCommandAck{
		NodeID:                     lease.NodeID,
		OwnerID:                    lease.OwnerID,
		AgentCommandOwnershipProof: commandOwnershipProof(lease),
	}
}

func mustAcquireCommandOwnership(
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

func TestAgentCommandAckRejectsTerminalCommand(t *testing.T) {
	st := New()
	ctx := t.Context()
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-1",
		TargetAgentID: "agent-1",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	if _, err := st.AgentCommands().Ack(ctx, "WS", command.CommandID, prooflessCommandAck("node-1", "owner-1")); err != nil {
		t.Fatalf("ack command: %v", err)
	}
	if _, err := st.AgentCommands().Complete(ctx, "WS", command.CommandID, store.AgentCommandComplete{
		NodeID: "node-1", OwnerID: "owner-1", Status: domain.AgentCommandCancelled,
	}); err != nil {
		t.Fatalf("cancel command: %v", err)
	}
	if _, err := st.AgentCommands().Ack(ctx, "WS", command.CommandID, prooflessCommandAck("node-1", "owner-1")); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ack cancelled command error = %v, want ErrInvalidTransition", err)
	}
}

func TestAgentCommandAckEnforcesPerAgentFIFO(t *testing.T) {
	st := New()
	ctx := t.Context()
	first, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-first",
		TargetAgentID: "agent-1",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create first command: %v", err)
	}
	second, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-second",
		TargetAgentID: "agent-1",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create second command: %v", err)
	}
	otherAgent, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-other-agent",
		TargetAgentID: "agent-2",
		Type:          "start",
	})
	if err != nil {
		t.Fatalf("create other-agent command: %v", err)
	}

	if _, err := st.AgentCommands().Ack(ctx, "WS", second.CommandID, prooflessCommandAck("node-2", "owner-2")); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ack second while first queued error = %v, want ErrInvalidTransition", err)
	}
	if _, err := st.AgentCommands().Ack(ctx, "WS", otherAgent.CommandID, prooflessCommandAck("node-3", "owner-3")); err != nil {
		t.Fatalf("ack unrelated agent command: %v", err)
	}
	firstLease := mustAcquireCommandOwnership(t, st, "WS", "agent-1", "node-1", "owner-1")
	firstAck := commandAckWithOwnership(firstLease)
	first, err = st.AgentCommands().Ack(ctx, "WS", first.CommandID, firstAck)
	if err != nil {
		t.Fatalf("ack first command: %v", err)
	}
	if _, err := st.AgentCommands().Ack(ctx, "WS", second.CommandID, prooflessCommandAck("node-2", "owner-2")); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ack second while first acked error = %v, want ErrInvalidTransition", err)
	}
	first, err = st.AgentCommands().Complete(ctx, "WS", first.CommandID, store.AgentCommandComplete{
		NodeID:                     "node-1",
		OwnerID:                    "owner-1",
		Status:                     domain.AgentCommandRunning,
		AgentCommandOwnershipProof: firstAck.AgentCommandOwnershipProof,
	})
	if err != nil {
		t.Fatalf("mark first command running: %v", err)
	}
	if _, err := st.AgentCommands().Ack(ctx, "WS", second.CommandID, prooflessCommandAck("node-2", "owner-2")); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ack second while first running error = %v, want ErrInvalidTransition", err)
	}
	if _, err := st.AgentOwnershipLeases().Release(ctx, "WS", "agent-1", firstLease.Token); err != nil {
		t.Fatalf("release first command ownership: %v", err)
	}
	if _, err := st.AgentCommands().Complete(ctx, "WS", first.CommandID, store.AgentCommandComplete{
		NodeID:                     "node-1",
		OwnerID:                    "owner-1",
		Status:                     domain.AgentCommandSucceeded,
		AgentCommandOwnershipProof: firstAck.AgentCommandOwnershipProof,
	}); err != nil {
		t.Fatalf("complete first command: %v", err)
	}
	ackedSecond, err := st.AgentCommands().Ack(ctx, "WS", second.CommandID, prooflessCommandAck("node-2", "owner-2"))
	if err != nil {
		t.Fatalf("ack second after first terminal: %v", err)
	}
	if ackedSecond.Status != domain.AgentCommandAcked || ackedSecond.AckedBy != "owner-2" {
		t.Fatalf("acked second command = %+v, want owner-2 Ack", ackedSecond)
	}

	emptyTargetFirst, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey: "WS",
		CommandID:    "empty-target-first",
		Type:         "start",
	})
	if err != nil {
		t.Fatalf("create first empty-target command: %v", err)
	}
	emptyTargetSecond, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey: "WS",
		CommandID:    "empty-target-second",
		Type:         "start",
	})
	if err != nil {
		t.Fatalf("create second empty-target command: %v", err)
	}
	if _, err := st.AgentCommands().Ack(
		ctx, "WS", emptyTargetSecond.CommandID, prooflessCommandAck("node-empty", "owner-empty"),
	); err != nil {
		t.Fatalf(
			"ack empty-target command behind cursor %d command %q: %v",
			emptyTargetFirst.Cursor,
			emptyTargetFirst.CommandID,
			err,
		)
	}
}

func TestAgentCommandLifecycleIsIdempotentAndOwnerFenced(t *testing.T) {
	st := New()
	ctx := t.Context()
	create := store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-fenced",
		TargetAgentID: "agent-1",
		Type:          "restart",
		Payload:       map[string]string{"reason": "test"},
	}
	command, err := st.AgentCommands().Create(ctx, create)
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	originalLease := mustAcquireCommandOwnership(t, st, "WS", "agent-1", "node-original", "stable-owner")
	originalAck := commandAckWithOwnership(originalLease)
	command, err = st.AgentCommands().Ack(ctx, "WS", command.CommandID, originalAck)
	if err != nil {
		t.Fatalf("ack command: %v", err)
	}
	claimTime := *command.AckedAt

	replayedCreate, err := st.AgentCommands().Create(ctx, create)
	if err != nil {
		t.Fatalf("same-ID Create after Ack: %v", err)
	}
	if replayedCreate.Status != domain.AgentCommandAcked ||
		replayedCreate.TargetNodeID != "node-original" ||
		replayedCreate.AckedBy != "stable-owner" {
		t.Fatalf("same-ID Create replay = %+v, want current claimed command", replayedCreate)
	}
	conflictingCreate := create
	conflictingCreate.Type = "stop"
	if _, err := st.AgentCommands().Create(ctx, conflictingCreate); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("different-intent Create replay error = %v, want ErrAlreadyExists", err)
	}
	targetConflict := create
	targetConflict.TargetNodeID = "different-node"
	if _, err := st.AgentCommands().Create(ctx, targetConflict); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("mismatched nonempty target replay error = %v, want ErrAlreadyExists", err)
	}

	replacementLease := mustAcquireCommandOwnership(t, st, "WS", "agent-1", "node-replacement", "stable-owner")
	replacementProof := commandOwnershipProof(replacementLease)
	rebound, err := st.AgentCommands().Get(ctx, "WS", command.CommandID)
	if err != nil {
		t.Fatalf("get rebound command: %v", err)
	}
	if rebound.OwnershipLeaseID != replacementLease.LeaseID ||
		rebound.OwnershipFencingToken != replacementLease.FencingToken ||
		rebound.TargetNodeID != "node-original" ||
		rebound.AckedBy != "stable-owner" {
		t.Fatalf("same-owner acquire did not preserve claimant and advance generation: %+v", rebound)
	}
	running, err := st.AgentCommands().Complete(ctx, "WS", command.CommandID, store.AgentCommandComplete{
		NodeID:                     "node-replacement",
		OwnerID:                    "stable-owner",
		Status:                     domain.AgentCommandRunning,
		AgentCommandOwnershipProof: replacementProof,
	})
	if err != nil {
		t.Fatalf("mark command running: %v", err)
	}
	if running.Status != domain.AgentCommandRunning ||
		running.TargetNodeID != "node-original" ||
		running.AckedBy != "stable-owner" ||
		running.AckedAt == nil ||
		!running.AckedAt.Equal(claimTime) {
		t.Fatalf("running command changed claim metadata: %+v", running)
	}

	completion := store.AgentCommandComplete{
		NodeID: "node-replacement", OwnerID: "stable-owner",
		Status:                     domain.AgentCommandSucceeded,
		Result:                     "restarted",
		AgentCommandOwnershipProof: replacementProof,
	}
	completed, err := st.AgentCommands().Complete(ctx, "WS", command.CommandID, completion)
	if err != nil {
		t.Fatalf("complete command: %v", err)
	}
	if completed.Status != domain.AgentCommandSucceeded ||
		completed.TargetNodeID != "node-original" ||
		completed.AckedBy != "stable-owner" ||
		completed.AckedAt == nil ||
		!completed.AckedAt.Equal(claimTime) {
		t.Fatalf("completed command changed claim metadata: %+v", completed)
	}
	if completed.OwnershipLeaseID != replacementLease.LeaseID ||
		completed.OwnershipFencingToken != replacementLease.FencingToken {
		t.Fatalf("completed command ownership = (%q,%d), want replacement (%q,%d)",
			completed.OwnershipLeaseID,
			completed.OwnershipFencingToken,
			replacementLease.LeaseID,
			replacementLease.FencingToken,
		)
	}
	if _, err := st.AgentOwnershipLeases().Release(ctx, "WS", "agent-1", replacementLease.Token); err != nil {
		t.Fatalf("release replacement ownership: %v", err)
	}
	if replayed, replayErr := st.AgentCommands().Complete(ctx, "WS", command.CommandID, completion); replayErr != nil ||
		replayed.Status != domain.AgentCommandSucceeded {
		t.Fatalf("exact completion replay = %+v, err=%v", replayed, replayErr)
	}
	different := completion
	different.Result = "different"
	if _, err := st.AgentCommands().Complete(ctx, "WS", command.CommandID, different); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("different completion replay error = %v, want ErrInvalidTransition", err)
	}
	wrongOwner := completion
	wrongOwner.OwnerID = "other-owner"
	if _, err := st.AgentCommands().Complete(ctx, "WS", command.CommandID, wrongOwner); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("wrong-owner completion error = %v, want ErrNotOwner", err)
	}
}

func TestAgentCommandProoflessAbsenceLifecycleRules(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		status     domain.AgentCommandStatus
		wantErr    error
		errorClass string
	}{
		{name: "stop succeeds idempotently", command: "stop", status: domain.AgentCommandSucceeded},
		{name: "start failure converges", command: "start", status: domain.AgentCommandFailed, errorClass: "control_error"},
		{name: "restart cancellation converges", command: "restart", status: domain.AgentCommandCancelled},
		{name: "yield failure converges", command: "yield", status: domain.AgentCommandFailed, errorClass: "control_error"},
		{name: "start success needs proof", command: "start", status: domain.AgentCommandSucceeded, wantErr: domain.ErrNotOwner},
		{name: "restart success needs proof", command: "restart", status: domain.AgentCommandSucceeded, wantErr: domain.ErrNotOwner},
		{name: "yield success needs proof", command: "yield", status: domain.AgentCommandSucceeded, wantErr: domain.ErrNotOwner},
		{name: "running always needs proof", command: "stop", status: domain.AgentCommandRunning, wantErr: domain.ErrNotOwner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := New()
			ctx := t.Context()
			command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
				WorkspaceKey:  "WS",
				CommandID:     "command-" + strings.ReplaceAll(tt.name, " ", "-"),
				TargetAgentID: "agent-1",
				Type:          tt.command,
			})
			if err != nil {
				t.Fatalf("create command: %v", err)
			}
			command, err = st.AgentCommands().Ack(
				ctx,
				"WS",
				command.CommandID,
				prooflessCommandAck("node-1", "stable-owner"),
			)
			if err != nil {
				t.Fatalf("proofless Ack: %v", err)
			}
			if command.OwnershipLeaseID != "" || command.OwnershipFencingToken != 0 {
				t.Fatalf("proofless Ack persisted ownership generation: %+v", command)
			}
			_, err = st.AgentCommands().Complete(ctx, "WS", command.CommandID, store.AgentCommandComplete{
				NodeID:     "node-1",
				OwnerID:    "stable-owner",
				Status:     tt.status,
				Result:     "result",
				ErrorClass: tt.errorClass,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Complete error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentCommandProoflessAckRejectsLiveOrUnknownAggregate(t *testing.T) {
	st := New()
	ctx := t.Context()
	mustAcquireCommandOwnership(t, st, "WS", "agent-live", "node-live", "owner-live")
	live, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-live",
		TargetAgentID: "agent-live",
		Type:          "stop",
	})
	if err != nil {
		t.Fatalf("create live command: %v", err)
	}
	if _, err := st.AgentCommands().Ack(
		ctx,
		"WS",
		live.CommandID,
		prooflessCommandAck("node-other", "owner-other"),
	); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("proofless Ack with live lease error = %v, want ErrNotOwner", err)
	}

	unknown, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-unknown",
		TargetAgentID: "agent-unknown",
		Type:          "future-operation",
	})
	if err != nil {
		t.Fatalf("create unknown command: %v", err)
	}
	if _, err := st.AgentCommands().Ack(
		ctx,
		"WS",
		unknown.CommandID,
		prooflessCommandAck("node-1", "owner-1"),
	); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("proofless unknown Ack error = %v, want ErrNotOwner", err)
	}
}

func TestAgentCommandMissingOwnershipLeaseIsNotOwner(t *testing.T) {
	st := New()
	ctx := t.Context()
	lease := mustAcquireCommandOwnership(t, st, "WS", "agent-1", "node-1", "owner-1")
	command, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  "WS",
		CommandID:     "command-missing-lease",
		TargetAgentID: "agent-1",
		Type:          "restart",
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	ack := commandAckWithOwnership(lease)
	if _, err := st.AgentCommands().Ack(ctx, "WS", command.CommandID, ack); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	st.ownership.mu.Lock()
	delete(st.ownership.items["WS"], "agent-1")
	st.ownership.mu.Unlock()
	if _, err := st.AgentCommands().Complete(ctx, "WS", command.CommandID, store.AgentCommandComplete{
		NodeID:                     "node-1",
		OwnerID:                    "owner-1",
		Status:                     domain.AgentCommandFailed,
		AgentCommandOwnershipProof: ack.AgentCommandOwnershipProof,
	}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Complete with missing ownership lease error = %v, want ErrNotOwner", err)
	}
}

func TestAgentOwnershipLeaseListUsesEffectiveExpiryStatus(t *testing.T) {
	st := New()
	ctx := t.Context()
	mustAcquireCommandOwnership(t, st, "WS", "agent-expired", "node-1", "owner-1")
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

func TestArtifactUploadContent(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{WorkspaceKey: "WS", ArtifactID: "artifact-1", Type: "patch"}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}

	body := []byte("diff --git a/file b/file\n")
	artifact, err := st.Artifacts().UploadContent(ctx, "WS", "artifact-1", store.ArtifactContentUpload{
		Body:     bytes.NewReader(body),
		MIMEType: "text/x-diff",
	})
	if err != nil {
		t.Fatalf("upload content: %v", err)
	}
	sum := sha256.Sum256(body)
	expectedHash := "sha256:" + hex.EncodeToString(sum[:])
	if artifact.DurableStatus != "uploading" || artifact.SizeBytes != int64(len(body)) || artifact.ContentHash != expectedHash || artifact.Checksum != expectedHash {
		t.Fatalf("artifact = %+v, want uploading with size/hash", artifact)
	}
	if artifact.MIMEType != "text/x-diff" || !strings.HasPrefix(artifact.URI, "mem://artifacts/WS/artifact-1/") {
		t.Fatalf("artifact mime/uri = %q/%q", artifact.MIMEType, artifact.URI)
	}

	finalized, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{})
	if err != nil {
		t.Fatalf("finalize artifact: %v", err)
	}
	if finalized.DurableStatus != "finalized" || finalized.FinalizedAt == nil || finalized.ContentHash != expectedHash {
		t.Fatalf("finalized artifact = %+v, want finalized with original hash", finalized)
	}
	refinalized, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{ContentHash: &expectedHash})
	if err != nil {
		t.Fatalf("re-finalize artifact: %v", err)
	}
	if refinalized.DurableStatus != "finalized" || refinalized.ContentHash != expectedHash {
		t.Fatalf("re-finalized artifact = %+v, want finalized with original hash", refinalized)
	}
	if _, err := st.Artifacts().UploadContent(ctx, "WS", "artifact-1", store.ArtifactContentUpload{Body: bytes.NewReader(body)}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("upload finalized error = %v, want ErrInvalidTransition", err)
	}
}

func TestArtifactFinalizeRejectsHashMismatch(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{WorkspaceKey: "WS", ArtifactID: "artifact-1", Type: "patch"}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	body := []byte("patch bytes")
	uploaded, err := st.Artifacts().UploadContent(ctx, "WS", "artifact-1", store.ArtifactContentUpload{Body: bytes.NewReader(body)})
	if err != nil {
		t.Fatalf("upload content: %v", err)
	}
	badHash := "sha256:bad"
	if _, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{ContentHash: &badHash}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("finalize mismatch error = %v, want ErrInvalidTransition", err)
	}
	artifact, err := st.Artifacts().Get(ctx, "WS", "artifact-1")
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.DurableStatus != "uploading" || artifact.ContentHash != uploaded.ContentHash || artifact.FinalizedAt != nil {
		t.Fatalf("artifact after rejected finalize = %+v, want original uploading artifact", artifact)
	}
}

func TestArtifactFinalizeRejectsMissingURI(t *testing.T) {
	st := New()
	ctx := t.Context()
	if _, err := st.Artifacts().Create(ctx, store.ArtifactCreate{WorkspaceKey: "WS", ArtifactID: "artifact-1", Type: "patch"}); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	if _, err := st.Artifacts().Finalize(ctx, "WS", "artifact-1", store.ArtifactFinalize{}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("finalize missing uri error = %v, want ErrInvalidTransition", err)
	}
}
