package daemon

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
)

func TestNewDaemon_NilConfig(t *testing.T) {
	_, err := NewDaemon(nil, "/tmp", nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil config, got nil")
	}
}

func TestNewDaemon_EmptyAgents(t *testing.T) {
	cfg := &DaemonConfig{
		Agents: []AgentEntry{},
	}
	_, err := NewDaemon(cfg, "/tmp", nil, nil, nil)
	if err == nil {
		t.Error("expected error for empty agents, got nil")
	}
}

func TestDaemon_StopAgent_NilProcess(t *testing.T) {
	cfg := &DaemonConfig{}
	d := &Daemon{
		config: cfg,
	}
	sup := &supervisor.Supervisor{
		ConfigSnapshot: d.configSnapshot,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	d.sup = sup

	ap := &AgentProcess{
		Entry: AgentEntry{Worktree: "test"},
		// Cmd is nil, Pid is 0
	}

	// Should be a no-op without panicking
	d.sup.StopAgent(ap, 5*time.Second)
}
