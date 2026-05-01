package supervisor

import (
	"testing"
	"time"

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
