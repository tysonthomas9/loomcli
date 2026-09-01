package supervisor

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// recordingOwnershipLeaseStore records every Release call so a test can assert
// exactly which agents were released, and how many times.
type recordingOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore

	mu       sync.Mutex
	released []string
}

func (s *recordingOwnershipLeaseStore) Release(_ context.Context, _, agentID, _ string) (*domain.AgentOwnershipLease, error) {
	s.mu.Lock()
	s.released = append(s.released, agentID)
	s.mu.Unlock()
	return nil, nil
}

func (s *recordingOwnershipLeaseStore) releases() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.released...)
	sort.Strings(out)
	return out
}

// shutdownSweepSupervisor builds a supervisor whose ownership releases are
// recorded, holding one agent per entry in tokens (an empty token means the
// lease was already released by the normal unwind).
func shutdownSweepSupervisor(t *testing.T, tokens map[string]string) (*Supervisor, *recordingOwnershipLeaseStore) {
	t.Helper()
	base := memstore.New()
	rec := &recordingOwnershipLeaseStore{AgentOwnershipLeaseStore: base.AgentOwnershipLeases()}
	s := newControlPlaneTestSupervisor(base)
	s.ControlStore = &controlPlaneStoreOverrides{Store: base, ownership: rec}

	names := make([]string, 0, len(tokens))
	for name := range tokens {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ap := &AgentProcess{
			Entry:  cfgpkg.AgentEntry{Worktree: name, Role: "task"},
			StopCh: make(chan struct{}),
			Done:   make(chan struct{}),
		}
		ap.OwnershipLeaseID = name + "-lease"
		ap.OwnershipLeaseToken = tokens[name]
		s.Agents = append(s.Agents, ap)
	}
	return s, rec
}

func TestStopReleasesOwnershipLeasesStillHeld(t *testing.T) {
	s, rec := shutdownSweepSupervisor(t, map[string]string{
		"worker-1": "token-1",
		"worker-2": "token-2",
	})

	s.Stop()

	if got, want := rec.releases(), []string{"worker-1", "worker-2"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("released = %v, want %v", got, want)
	}
	for _, ap := range s.Agents {
		ap.Mu.Lock()
		token := ap.OwnershipLeaseToken
		ap.Mu.Unlock()
		if token != "" {
			t.Fatalf("agent %q still holds ownership token %q after Stop", ap.Entry.Worktree, token)
		}
	}
}

func TestStopSkipsAgentsWhoseLeaseWasAlreadyReleased(t *testing.T) {
	s, rec := shutdownSweepSupervisor(t, map[string]string{
		"worker-1": "",        // normal unwind already released it
		"worker-2": "token-2", // still held
	})

	s.Stop()

	if got, want := rec.releases(), []string{"worker-2"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("released = %v, want %v (no second release for an already-released lease)", got, want)
	}
}

func TestStopSweepsOwnershipLeasesWhenSuperviseGoroutineNeverExits(t *testing.T) {
	s, rec := shutdownSweepSupervisor(t, map[string]string{"worker-1": "token-1"})

	oldWait := shutdownAgentWaitTimeout
	shutdownAgentWaitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { shutdownAgentWaitTimeout = oldWait })

	// A goroutine that outlives the shutdown: Wg.Wait() would block forever.
	wedged := make(chan struct{})
	s.Wg.Add(1)
	go func() {
		defer s.Wg.Done()
		<-wedged
	}()
	t.Cleanup(func() { close(wedged) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Stop()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return; the wait for supervise goroutines is not bounded")
	}

	if got, want := rec.releases(), []string{"worker-1"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("released = %v, want %v after the bounded wait expired", got, want)
	}
}

func TestReleaseAllOwnershipLeasesIsIdempotent(t *testing.T) {
	s, rec := shutdownSweepSupervisor(t, map[string]string{"worker-1": "token-1"})

	s.releaseAllOwnershipLeases()
	s.releaseAllOwnershipLeases()

	if got := rec.releases(); len(got) != 1 || got[0] != "worker-1" {
		t.Fatalf("released = %v, want exactly one release for worker-1", got)
	}
}

func TestReleaseAllOwnershipLeasesNoopWithoutControlPlane(t *testing.T) {
	s, rec := shutdownSweepSupervisor(t, map[string]string{"worker-1": "token-1"})
	s.ControlStore = nil

	s.releaseAllOwnershipLeases()

	if got := rec.releases(); len(got) != 0 {
		t.Fatalf("released = %v, want none when the control plane is disabled", got)
	}
}
