package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

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
	if daemon.sup.EventBus != spy {
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
	if _, ok := daemon.sup.EventBus.(events.NopBus); !ok {
		t.Errorf("expected NopBus, got %T", daemon.sup.EventBus)
	}
}
