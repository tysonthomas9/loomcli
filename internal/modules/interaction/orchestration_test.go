package interaction_test

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

// sessionFixture keeps this owner test at the owner boundary. Adapter
// conformance belongs to the memory and FleetDB adapter packages.
type sessionFixture struct {
	sessions []*interaction.SessionRecord
}

func newSessionFixture() *sessionFixture { return &sessionFixture{} }

func (f *sessionFixture) AgentSessions() interaction.AgentSessionStore { return f }

func (f *sessionFixture) Create(_ context.Context, in interaction.AgentSessionCreate) (*interaction.SessionRecord, error) {
	now := time.Now().UTC()
	record := &interaction.SessionRecord{
		WorkspaceKey: in.WorkspaceKey, SessionID: in.SessionID, AgentID: in.AgentID,
		NodeID: in.NodeID, Kind: in.Kind, TaskID: in.TaskID, TerminalID: in.TerminalID,
		ParentSessionID: in.ParentSessionID, Status: in.Status, Phase: in.Phase,
		Attempt: in.Attempt, Metadata: cloneSessionMetadata(in.Metadata),
		StartedAt: now, LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	f.sessions = append(f.sessions, record)
	return cloneSessionRecord(record), nil
}

func (f *sessionFixture) Get(_ context.Context, workspaceKey, sessionID string) (*interaction.SessionRecord, error) {
	for _, record := range f.sessions {
		if record.WorkspaceKey == workspaceKey && record.SessionID == sessionID {
			return cloneSessionRecord(record), nil
		}
	}
	return nil, nil
}

func (f *sessionFixture) List(_ context.Context, workspaceKey string, filter interaction.AgentSessionFilter) ([]*interaction.SessionRecord, error) {
	out := make([]*interaction.SessionRecord, 0, len(f.sessions))
	for _, record := range f.sessions {
		if record.WorkspaceKey != workspaceKey || filter.AgentID != "" && record.AgentID != filter.AgentID ||
			filter.Kind != "" && record.Kind != filter.Kind || filter.Status != "" && record.Status != filter.Status ||
			filter.ParentSessionID != "" && record.ParentSessionID != filter.ParentSessionID {
			continue
		}
		out = append(out, cloneSessionRecord(record))
		if filter.Limit > 0 && len(out) == filter.Limit {
			break
		}
	}
	return out, nil
}

func (f *sessionFixture) Heartbeat(_ context.Context, workspaceKey, sessionID string) (*interaction.SessionRecord, error) {
	for _, record := range f.sessions {
		if record.WorkspaceKey == workspaceKey && record.SessionID == sessionID {
			now := time.Now().UTC()
			record.LastHeartbeat = now
			record.UpdatedAt = now
			return cloneSessionRecord(record), nil
		}
	}
	return nil, nil
}

func (f *sessionFixture) Update(_ context.Context, workspaceKey, sessionID string, patch interaction.AgentSessionUpdate) (*interaction.SessionRecord, error) {
	for _, record := range f.sessions {
		if record.WorkspaceKey != workspaceKey || record.SessionID != sessionID {
			continue
		}
		if patch.NodeID != nil {
			record.NodeID = *patch.NodeID
		}
		if patch.TaskID != nil {
			record.TaskID = *patch.TaskID
		}
		if patch.Status != nil {
			record.Status = *patch.Status
		}
		if patch.Phase != nil {
			record.Phase = *patch.Phase
		}
		if patch.LastHeartbeat != nil {
			record.LastHeartbeat = *patch.LastHeartbeat
		}
		if patch.FinishedAt != nil {
			record.FinishedAt = *patch.FinishedAt
		}
		if patch.Summary != nil {
			record.Summary = *patch.Summary
		}
		if patch.ErrorClass != nil {
			record.ErrorClass = *patch.ErrorClass
		}
		if patch.ExitCode != nil {
			record.ExitCode = *patch.ExitCode
		}
		if patch.Metadata != nil {
			record.Metadata = cloneSessionMetadata(*patch.Metadata)
		}
		record.UpdatedAt = time.Now().UTC()
		return cloneSessionRecord(record), nil
	}
	return nil, nil
}

func cloneSessionRecord(record *interaction.SessionRecord) *interaction.SessionRecord {
	if record == nil {
		return nil
	}
	copy := *record
	copy.Metadata = cloneSessionMetadata(record.Metadata)
	return &copy
}

func cloneSessionMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	copy := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	return copy
}

