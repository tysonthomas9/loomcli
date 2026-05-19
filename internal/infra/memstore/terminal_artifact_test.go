package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTerminalSessionStoreCRUDAndFilters(t *testing.T) {
	st := New()
	ctx := t.Context()
	terms := st.TerminalSessions()

	if _, err := terms.Create(ctx, store.TerminalSessionCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}

	first, err := terms.Create(ctx, store.TerminalSessionCreate{
		WorkspaceKey:    "WS",
		TerminalID:      "term-1",
		AgentID:         "agent-1",
		SessionID:       "session-1",
		NodeID:          "node-1",
		TaskID:          "task-1",
		Title:           "Lead terminal",
		Kind:            "lead",
		Status:          domain.TerminalSessionOpen,
		PTYProvider:     "tmux",
		StreamRef:       "stream://1",
		TranscriptRef:   "artifact://transcript",
		AttachedClients: 2,
		Metadata:        map[string]string{"role": "lead"},
	})
	if err != nil {
		t.Fatalf("create term-1: %v", err)
	}
	if first.CreatedAt.IsZero() || first.StartedAt.IsZero() || first.Metadata["role"] != "lead" {
		t.Fatalf("created term missing timestamps or metadata: %+v", first)
	}

	if _, err := terms.Create(ctx, store.TerminalSessionCreate{
		WorkspaceKey: "WS", TerminalID: "term-2", AgentID: "agent-2",
		SessionID: "session-2", NodeID: "node-2", TaskID: "task-2",
		Status: domain.TerminalSessionLost, Metadata: map[string]string{"role": "worker"},
	}); err != nil {
		t.Fatalf("create term-2: %v", err)
	}

	got, err := terms.Get(ctx, "WS", "term-1")
	if err != nil {
		t.Fatalf("get term-1: %v", err)
	}
	got.Metadata["role"] = "mutated"
	again, _ := terms.Get(ctx, "WS", "term-1")
	if again.Metadata["role"] != "lead" {
		t.Fatalf("get returned mutable metadata alias: %+v", again.Metadata)
	}

	filtered, err := terms.List(ctx, "WS", store.TerminalSessionFilter{
		AgentID: "agent-1", SessionID: "session-1", NodeID: "node-1",
		TaskID: "task-1", Status: domain.TerminalSessionOpen,
	})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 1 || filtered[0].TerminalID != "term-1" {
		t.Fatalf("filtered list = %+v", filtered)
	}

	limited, err := terms.List(ctx, "WS", store.TerminalSessionFilter{Limit: 1})
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited list length = %d", len(limited))
	}

	lastSeen := time.Now().UTC().Add(time.Minute)
	ended := lastSeen.Add(time.Minute)
	endedPtr := &ended
	closed := domain.TerminalSessionClosed
	clients := 0
	meta := map[string]string{"role": "done"}
	updated, err := terms.Update(ctx, "WS", "term-1", store.TerminalSessionUpdate{
		Status:          &closed,
		LastSeenAt:      &lastSeen,
		EndedAt:         &endedPtr,
		AttachedClients: &clients,
		Metadata:        &meta,
	})
	if err != nil {
		t.Fatalf("update term-1: %v", err)
	}
	meta["role"] = "mutated"
	if updated.Status != domain.TerminalSessionClosed || updated.AttachedClients != 0 || updated.EndedAt == nil || updated.Metadata["role"] != "done" {
		t.Fatalf("updated term = %+v", updated)
	}
	if _, err := terms.Update(ctx, "WS", "missing", store.TerminalSessionUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
}

