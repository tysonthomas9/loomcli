package supervisor

import (
	"os"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestSupervisorRegistersControlPlaneNodeOnStart(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if node.RuntimeProvider != domain.RuntimeProviderLocal {
		t.Fatalf("RuntimeProvider = %q, want local", node.RuntimeProvider)
	}
	if node.DrainState != domain.NodeDrainActive {
		t.Fatalf("DrainState = %q, want active", node.DrainState)
	}
	if node.Capacity != 0 {
		t.Fatalf("Capacity = %d, want 0", node.Capacity)
	}
}

func TestSupervisorHeartbeatsControlPlaneNodeUntilStop(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	s.NodeInterval = 10 * time.Millisecond

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	node, err := st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get initial node: %v", err)
	}
	initialHeartbeat := node.LastHeartbeat

	deadline := time.Now().Add(2 * time.Second)
	for {
		node, err = st.Nodes().Get(t.Context(), "WS", "node-1")
		if err != nil {
			t.Fatalf("get heartbeat node: %v", err)
		}
		if node.LastHeartbeat.After(initialHeartbeat) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node heartbeat did not advance after %s", time.Since(initialHeartbeat))
		}
		time.Sleep(5 * time.Millisecond)
	}

	s.Stop()
	stoppedHeartbeat := node.LastHeartbeat
	time.Sleep(30 * time.Millisecond)
	node, err = st.Nodes().Get(t.Context(), "WS", "node-1")
	if err != nil {
		t.Fatalf("get stopped node: %v", err)
	}
	if !node.LastHeartbeat.Equal(stoppedHeartbeat) {
		t.Fatalf("heartbeat advanced after Stop: %s -> %s", stoppedHeartbeat, node.LastHeartbeat)
	}
}

func TestSupervisorMirrorsAgentSessionToControlPlane(t *testing.T) {
	st := memstore.New()
	s := newControlPlaneTestSupervisor(st)
	worktree := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
		cli.ResetBeadsDirCache()
	})
	cli.ResetBeadsDirCache()

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task", Repo: "repo-a"},
		WorktreePath: worktree,
	}

	s.createAgentSession(ap, "epic-1")
	if ap.AgentSessionID == "" {
		t.Fatal("AgentSessionID was not set")
	}
	session, err := st.AgentSessions().Get(t.Context(), "WS", ap.AgentSessionID)
	if err != nil {
		t.Fatalf("get created agent session: %v", err)
	}
	if session.AgentID != "worker-1" || session.NodeID != "node-1" {
		t.Fatalf("session agent/node = %q/%q, want worker-1/node-1", session.AgentID, session.NodeID)
	}
	if session.Status != domain.AgentSessionStarting {
		t.Fatalf("status = %q, want starting", session.Status)
	}
	if session.Metadata["epic_id"] != "epic-1" || session.Metadata["repo"] != "repo-a" {
		t.Fatalf("metadata = %#v, want epic/repo", session.Metadata)
	}

	s.markControlPlaneAgentSessionRunning(ap)
	session, err = st.AgentSessions().Get(t.Context(), "WS", ap.AgentSessionID)
	if err != nil {
		t.Fatalf("get running agent session: %v", err)
	}
	if session.Status != domain.AgentSessionRunning {
		t.Fatalf("status = %q, want running", session.Status)
	}

	sessionID := ap.AgentSessionID
	s.completeControlPlaneAgentSession(ap, sessionID, 7, "Fatal")
	session, err = st.AgentSessions().Get(t.Context(), "WS", sessionID)
	if err != nil {
		t.Fatalf("get completed agent session: %v", err)
	}
	if session.Status != domain.AgentSessionFailed {
		t.Fatalf("status = %q, want failed", session.Status)
	}
	if session.ExitCode == nil || *session.ExitCode != 7 {
		t.Fatalf("exit code = %v, want 7", session.ExitCode)
	}
	if session.ErrorClass != "Fatal" {
		t.Fatalf("error class = %q, want Fatal", session.ErrorClass)
	}
	if session.FinishedAt == nil {
		t.Fatal("FinishedAt was not set")
	}
}

func newControlPlaneTestSupervisor(st *memstore.Store) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{}
		},
		WorkspaceID:   "WS",
		ControlStore:  st,
		NodeID:        "node-1",
		NodeTTL:       time.Minute,
		NodeInterval:  time.Hour,
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		Concurrency:   NewConcurrencyTracker(nil),
		EmitEvent:     func(events.Event) {},
	}
}
