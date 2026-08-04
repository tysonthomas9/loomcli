package store_test

// Lives in store_test package because it depends on memstore for a
// concrete Store implementation, and memstore imports store.

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestOrchestrationSessionFor_NoSession(t *testing.T) {
	st := memstore.New()
	got, err := store.OrchestrationSessionFor(context.Background(), st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil session when none exists, got %+v", got)
	}
}

func TestOrchestrationSessionFor_HappyPath(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()

	// Create the lead's orchestration session
	want, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-abc",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	})
	if err != nil {
		t.Fatalf("create orch: %v", err)
	}
	// Plus an unrelated task session for a different agent — should be ignored
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "task-worker-1",
		AgentID:      "worker-1",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := store.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.SessionID != want.SessionID {
		t.Fatalf("session id = %q, want %q", got.SessionID, want.SessionID)
	}
}

func TestOrchestrationSessionFor_PicksMostRecentlyUpdated(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()

	// Create older then newer in order. Update on memstore sets UpdatedAt
	// to now, so creating the newer one second is sufficient to give it
	// a later UpdatedAt than the older one.
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-old",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create old: %v", err)
	}
	// Slight pause so the second Create has a strictly later UpdatedAt.
	time.Sleep(2 * time.Millisecond)
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-new",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create new: %v", err)
	}

	got, err := store.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.SessionID != "lead-nova-new" {
		t.Fatalf("got = %+v, want session id lead-nova-new (most recent)", got)
	}
}

func TestOrchestrationSessionFor_IgnoresCompletedSessions(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()

	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-running",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create running: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	completed, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-completed",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionCompleted,
	})
	if err != nil {
		t.Fatalf("create completed: %v", err)
	}
	finishedAt := time.Now().UTC()
	finishedAtPtr := &finishedAt
	if _, err := st.AgentSessions().Update(ctx, "WS", completed.SessionID, store.AgentSessionUpdate{FinishedAt: &finishedAtPtr}); err != nil {
		t.Fatalf("finish completed: %v", err)
	}

	got, err := store.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.SessionID != "lead-nova-running" {
		t.Fatalf("got = %+v, want running session", got)
	}
}

func TestOrchestrationSessionIDFor_EmptyAgentID(t *testing.T) {
	st := memstore.New()
	id, err := store.OrchestrationSessionIDFor(context.Background(), st, "WS", "")
	if err != nil || id != "" {
		t.Fatalf("expected empty id and nil err for blank agent; got id=%q err=%v", id, err)
	}
}
