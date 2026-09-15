package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// reclaimLeaseStore is a fake that serves one Get result and one scripted
// Acquire result, recording the takeover the supervisor asked for.
type reclaimLeaseStore struct {
	mu sync.Mutex

	getLease *domain.AgentOwnershipLease
	getErr   error

	acquireLease *domain.AgentOwnershipLease
	acquireErr   error

	getCalls     int
	acquireCalls int
	lastTakeover string
}

func (f *reclaimLeaseStore) Acquire(_ context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquireCalls++
	f.lastTakeover = in.TakeoverFromOwnerID
	if f.acquireErr != nil {
		return nil, f.acquireErr
	}
	lease := *f.acquireLease
	lease.AgentID = in.AgentID
	lease.WorkspaceKey = in.WorkspaceKey
	lease.OwnerID = in.OwnerID
	return &lease, nil
}

func (f *reclaimLeaseStore) Get(context.Context, string, string) (*domain.AgentOwnershipLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getLease, nil
}

func (f *reclaimLeaseStore) List(context.Context, string, store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	return nil, nil
}

func (f *reclaimLeaseStore) Heartbeat(context.Context, string, string, string, time.Duration) (*domain.AgentOwnershipLease, error) {
	return nil, errors.New("reclaim fake: unexpected Heartbeat call")
}

func (f *reclaimLeaseStore) Release(context.Context, string, string, string) (*domain.AgentOwnershipLease, error) {
	return nil, nil
}

func newReclaimTestSupervisor(fake *reclaimLeaseStore, nodeID string) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		WorkspaceID:    "WS",
		ControlStore:   &ownershipStoreOverride{Store: memstore.New(), ownership: fake},
		NodeID:         nodeID,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*AgentProcess, 0),
		Concurrency:    NewConcurrencyTracker(nil),
		EmitEvent:      func(events.Event) {},
	}
}

// pinPIDLiveness swaps the package-level liveness seam for one test.
func pinPIDLiveness(t *testing.T, alive bool) {
	t.Helper()
	prev := ownershipPIDIsRunning
	ownershipPIDIsRunning = func(int) bool { return alive }
	t.Cleanup(func() { ownershipPIDIsRunning = prev })
}

func localHostForTest(t *testing.T) string {
	t.Helper()
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skip("hostname unavailable; the reclaim probe is host-scoped by design")
	}
	return host
}

func activeLease(ownerID string) *domain.AgentOwnershipLease {
	return &domain.AgentOwnershipLease{
		WorkspaceKey:    "WS",
		AgentID:         "agent-1",
		LeaseID:         "ol-agent-1",
		OwnerID:         ownerID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		Status:          domain.AgentLeaseActive,
		ExpiresAt:       time.Now().Add(20 * time.Minute),
		LastHeartbeat:   time.Now(),
	}
}

