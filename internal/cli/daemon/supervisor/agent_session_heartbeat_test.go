package supervisor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// countingAgentSessionStore is a store.AgentSessionStore that counts Heartbeat
// calls and records the arguments of the last one. Only Heartbeat is
// overridden; every other method comes from the embedded interface and must not
// be called by the heartbeat loop.
type countingAgentSessionStore struct {
	store.AgentSessionStore

	heartbeats atomic.Int64

	mu     sync.Mutex
	lastWS string
	lastID string
	err    error // when non-nil, every Heartbeat fails
}

func (c *countingAgentSessionStore) Heartbeat(_ context.Context, ws, id string) (*domain.AgentSession, error) {
	c.mu.Lock()
	c.lastWS, c.lastID = ws, id
	err := c.err
	c.mu.Unlock()
	c.heartbeats.Add(1)
	if err != nil {
		return nil, err
	}
	return &domain.AgentSession{}, nil
}

func (c *countingAgentSessionStore) last() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastWS, c.lastID
}

func (c *countingAgentSessionStore) setErr(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

// sessionCountingStore embeds a real store.Store and overrides AgentSessions()
// with the counting fake, so the supervisor's ControlStore.AgentSessions()
// calls are observed.
type sessionCountingStore struct {
	store.Store
	sessions *countingAgentSessionStore
}

func (s *sessionCountingStore) AgentSessions() store.AgentSessionStore { return s.sessions }

func newAgentSessionHeartbeatSupervisor() (*Supervisor, *countingAgentSessionStore) {
	cs := &countingAgentSessionStore{}
	return &Supervisor{
		ControlStore: &sessionCountingStore{Store: memstore.New(), sessions: cs},
		WorkspaceID:  "WS",
		Shutdown:     make(chan struct{}),
	}, cs
}

func TestStartAgentSessionHeartbeatEvery_RenewsWhileAgentRuns(t *testing.T) {
	s, cs := newAgentSessionHeartbeatSupervisor()
	ap := &AgentProcess{AgentSessionID: "sess-1"}

	stop := s.startAgentSessionHeartbeatEvery(ap, 10*time.Millisecond)
	waitForCount(t, cs.heartbeats.Load, 2)
	stop()

	if ws, id := cs.last(); ws != "WS" || id != "sess-1" {
		t.Errorf("heartbeat args = (%q, %q), want (\"WS\", \"sess-1\")", ws, id)
	}

	got := cs.heartbeats.Load()
	time.Sleep(40 * time.Millisecond)
	if after := cs.heartbeats.Load(); after != got {
		t.Errorf("heartbeats continued after stop: %d -> %d", got, after)
	}
}

func TestStartAgentSessionHeartbeatEvery_StopBlocksUntilGoroutineExits(t *testing.T) {
	s, cs := newAgentSessionHeartbeatSupervisor()
	ap := &AgentProcess{AgentSessionID: "sess-1"}

	stop := s.startAgentSessionHeartbeatEvery(ap, time.Millisecond)
	waitForCount(t, cs.heartbeats.Load, 3)
	stop()

	// The stop func blocks on the goroutine's done channel, so the count
	// observed immediately after it returns is final. This is the property that
	// keeps a late beat off an already-terminal session row.
	got := cs.heartbeats.Load()
	time.Sleep(20 * time.Millisecond)
	if after := cs.heartbeats.Load(); after != got {
		t.Errorf("heartbeat landed after stop returned: %d -> %d", got, after)
	}
}

func TestStartAgentSessionHeartbeatEvery_StopsOnShutdown(t *testing.T) {
	s, cs := newAgentSessionHeartbeatSupervisor()
	ap := &AgentProcess{AgentSessionID: "sess-1"}

	_ = s.startAgentSessionHeartbeatEvery(ap, time.Millisecond)
	waitForCount(t, cs.heartbeats.Load, 2)
	close(s.Shutdown) // ends the loop without the stop func being called

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := cs.heartbeats.Load()
		time.Sleep(20 * time.Millisecond)
		if cs.heartbeats.Load() == got {
			return // frozen
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeats did not stop after shutdown")
		}
	}
}

func TestStartAgentSessionHeartbeatEvery_NoControlStoreIsNoOp(t *testing.T) {
	s := &Supervisor{Shutdown: make(chan struct{})} // no ControlStore / WorkspaceID
	ap := &AgentProcess{AgentSessionID: "sess-1"}
	// Must return a usable no-op stop and never panic.
	stop := s.startAgentSessionHeartbeatEvery(ap, time.Millisecond)
	stop()
}

func TestStartAgentSessionHeartbeatEvery_SkipsEmptySessionID(t *testing.T) {
	s, cs := newAgentSessionHeartbeatSupervisor()
	ap := &AgentProcess{} // no session id yet

	stop := s.startAgentSessionHeartbeatEvery(ap, time.Millisecond)
	defer stop()

	time.Sleep(30 * time.Millisecond)
	if got := cs.heartbeats.Load(); got != 0 {
		t.Errorf("heartbeats with empty session id = %d, want 0", got)
	}

	// The id is read per tick, so setting it mid-flight starts the beats.
	ap.Mu.Lock()
	ap.AgentSessionID = "sess-late"
	ap.Mu.Unlock()

	waitForCount(t, cs.heartbeats.Load, 2)
	if _, id := cs.last(); id != "sess-late" {
		t.Errorf("session id = %q, want \"sess-late\"", id)
	}
}

func TestStartAgentSessionHeartbeatEvery_SurvivesStoreError(t *testing.T) {
	s, cs := newAgentSessionHeartbeatSupervisor()
	cs.setErr(errors.New("fleet-db down"))
	ap := &AgentProcess{AgentSessionID: "sess-1"}

	stop := s.startAgentSessionHeartbeatEvery(ap, time.Millisecond)
	defer stop()

	// A transient outage must not permanently silence the heartbeat.
	waitForCount(t, cs.heartbeats.Load, 3)
}
