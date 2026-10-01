package supervisor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/clock"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var clockSeamEpoch = time.Date(2026, 9, 28, 4, 17, 33, 0, time.UTC)

// signalingOwnershipStore answers Heartbeat from a script and reports each
// call on a channel so a test can wait for the heartbeat goroutine without
// sleeping. Unscripted calls fail as inconclusive (untyped) errors.
type signalingOwnershipStore struct {
	scriptedOwnershipLeaseStore
	mu      sync.Mutex
	results []error
	calls   chan struct{}
}

func (f *signalingOwnershipStore) Heartbeat(context.Context, string, string, string, time.Duration) (*domain.AgentOwnershipLease, error) {
	f.mu.Lock()
	err := errors.New("dial tcp: i/o timeout")
	if len(f.results) > 0 {
		err = f.results[0]
		f.results = f.results[1:]
	}
	f.mu.Unlock()
	defer func() { f.calls <- struct{}{} }()
	if err != nil {
		return nil, err
	}
	return &domain.AgentOwnershipLease{LeaseID: "ol-agent-1", Token: "TOKEN_FIRST", FencingToken: 1, Status: domain.AgentLeaseActive}, nil
}

var _ store.AgentOwnershipLeaseStore = (*signalingOwnershipStore)(nil)

// The ownership heartbeat loop runs on the injected clock: one renewal at the
// first interval, then inconclusive failures ride out the fail-open window and
// the kill lands exactly when the fake clock reaches renewal+TTL — not one
// interval earlier — with no wall-clock wait.
func TestClockSeam_OwnershipHeartbeatKillsExactlyAtFakeTTL(t *testing.T) {
	fake := clock.NewFake(clockSeamEpoch)
	st := &signalingOwnershipStore{results: []error{nil}, calls: make(chan struct{}, 4)}
	s := newOwnershipVerifyTestSupervisor(&st.scriptedOwnershipLeaseStore)
	s.ControlStore = &ownershipStoreOverride{Store: s.ControlStore.(*ownershipStoreOverride).Store, ownership: st}
	s.Clock = fake
	ap := newOwnershipVerifyAgent()
	ap.OwnershipRenewedAt = fake.Now()

	ttl := defaultLeaseTTL
	interval := ownershipHeartbeatBaseInterval(ttl)
	stop := s.startOwnershipHeartbeat(ap)

	fake.BlockUntilWaiters(1)
	fake.Advance(interval)
	<-st.calls
	fake.BlockUntilWaiters(1)
	ap.Mu.Lock()
	renewedAt := ap.OwnershipRenewedAt
	ap.Mu.Unlock()
	if want := clockSeamEpoch.Add(interval); !renewedAt.Equal(want) {
		t.Fatalf("OwnershipRenewedAt = %v, want fake send time %v", renewedAt, want)
	}

	deadline := renewedAt.Add(ttl)
	ticks := 0
	for {
		fake.Advance(interval)
		<-st.calls // heartbeat
		<-st.calls // immediate retry
		ticks++
		if !fake.Now().Before(deadline) {
			break
		}
		fake.BlockUntilWaiters(1)
		ap.Mu.Lock()
		killed := ap.LastError != nil
		ap.Mu.Unlock()
		if killed {
			t.Fatalf("killed at %v, before fake TTL deadline %v", fake.Now(), deadline)
		}
	}
	stop()

	if want := int(ttl / interval); ticks != want {
		t.Fatalf("failure ticks before kill = %d, want %d", ticks, want)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil || !strings.Contains(ap.LastError.Message, "ownership_unverifiable") {
		t.Fatalf("LastError = %+v, want ownership_unverifiable kill", ap.LastError)
	}
	if !ap.LastError.Timestamp.Equal(deadline) {
		t.Fatalf("kill timestamp = %v, want fake deadline %v", ap.LastError.Timestamp, deadline)
	}
}

// Liveness ticks and the run-duration kill read the injected clock.
func TestClockSeam_LivenessTicksAndRunDurationKillUseFakeClock(t *testing.T) {
	fake := clock.NewFake(clockSeamEpoch)
	s := &Supervisor{Clock: fake}
	s.RegisterTick("agent:x")
	if got, _ := s.LoadTick("agent:x"); !got.Equal(clockSeamEpoch) {
		t.Fatalf("RegisterTick stamped %v, want fake now", got)
	}
	fake.Advance(42 * time.Second)
	s.RecordTick("agent:x")
	if got, _ := s.LoadTick("agent:x"); !got.Equal(clockSeamEpoch.Add(42 * time.Second)) {
		t.Fatalf("RecordTick stamped %v", got)
	}

	maxRun := 60
	ap := &AgentProcess{}
	ap.RoleConfig.MaxRunDuration = &maxRun
	lastStart := fake.Now()
	fake.Advance(time.Minute)
	if s.applyRunDurationKill(ap, lastStart, "x") {
		t.Fatal("killed at exactly max run duration")
	}
	fake.Advance(time.Millisecond)
	if !s.applyRunDurationKill(ap, lastStart, "x") {
		t.Fatal("not killed past max run duration on the fake clock")
	}
	if ap.StopReason != StopReasonRunDurationExceeded {
		t.Fatalf("StopReason = %v", ap.StopReason)
	}
}

// A nil Clock keeps production behavior.
func TestClockSeam_NilClockIsReal(t *testing.T) {
	if (&Supervisor{}).clk() != clock.Real {
		t.Fatal("nil Supervisor.Clock did not resolve to clock.Real")
	}
}