func TestOrchestrationSessionFor_NoSession(t *testing.T) {
	st := newSessionFixture()
	got, err := interaction.OrchestrationSessionFor(context.Background(), st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil session when none exists, got %+v", got)
	}
}

func TestOrchestrationSessionFor_IgnoresNonInteractiveKind(t *testing.T) {
	st := newSessionFixture()
	ctx := context.Background()

	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-abc",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordTask,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create orch: %v", err)
	}
	// Plus an unrelated task session for a different agent — should be ignored
	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "task-worker-1",
		AgentID:      "worker-1",
		Kind:         interaction.SessionRecordTask,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := interaction.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %+v, want non-interactive session ignored", got)
	}
}

func TestOrchestrationSessionFor_IgnoresNewerNonInteractiveKind(t *testing.T) {
	st := newSessionFixture()
	ctx := context.Background()

	canonical, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-interactive",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordRunning,
	})
	if err != nil {
		t.Fatalf("create canonical interactive session: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-task-newer",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordTask,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create newer task session: %v", err)
	}

	got, err := interaction.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("OrchestrationSessionFor: %v", err)
	}
	if got == nil || got.SessionID != canonical.SessionID {
		t.Fatalf("got = %+v, want canonical interactive session %q", got, canonical.SessionID)
	}
}

func TestOrchestrationSessionFor_ReturnsNilWhenInteractiveSessionIsInactive(t *testing.T) {
	st := newSessionFixture()
	ctx := context.Background()

	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-interactive-completed",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordCompleted,
	}); err != nil {
		t.Fatalf("create inactive canonical session: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-task-running",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordTask,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create active task session: %v", err)
	}

	got, err := interaction.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("OrchestrationSessionFor: %v", err)
	}
	if got != nil {
		t.Fatalf("got = %+v, want nil without an active interactive session", got)
	}
}

func TestOrchestrationSessionFor_PicksMostRecentlyUpdated(t *testing.T) {
	st := newSessionFixture()
	ctx := context.Background()

	// Create older then newer in order. Update on memstore sets UpdatedAt
	// to now, so creating the newer one second is sufficient to give it
	// a later UpdatedAt than the older one.
	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-old",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create old: %v", err)
	}
	// Slight pause so the second Create has a strictly later UpdatedAt.
	time.Sleep(2 * time.Millisecond)
	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-new",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create new: %v", err)
	}

	got, err := interaction.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.SessionID != "lead-nova-new" {
		t.Fatalf("got = %+v, want session id lead-nova-new (most recent)", got)
	}
}

func TestOrchestrationSessionFor_IgnoresCompletedSessions(t *testing.T) {
	st := newSessionFixture()
	ctx := context.Background()

	if _, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-running",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordRunning,
	}); err != nil {
		t.Fatalf("create running: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	completed, err := st.AgentSessions().Create(ctx, interaction.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-nova-completed",
		AgentID:      "nova",
		Kind:         interaction.SessionRecordInteractive,
		Status:       interaction.SessionRecordCompleted,
	})
	if err != nil {
		t.Fatalf("create completed: %v", err)
	}
	finishedAt := time.Now().UTC()
	finishedAtPtr := &finishedAt
	if _, err := st.AgentSessions().Update(ctx, "WS", completed.SessionID, interaction.AgentSessionUpdate{FinishedAt: &finishedAtPtr}); err != nil {
		t.Fatalf("finish completed: %v", err)
	}

	got, err := interaction.OrchestrationSessionFor(ctx, st, "WS", "nova")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || got.SessionID != "lead-nova-running" {
		t.Fatalf("got = %+v, want running session", got)
	}
}

func TestOrchestrationSessionIDFor_EmptyAgentID(t *testing.T) {
	st := newSessionFixture()
	id, err := interaction.OrchestrationSessionIDFor(context.Background(), st, "WS", "")
	if err != nil || id != "" {
		t.Fatalf("expected empty id and nil err for blank agent; got id=%q err=%v", id, err)
	}
}
