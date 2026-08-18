package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/daemonregistry"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
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

func setupTestWorktree(t *testing.T, name string) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	wtDir := filepath.Join(tmpDir, "worktrees", name)
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	return tmpDir, wtDir
}

func TestDaemon_NewDaemon_StoresEventBus(t *testing.T) {
	spy := &SpyEmitter{}
	tmpDir, wtDir := setupTestWorktree(t, "falcon")
	agents := []AgentEntry{{Worktree: wtDir, Role: "plan"}}
	config := makeDaemonConfig(agents, nil)

	daemon, err := NewDaemon(config, tmpDir, spy, nil, nil)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	if daemon.sup.EventBus != spy {
		t.Error("expected eventBus to be the spy emitter")
	}
}

func TestDaemon_NewDaemon_NilBusDefaultsToNop(t *testing.T) {
	tmpDir, wtDir := setupTestWorktree(t, "falcon")
	agents := []AgentEntry{{Worktree: wtDir, Role: "plan"}}
	config := makeDaemonConfig(agents, nil)

	daemon, err := NewDaemon(config, tmpDir, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	if _, ok := daemon.sup.EventBus.(events.NopBus); !ok {
		t.Errorf("expected NopBus, got %T", daemon.sup.EventBus)
	}
}

func TestDaemonStartEmitsStructuredDaemonStartedEvent(t *testing.T) {
	spy := &SpyEmitter{}
	config := makeDaemonConfig(nil, nil)
	daemon, err := NewDaemon(config, t.TempDir(), spy, nil, nil)
	if err != nil {
		t.Fatalf("NewDaemon: %v", err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	daemon.Stop()
	started := spy.EventsByType(events.DaemonStarted)
	if len(started) != 1 {
		t.Fatalf("daemon.started events = %+v", started)
	}
	decoded, err := started[0].DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	data := decoded.(*events.DaemonStartedData)
	if data.PID <= 0 {
		t.Fatalf("daemon started data = %+v", data)
	}
}

func TestDaemon_NewDaemon_DoesNotPublishAgentIPCSocketPathBeforeBind(t *testing.T) {
	tmpDir, wtDir := setupTestWorktree(t, "falcon")
	agents := []AgentEntry{{Worktree: wtDir, Role: "plan"}}
	config := makeDaemonConfig(agents, nil)

	daemon, err := NewDaemon(config, tmpDir, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}

	if daemon.sup.IpcSocketPath != "" {
		t.Fatalf("IpcSocketPath = %q, want empty before agent IPC bind succeeds", daemon.sup.IpcSocketPath)
	}
}

func TestDaemon_StartDoesNotPublishSocketLabelBeforeAgentIPCBind(t *testing.T) {
	st := memstore.New()
	tmpDir, wtDir := setupTestWorktree(t, "falcon")
	agents := []AgentEntry{{Worktree: wtDir, Role: "plan"}}
	config := makeDaemonConfig(agents, nil)
	config.Backend = "fleet"

	daemon, err := NewDaemon(config, tmpDir, nil, nil, st)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}
	daemon.sup.WorkspaceID = "WS"
	daemon.sup.NodeID = "node-1"

	if err := daemon.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer daemon.Stop()

	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	for _, label := range node.Labels {
		if strings.HasPrefix(label, daemonregistry.LabelSocket) {
			t.Fatalf("unexpected socket label before agent IPC bind succeeds: labels=%v", node.Labels)
		}
	}
}