// The whole steal policy in one table. Every case that falls short of
// "provably a dead supervisor process on this host" must decline.
func TestAbandonedOwnerID_Policy(t *testing.T) {
	const selfPID = 4242
	self := fmt.Sprintf("loom-supervisor-thishost-%d", selfPID)
	dead := fmt.Sprintf("loom-supervisor-thishost-%d", selfPID+1)
	now := time.Now()

	withLease := func(mutate func(*domain.AgentOwnershipLease)) *domain.AgentOwnershipLease {
		l := activeLease(dead)
		if mutate != nil {
			mutate(l)
		}
		return l
	}

	cases := []struct {
		name      string
		lease     *domain.AgentOwnershipLease
		localHost string
		alive     bool
		want      string
	}{
		{"local host, dead pid, active lease", withLease(nil), "thishost", false, dead},
		{"owner pid still alive", withLease(nil), "thishost", true, ""},
		{"remote host", withLease(func(l *domain.AgentOwnershipLease) {
			l.OwnerID = "loom-supervisor-otherhost-99"
		}), "thishost", false, ""},
		{"unparseable owner", withLease(func(l *domain.AgentOwnershipLease) {
			l.OwnerID = "fleet-runner-abc"
		}), "thishost", false, ""},
		{"owner pid not numeric", withLease(func(l *domain.AgentOwnershipLease) {
			l.OwnerID = "loom-supervisor-thishost-nope"
		}), "thishost", false, ""},
		{"our own node id", withLease(func(l *domain.AgentOwnershipLease) {
			l.OwnerID = self
		}), "thishost", false, ""},
		{"our own pid under a different node id", withLease(func(l *domain.AgentOwnershipLease) {
			l.OwnerID = fmt.Sprintf("loom-supervisor-thishost-%d", selfPID)
		}), "thishost", false, ""},
		{"expired lease", withLease(func(l *domain.AgentOwnershipLease) {
			l.ExpiresAt = now.Add(-time.Second)
		}), "thishost", false, ""},
		{"released lease", withLease(func(l *domain.AgentOwnershipLease) {
			l.Status = domain.AgentLeaseReleased
		}), "thishost", false, ""},
		{"remote runtime provider", withLease(func(l *domain.AgentOwnershipLease) {
			l.RuntimeProvider = domain.RuntimeProvider("daytona")
		}), "thishost", false, ""},
		{"empty owner", withLease(func(l *domain.AgentOwnershipLease) {
			l.OwnerID = ""
		}), "thishost", false, ""},
		{"unknown local hostname", withLease(nil), "", false, ""},
		{"nil lease", nil, "thishost", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isRunning := func(int) bool { return tc.alive }
			got, ok := abandonedOwnerID(tc.lease, self, selfPID, tc.localHost, isRunning, now)
			if tc.want == "" {
				if ok {
					t.Fatalf("abandonedOwnerID = (%q, true), want no steal", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("abandonedOwnerID = (%q, %v), want (%q, true)", got, ok, tc.want)
			}
		})
	}
}

// The reclaim: the acquire must carry the observed owner_id as the
// compare-and-steal target, and the steal must be logged loudly with both
// owners named.
func TestReclaimAbandonedOwnership_StealsFromDeadLocalOwner(t *testing.T) {
	host := localHostForTest(t)
	deadOwner := fmt.Sprintf("loom-supervisor-%s-%d", host, os.Getpid()+1)
	pinPIDLiveness(t, false)
	logs := captureSlog(t)

	fake := &reclaimLeaseStore{
		getLease:     activeLease(deadOwner),
		acquireLease: freshOwnershipLease("TOKEN_STOLEN", 7),
	}
	s := newReclaimTestSupervisor(fake, "node-new")
	ap := newOwnershipVerifyAgent()

	if !s.reclaimAbandonedOwnership(ap) {
		t.Fatal("reclaimAbandonedOwnership = false, want a steal from a dead local owner")
	}
	if fake.lastTakeover != deadOwner {
		t.Fatalf("TakeoverFromOwnerID = %q, want the observed owner %q", fake.lastTakeover, deadOwner)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.OwnershipLeaseToken != "TOKEN_STOLEN" || ap.OwnershipFencingToken != 7 {
		t.Fatalf("lease state = (%q, %d), want the freshly issued lease", ap.OwnershipLeaseToken, ap.OwnershipFencingToken)
	}
	out := logs.String()
	if !strings.Contains(out, "reclaiming abandoned agent ownership lease") ||
		!strings.Contains(out, deadOwner) || !strings.Contains(out, "node-new") {
		t.Fatalf("logs missing the loud steal naming both owners:\n%s", out)
	}
}

// Two supervisors reclaiming the same lease: the loser's compare-and-steal
// is answered truthfully with 409 and must fall back to the retry loop.
func TestReclaimAbandonedOwnership_LostRaceFallsBack(t *testing.T) {
	host := localHostForTest(t)
	deadOwner := fmt.Sprintf("loom-supervisor-%s-%d", host, os.Getpid()+1)
	pinPIDLiveness(t, false)

	fake := &reclaimLeaseStore{
		getLease:   activeLease(deadOwner),
		acquireErr: wrappedSentinel(domain.ErrAlreadyClaimed),
	}
	s := newReclaimTestSupervisor(fake, "node-new")

	if s.reclaimAbandonedOwnership(newOwnershipVerifyAgent()) {
		t.Fatal("reclaimAbandonedOwnership = true, want false after losing the steal race")
	}
	if fake.acquireCalls != 1 {
		t.Fatalf("acquire calls = %d, want exactly 1 (no retry storm)", fake.acquireCalls)
	}
}

// Version skew: a fleet-db that does not know takeover_from_owner_id
// rejects the body outright. That must degrade to today's wait, not to a
// new failure mode.
func TestReclaimAbandonedOwnership_InvalidTakeoverFallsBack(t *testing.T) {
	host := localHostForTest(t)
	deadOwner := fmt.Sprintf("loom-supervisor-%s-%d", host, os.Getpid()+1)
	pinPIDLiveness(t, false)

	fake := &reclaimLeaseStore{
		getLease:   activeLease(deadOwner),
		acquireErr: wrappedSentinel(domain.ErrInvalid),
	}
	s := newReclaimTestSupervisor(fake, "node-new")

	if s.reclaimAbandonedOwnership(newOwnershipVerifyAgent()) {
		t.Fatal("reclaimAbandonedOwnership = true, want false when the server rejects the takeover")
	}
}

// No evidence, no steal: an unreadable lease never reaches the acquire.
func TestReclaimAbandonedOwnership_GetErrorDoesNotSteal(t *testing.T) {
	pinPIDLiveness(t, false)
	fake := &reclaimLeaseStore{getErr: errors.New("dial tcp: i/o timeout")}
	s := newReclaimTestSupervisor(fake, "node-new")

	if s.reclaimAbandonedOwnership(newOwnershipVerifyAgent()) {
		t.Fatal("reclaimAbandonedOwnership = true, want false with no lease evidence")
	}
	if fake.acquireCalls != 0 {
		t.Fatalf("acquire calls = %d, want 0 (no evidence, no steal)", fake.acquireCalls)
	}
}

// A live third-party owner is never stolen from — the negative control for
// the split-brain guard.
func TestReclaimAbandonedOwnership_LiveOwnerIsNeverStolenFrom(t *testing.T) {
	host := localHostForTest(t)
	liveOwner := fmt.Sprintf("loom-supervisor-%s-%d", host, os.Getpid()+1)
	pinPIDLiveness(t, true)

	fake := &reclaimLeaseStore{getLease: activeLease(liveOwner)}
	s := newReclaimTestSupervisor(fake, "node-new")

	if s.reclaimAbandonedOwnership(newOwnershipVerifyAgent()) {
		t.Fatal("reclaimAbandonedOwnership = true, want false against a live owner")
	}
	if fake.acquireCalls != 0 {
		t.Fatalf("acquire calls = %d, want 0 (a live owner must never be stolen from)", fake.acquireCalls)
	}
}

// Control plane disabled: the probe returns immediately, matching
// acquireAgentOwnership's own short-circuit.
func TestReclaimAbandonedOwnership_NoControlPlane(t *testing.T) {
	fake := &reclaimLeaseStore{}
	s := newReclaimTestSupervisor(fake, "node-new")
	s.ControlStore = nil
	if s.reclaimAbandonedOwnership(newOwnershipVerifyAgent()) {
		t.Fatal("reclaimAbandonedOwnership = true, want false with no control plane")
	}
	if fake.getCalls != 0 {
		t.Fatalf("get calls = %d, want 0", fake.getCalls)
	}
}

// The supervision loop's wrapper: an ordinary acquire needs no probe, and
// an inconclusive one must not trigger a steal attempt either.
func TestAcquireAgentOwnershipWithReclaim_OnlyProbesOnHeldByOther(t *testing.T) {
	cases := []struct {
		name      string
		acquire   error
		wantOK    bool
		wantProbe bool
	}{
		{"acquired", nil, true, false},
		{"inconclusive", errors.New("dial tcp: i/o timeout"), false, false},
		{"held by other", wrappedSentinel(domain.ErrAlreadyClaimed), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinPIDLiveness(t, true) // any probe declines, so wantOK stays false
			fake := &reclaimLeaseStore{
				acquireErr:   tc.acquire,
				acquireLease: freshOwnershipLease("TOKEN_FIRST", 1),
				getLease:     activeLease("loom-supervisor-otherhost-99"),
			}
			s := newReclaimTestSupervisor(fake, "node-new")
			if got := s.acquireAgentOwnershipWithReclaim(newOwnershipVerifyAgent()); got != tc.wantOK {
				t.Fatalf("acquireAgentOwnershipWithReclaim = %v, want %v", got, tc.wantOK)
			}
			gotProbe := fake.getCalls > 0
			if gotProbe != tc.wantProbe {
				t.Fatalf("lease probed = %v, want %v", gotProbe, tc.wantProbe)
			}
		})
	}
}