func TestArtifactStoreCRUDAndFilters(t *testing.T) {
	st := New()
	ctx := t.Context()
	artifacts := st.Artifacts()

	if _, err := artifacts.Create(ctx, store.ArtifactCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	first, err := artifacts.Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "artifact-1", AgentID: "agent-1",
		SessionID: "session-1", TerminalID: "term-1", TaskID: "task-1",
		Type: "transcript", URI: "file:///tmp/transcript.jsonl", Summary: "summary",
		MIMEType: "application/jsonl", SizeBytes: 42, Checksum: "sha256:abc",
		Metadata: map[string]string{"source": "test"},
	})
	if err != nil {
		t.Fatalf("create artifact-1: %v", err)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() || first.Metadata["source"] != "test" {
		t.Fatalf("created artifact = %+v", first)
	}
	if _, err := artifacts.Create(ctx, store.ArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "artifact-2", AgentID: "agent-2",
		SessionID: "session-2", TerminalID: "term-2", TaskID: "task-2",
		Type: "log", URI: "file:///tmp/log.txt",
	}); err != nil {
		t.Fatalf("create artifact-2: %v", err)
	}

	got, err := artifacts.Get(ctx, "WS", "artifact-1")
	if err != nil {
		t.Fatalf("get artifact-1: %v", err)
	}
	got.Metadata["source"] = "mutated"
	again, _ := artifacts.Get(ctx, "WS", "artifact-1")
	if again.Metadata["source"] != "test" {
		t.Fatalf("get returned mutable metadata alias: %+v", again.Metadata)
	}

	filtered, err := artifacts.List(ctx, "WS", store.ArtifactFilter{
		AgentID: "agent-1", SessionID: "session-1", TerminalID: "term-1",
		TaskID: "task-1", Type: "transcript",
	})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ArtifactID != "artifact-1" {
		t.Fatalf("filtered list = %+v", filtered)
	}
	limited, err := artifacts.List(ctx, "WS", store.ArtifactFilter{Limit: 1})
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited list length = %d", len(limited))
	}

	uri := "file:///tmp/new.jsonl"
	summary := "updated summary"
	meta := map[string]string{"source": "updated"}
	updated, err := artifacts.Update(ctx, "WS", "artifact-1", store.ArtifactUpdate{
		URI: &uri, Summary: &summary, Metadata: &meta,
	})
	if err != nil {
		t.Fatalf("update artifact-1: %v", err)
	}
	meta["source"] = "mutated"
	if updated.URI != uri || updated.Summary != summary || updated.Metadata["source"] != "updated" {
		t.Fatalf("updated artifact = %+v", updated)
	}
	if _, err := artifacts.Update(ctx, "WS", "missing", store.ArtifactUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing: want ErrNotFound, got %v", err)
	}
	if _, err := artifacts.Get(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing: want ErrNotFound, got %v", err)
	}
}

func TestAgentLeaseStoreLifecycle(t *testing.T) {
	st := New()
	ctx := t.Context()
	leases := st.AgentLeases()

	if _, err := leases.Create(ctx, store.AgentLeaseCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	lease, err := leases.Create(ctx, store.AgentLeaseCreate{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1", NodeID: "node-1",
	})
	if err != nil {
		t.Fatalf("create lease: %v", err)
	}
	if lease.LeaseID == "" || lease.Token == "" || lease.FencingToken == 0 || lease.Status != domain.AgentLeaseActive {
		t.Fatalf("created lease = %+v", lease)
	}
	exp := lease.ExpiresAt

	got, err := leases.Get(ctx, "WS", lease.LeaseID)
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if got.SessionID != "session-1" {
		t.Fatalf("got lease = %+v", got)
	}
	filtered, err := leases.List(ctx, "WS", store.AgentLeaseFilter{
		SessionID: "session-1", AgentID: "agent-1", NodeID: "node-1", Status: domain.AgentLeaseActive,
	})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 1 || filtered[0].LeaseID != lease.LeaseID {
		t.Fatalf("filtered list = %+v", filtered)
	}
	limited, err := leases.List(ctx, "WS", store.AgentLeaseFilter{Limit: 1})
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited list length = %d", len(limited))
	}

	beat, err := leases.Heartbeat(ctx, "WS", lease.LeaseID, lease.Token, 10*time.Minute)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !beat.ExpiresAt.After(exp) {
		t.Fatalf("heartbeat did not extend expiry: before=%s after=%s", exp, beat.ExpiresAt)
	}
	if _, err := leases.Heartbeat(ctx, "WS", lease.LeaseID, "bad-token", time.Minute); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("heartbeat bad token: want ErrConflict, got %v", err)
	}

	released, err := leases.Release(ctx, "WS", lease.LeaseID, lease.Token)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released.Status != domain.AgentLeaseReleased {
		t.Fatalf("released lease = %+v", released)
	}
	if _, err := leases.Release(ctx, "WS", lease.LeaseID, "bad-token"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("release bad token: want ErrConflict, got %v", err)
	}
	if _, err := leases.Get(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing: want ErrNotFound, got %v", err)
	}
}

