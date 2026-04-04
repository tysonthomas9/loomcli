package cli

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// SpyEmitter records all emitted events for test assertions.
type SpyEmitter struct {
	mu     sync.Mutex
	events []events.Event
}

func (s *SpyEmitter) Emit(e events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *SpyEmitter) Close() error { return nil }

func (s *SpyEmitter) Events() []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]events.Event, len(s.events))
	copy(cp, s.events)
	return cp
}

func (s *SpyEmitter) EventsByType(t events.EventType) []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []events.Event
	for _, e := range s.events {
		if e.Type == t {
			result = append(result, e)
		}
	}
	return result
}

func setupTestWorktree(t *testing.T, name string) string {
	t.Helper()
	tmpDir := t.TempDir()
	wtDir := filepath.Join(tmpDir, "worktrees", name)
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	return tmpDir
}

func TestDaemon_NewDaemon_StoresEventBus(t *testing.T) {
	spy := &SpyEmitter{}
	agents := []AgentEntry{{Worktree: "falcon", Role: "plan"}}
	tmpDir := setupTestWorktree(t, "falcon")
	config := makeDaemonConfig(agents, nil)

	daemon, err := NewDaemon(config, tmpDir, spy, nil)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	if daemon.eventBus != spy {
		t.Error("expected eventBus to be the spy emitter")
	}
}

func TestDaemon_NewDaemon_NilBusDefaultsToNop(t *testing.T) {
	agents := []AgentEntry{{Worktree: "falcon", Role: "plan"}}
	tmpDir := setupTestWorktree(t, "falcon")
	config := makeDaemonConfig(agents, nil)

	daemon, err := NewDaemon(config, tmpDir, nil, nil)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	if _, ok := daemon.eventBus.(events.NopBus); !ok {
		t.Errorf("expected NopBus, got %T", daemon.eventBus)
	}
}

func TestCheckAgentHealth_EmitsHealthCheck(t *testing.T) {
	spy := &SpyEmitter{}
	d := &Daemon{
		config:   &DaemonConfig{},
		eventBus: spy,
		agents: []*AgentProcess{
			{entry: AgentEntry{Worktree: "a"}, pid: 0},
			{entry: AgentEntry{Worktree: "b"}, pid: 0},
		},
	}

	d.checkAgentHealth()

	evts := spy.EventsByType(events.HealthCheck)
	if len(evts) != 1 {
		t.Fatalf("expected 1 health_check event, got %d", len(evts))
	}

	data, err := evts[0].DecodeData()
	if err != nil {
		t.Fatalf("DecodeData error: %v", err)
	}
	hc, ok := data.(*events.HealthCheckData)
	if !ok {
		t.Fatalf("expected *HealthCheckData, got %T", data)
	}
	if hc.AgentCount != 2 {
		t.Errorf("AgentCount = %d, want 2", hc.AgentCount)
	}
	if hc.HealthyCount != 0 {
		t.Errorf("HealthyCount = %d, want 0 (no PIDs running)", hc.HealthyCount)
	}
}

func TestHandleEpicTransition_EmitsEpicExhausted(t *testing.T) {
	mock := &mockDaemonIssueBackend{
		ReadyFn: func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
			return []backend.IssueData{}, nil
		},
	}

	spy := &SpyEmitter{}

	d := &Daemon{
		config:       &DaemonConfig{},
		eventBus:     spy,
		issueBackend: mock,
	}
	ap := &AgentProcess{
		entry:          AgentEntry{Worktree: "falcon", Role: "task", Parent: "epic-1"},
		worktreePath:   "/repo/worktrees/falcon",
		assignedEpicID: "epic-1",
	}

	d.handleEpicTransition(ap)

	evts := spy.EventsByType(events.EpicExhausted)
	if len(evts) != 1 {
		t.Fatalf("expected 1 epic_exhausted event, got %d", len(evts))
	}
	if evts[0].EpicID != "epic-1" {
		t.Errorf("epic_id = %q, want %q", evts[0].EpicID, "epic-1")
	}
}

func TestEmitEvent_NilBusSafe(t *testing.T) {
	d := &Daemon{} // eventBus is nil
	evt, _ := events.NewEvent(events.HealthCheck, "", "", "", events.HealthCheckData{AgentCount: 1, HealthyCount: 1})
	// Should not panic
	d.emitEvent(evt)
}