func TestAgentOwnershipLeaseStoreLifecycle(t *testing.T) {
	st := New()
	ctx := t.Context()
	leases := st.AgentOwnershipLeases()

	if _, err := leases.Acquire(ctx, store.AgentOwnershipLeaseAcquire{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty acquire: want ErrInvalid, got %v", err)
	}
	lease, err := leases.Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS", AgentID: "agent-1", OwnerID: "owner-1", NodeID: "node-1",
	})
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if lease.LeaseID == "" || lease.Token == "" || lease.RuntimeProvider != domain.RuntimeProviderLocal || lease.Status != domain.AgentLeaseActive {
		t.Fatalf("acquired lease = %+v", lease)
	}
	if _, err := leases.Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS", AgentID: "agent-1", OwnerID: "owner-2",
	}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate active acquire: want ErrAlreadyExists, got %v", err)
	}

	got, err := leases.Get(ctx, "WS", "agent-1")
	if err != nil {
		t.Fatalf("get lease: %v", err)
	}
	if got.OwnerID != "owner-1" {
		t.Fatalf("got lease = %+v", got)
	}
	filtered, err := leases.List(ctx, "WS", store.AgentOwnershipLeaseFilter{
		OwnerID: "owner-1", NodeID: "node-1", RuntimeProvider: domain.RuntimeProviderLocal,
		Status: domain.AgentLeaseActive,
	})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 1 || filtered[0].AgentID != "agent-1" {
		t.Fatalf("filtered list = %+v", filtered)
	}
	limited, err := leases.List(ctx, "WS", store.AgentOwnershipLeaseFilter{Limit: 1})
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited list length = %d", len(limited))
	}

	beat, err := leases.Heartbeat(ctx, "WS", "agent-1", lease.Token, time.Minute)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if !beat.LastHeartbeat.After(lease.LastHeartbeat) {
		t.Fatalf("heartbeat did not update timestamp: before=%s after=%s", lease.LastHeartbeat, beat.LastHeartbeat)
	}
	if _, err := leases.Heartbeat(ctx, "WS", "agent-1", "bad-token", time.Minute); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("heartbeat bad token: want ErrConflict, got %v", err)
	}

	released, err := leases.Release(ctx, "WS", "agent-1", lease.Token)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released.Status != domain.AgentLeaseReleased {
		t.Fatalf("released lease = %+v", released)
	}
	if _, err := leases.Release(ctx, "WS", "agent-1", "bad-token"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("release bad token: want ErrConflict, got %v", err)
	}
	if _, err := leases.Get(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing: want ErrNotFound, got %v", err)
	}
}

func TestAgentCommandStoreLifecycle(t *testing.T) {
	st := New()
	ctx := t.Context()
	commands := st.AgentCommands()

	if _, err := commands.Create(ctx, store.AgentCommandCreate{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty create: want ErrInvalid, got %v", err)
	}
	cmd, err := commands.Create(ctx, store.AgentCommandCreate{
		WorkspaceKey: "WS", TargetAgentID: "agent-1", TargetNodeID: "node-1",
		SessionID: "session-1", Type: "interrupt", Payload: map[string]string{"reason": "test"},
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	if cmd.CommandID == "" || cmd.Cursor == 0 || cmd.Status != domain.AgentCommandQueued || cmd.Payload["reason"] != "test" {
		t.Fatalf("created command = %+v", cmd)
	}
	cmd.Payload["reason"] = "mutated"
	got, err := commands.Get(ctx, "WS", cmd.CommandID)
	if err != nil {
		t.Fatalf("get command: %v", err)
	}
	if got.Payload["reason"] != "test" {
		t.Fatalf("get returned mutable payload alias: %+v", got.Payload)
	}

	filtered, err := commands.List(ctx, "WS", store.AgentCommandFilter{
		TargetAgentID: "agent-1", TargetNodeID: "node-1", Status: domain.AgentCommandQueued,
	})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != 1 || filtered[0].CommandID != cmd.CommandID {
		t.Fatalf("filtered list = %+v", filtered)
	}
	afterCursor, err := commands.List(ctx, "WS", store.AgentCommandFilter{AfterCursor: cmd.Cursor})
	if err != nil {
		t.Fatalf("after-cursor list: %v", err)
	}
	if len(afterCursor) != 0 {
		t.Fatalf("after-cursor list = %+v", afterCursor)
	}
	limited, err := commands.List(ctx, "WS", store.AgentCommandFilter{Limit: 1})
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited list length = %d", len(limited))
	}

	acked, err := commands.Ack(ctx, "WS", cmd.CommandID)
	if err != nil {
		t.Fatalf("ack command: %v", err)
	}
	if acked.Status != domain.AgentCommandAcked || acked.AckedAt == nil {
		t.Fatalf("acked command = %+v", acked)
	}
	completed, err := commands.Complete(ctx, "WS", cmd.CommandID, store.AgentCommandComplete{
		Result: "ok",
	})
	if err != nil {
		t.Fatalf("complete command: %v", err)
	}
	if completed.Status != domain.AgentCommandSucceeded || completed.Result != "ok" {
		t.Fatalf("completed command = %+v", completed)
	}
	failed, err := commands.Complete(ctx, "WS", cmd.CommandID, store.AgentCommandComplete{
		Status: domain.AgentCommandFailed, ErrorClass: "tool_error",
	})
	if err != nil {
		t.Fatalf("complete failed command: %v", err)
	}
	if failed.Status != domain.AgentCommandFailed || failed.ErrorClass != "tool_error" {
		t.Fatalf("failed command = %+v", failed)
	}
	if _, err := commands.Ack(ctx, "WS", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ack missing: want ErrNotFound, got %v", err)
	}
	if _, err := commands.Complete(ctx, "WS", "missing", store.AgentCommandComplete{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("complete missing: want ErrNotFound, got %v", err)
	}
}
