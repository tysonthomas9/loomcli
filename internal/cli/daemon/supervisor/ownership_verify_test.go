package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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

// scriptedOwnershipLeaseStore is a purpose-built fake with per-call
// programmable results. The shared memstore is deliberately NOT used for
// these tests: its ownership implementation collapses missing/wrong-token/
// expired heartbeat failures into a single ErrConflict and does not model
// fleet-db's acquire semantics (CLAIMED vs same-owner-live token
// preservation) — exactly the distinctions under test here.
type scriptedOwnershipLeaseStore struct {
	mu sync.Mutex

	heartbeatResults []error // dequeued per Heartbeat call; nil = success
	acquireResults   []scriptedAcquireResult

	heartbeatCalls int
	acquireCalls   int
}

type scriptedAcquireResult struct {
	lease *domain.AgentOwnershipLease
	err   error
}

func (f *scriptedOwnershipLeaseStore) Acquire(_ context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquireCalls++
	if len(f.acquireResults) == 0 {
		return nil, errors.New("scripted store: unscripted Acquire call")
	}
	res := f.acquireResults[0]
	f.acquireResults = f.acquireResults[1:]
	if res.err != nil {
		return nil, res.err
	}
	lease := *res.lease
	lease.AgentID = in.AgentID
	lease.WorkspaceKey = in.WorkspaceKey
	return &lease, nil
}

func (f *scriptedOwnershipLeaseStore) Get(context.Context, string, string) (*domain.AgentOwnershipLease, error) {
	return nil, errors.New("scripted store: unexpected Get call")
}

func (f *scriptedOwnershipLeaseStore) List(context.Context, string, store.AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error) {
	return nil, nil
}

func (f *scriptedOwnershipLeaseStore) Heartbeat(context.Context, string, string, string, time.Duration) (*domain.AgentOwnershipLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeatCalls++
	if len(f.heartbeatResults) == 0 {
		return nil, errors.New("scripted store: unscripted Heartbeat call")
	}
	err := f.heartbeatResults[0]
	f.heartbeatResults = f.heartbeatResults[1:]
	if err != nil {
		return nil, err
	}
	return &domain.AgentOwnershipLease{
		AgentID:       "agent-1",
		LeaseID:       "ol-agent-1",
		Token:         "TOKEN_FIRST",
		FencingToken:  1,
		Status:        domain.AgentLeaseActive,
		LastHeartbeat: time.Now(),
		ExpiresAt:     time.Now().Add(time.Minute),
	}, nil
}

func (f *scriptedOwnershipLeaseStore) Release(context.Context, string, string, string) (*domain.AgentOwnershipLease, error) {
	return nil, nil
}

// ownershipStoreOverride swaps only the ownership-lease store; everything
// else is the shared memstore (unused by these tests).
type ownershipStoreOverride struct {
	*memstore.Store
	ownership store.AgentOwnershipLeaseStore
}

func (s *ownershipStoreOverride) AgentOwnershipLeases() store.AgentOwnershipLeaseStore {
	return s.ownership
}

func newOwnershipVerifyTestSupervisor(fake *scriptedOwnershipLeaseStore) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{}
		},
		WorkspaceID:   "WS",
		ControlStore:  &ownershipStoreOverride{Store: memstore.New(), ownership: fake},
		NodeID:        "node-1",
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		Concurrency:   NewConcurrencyTracker(nil),
		EmitEvent:     func(events.Event) {},
	}
}

// newOwnershipVerifyAgent returns an AgentProcess that reads as running
// (Cmd non-nil, Pid non-zero, ProcessState nil) without spawning anything;
// StopAgent's proc.Process==nil guard keeps kill paths side-effect-free.
func newOwnershipVerifyAgent() *AgentProcess {
	return &AgentProcess{
		Entry:                 cfgpkg.AgentEntry{Worktree: "agent-1", Role: "worker"},
		Cmd:                   &exec.Cmd{},
		Pid:                   4242,
		OwnershipLeaseID:      "ol-agent-1",
		OwnershipLeaseToken:   "TOKEN_FIRST",
		OwnershipFencingToken: 1,
		OwnershipRenewedAt:    time.Now(),
		StopCh:                make(chan struct{}),
	}
}

func freshOwnershipLease(token string, fence int64) *domain.AgentOwnershipLease {
	return &domain.AgentOwnershipLease{
		LeaseID:       "ol-agent-1",
		OwnerID:       "node-1",
		Token:         token,
		FencingToken:  fence,
		Status:        domain.AgentLeaseActive,
		LastHeartbeat: time.Now(),
		ExpiresAt:     time.Now().Add(time.Minute),
	}
}

func wrappedSentinel(sentinel error) error {
	return fmt.Errorf("fleetdb: POST /api/v1/WS/agent-ownership-leases/agent-1/heartbeat: HTTP xxx: heartbeat agent ownership lease failed: %w", sentinel)
}

// syncBuffer guards the capture buffer against concurrent slog writes from
// any parallel test sharing the default logger.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// Headline: a 410/ErrGone heartbeat (lease lapsed, e.g. after a suspend
// stall) re-acquires and continues — no kill, fence bumped, fresh token.
func TestVerifyAgentOwnership_GoneReacquiresAndContinues(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{wrappedSentinel(domain.ErrGone)},
		acquireResults:   []scriptedAcquireResult{{lease: freshOwnershipLease("TOKEN_FRESH", 7)}},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()

	if !s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned false, want continue")
	}
	if fake.acquireCalls != 1 {
		t.Fatalf("acquire calls = %d, want exactly 1", fake.acquireCalls)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError != nil {
		t.Fatalf("LastError = %v, want nil (no kill)", ap.LastError)
	}
	if ap.OwnershipFencingToken != 7 {
		t.Fatalf("fencing token = %d, want re-acquired fence 7", ap.OwnershipFencingToken)
	}
	if ap.OwnershipLeaseToken != "TOKEN_FRESH" {
		t.Fatalf("token = %q, want re-acquired token", ap.OwnershipLeaseToken)
	}
}

// Old fleet-db still answers 409 → ErrAlreadyExists; the verify path must
// treat it identically to ErrGone (this is why verification triggers on all
// errors, not just the new sentinel).
func TestVerifyAgentOwnership_Legacy409ReacquiresAndContinues(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{wrappedSentinel(domain.ErrAlreadyExists)},
		acquireResults:   []scriptedAcquireResult{{lease: freshOwnershipLease("TOKEN_FRESH", 9)}},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()

	if !s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned false, want continue")
	}
	if fake.acquireCalls != 1 {
		t.Fatalf("acquire calls = %d, want exactly 1", fake.acquireCalls)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError != nil {
		t.Fatalf("LastError = %v, want nil (no kill)", ap.LastError)
	}
}

// An untyped failure (timeout/connection/5xx) is verified by one immediate
// heartbeat retry; success means no kill, no re-acquire, and the renewal
// anchor advances (the retry is a genuine renewal).
func TestVerifyAgentOwnership_TransientRetrySucceeds(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{errors.New("connection reset by peer"), nil},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()
	anchor := time.Now().Add(-30 * time.Second)
	ap.OwnershipRenewedAt = anchor

	before := time.Now()
	if !s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned false, want continue")
	}
	if fake.heartbeatCalls != 2 {
		t.Fatalf("heartbeat calls = %d, want 2 (original + retry)", fake.heartbeatCalls)
	}
	if fake.acquireCalls != 0 {
		t.Fatalf("acquire calls = %d, want 0", fake.acquireCalls)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError != nil {
		t.Fatalf("LastError = %v, want nil (no kill)", ap.LastError)
	}
	if ap.OwnershipRenewedAt.Before(before) {
		t.Fatalf("OwnershipRenewedAt = %v, want advanced by the retry (>= %v)", ap.OwnershipRenewedAt, before)
	}
}

// Re-acquire answered CLAIMED (held by another daemon): ownership is
// verifiably lost — kill with reason verifiably_lost.
func TestVerifyAgentOwnership_HeldByOtherDaemonKills(t *testing.T) {
	logs := captureSlog(t)
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{wrappedSentinel(domain.ErrGone)},
		acquireResults:   []scriptedAcquireResult{{err: wrappedSentinel(domain.ErrAlreadyExists)}},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()

	if s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned true, want kill")
	}
	if fake.acquireCalls != 1 {
		t.Fatalf("acquire calls = %d, want exactly 1", fake.acquireCalls)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil {
		t.Fatal("LastError = nil, want kill error")
	}
	if !strings.Contains(ap.LastError.Message, "verifiably_lost") {
		t.Fatalf("LastError.Message = %q, want reason verifiably_lost", ap.LastError.Message)
	}
	if !strings.Contains(logs.String(), "reason=verifiably_lost") {
		t.Fatalf("logs missing reason=verifiably_lost:\n%s", logs.String())
	}
}

// The split-brain regression: a heartbeat failure whose re-acquire is
// answered with 409 already_claimed must kill the agent. Before
// ErrAlreadyClaimed was classified as heldByOther this took the bounded
// fail-open branch and kept running an agent whose lease the server had
// already handed to someone else.
func TestVerifyAgentOwnership_AlreadyClaimedKills(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{wrappedSentinel(domain.ErrGone)},
		acquireResults:   []scriptedAcquireResult{{err: wrappedSentinel(domain.ErrAlreadyClaimed)}},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()
	ap.OwnershipRenewedAt = time.Now() // fresh: fail-open would have continued

	if s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned true, want kill on a verifiably lost lease")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil || !strings.Contains(ap.LastError.Message, "verifiably_lost") {
		t.Fatalf("LastError = %v, want a verifiably_lost kill", ap.LastError)
	}
}

// isTypedDomainError gates whether a heartbeat failure is arbitrated at
// all; ErrAlreadyClaimed is a server verdict, so it must be typed.
func TestIsTypedDomainError_AlreadyClaimed(t *testing.T) {
	if !isTypedDomainError(wrappedSentinel(domain.ErrAlreadyClaimed)) {
		t.Fatal("isTypedDomainError(ErrAlreadyClaimed) = false, want true")
	}
}

// Inconclusive verification within the validity window rides through (no
// kill) — both inconclusive shapes: retry-still-untyped and
// acquire-inconclusive.
func TestVerifyAgentOwnership_InconclusiveWithinValidityDoesNotKill(t *testing.T) {
	t.Run("untyped retry untyped again", func(t *testing.T) {
		fake := &scriptedOwnershipLeaseStore{
			heartbeatResults: []error{errors.New("dial tcp: i/o timeout"), errors.New("dial tcp: i/o timeout")},
		}
		s := newOwnershipVerifyTestSupervisor(fake)
		ap := newOwnershipVerifyAgent()
		ap.OwnershipRenewedAt = time.Now() // fresh: within validity

		if !s.heartbeatAgentOwnership(ap, time.Minute) {
			t.Fatal("heartbeat returned false, want fail-open continue")
		}
		if fake.acquireCalls != 0 {
			t.Fatalf("acquire calls = %d, want 0", fake.acquireCalls)
		}
		ap.Mu.Lock()
		defer ap.Mu.Unlock()
		if ap.LastError != nil {
			t.Fatalf("LastError = %v, want nil (no kill)", ap.LastError)
		}
	})

	t.Run("typed error then acquire inconclusive", func(t *testing.T) {
		fake := &scriptedOwnershipLeaseStore{
			heartbeatResults: []error{wrappedSentinel(domain.ErrGone)},
			acquireResults:   []scriptedAcquireResult{{err: errors.New("dial tcp: i/o timeout")}},
		}
		s := newOwnershipVerifyTestSupervisor(fake)
		ap := newOwnershipVerifyAgent()
		ap.OwnershipRenewedAt = time.Now()

		if !s.heartbeatAgentOwnership(ap, time.Minute) {
			t.Fatal("heartbeat returned false, want fail-open continue")
		}
		ap.Mu.Lock()
		defer ap.Mu.Unlock()
		if ap.LastError != nil {
			t.Fatalf("LastError = %v, want nil (no kill)", ap.LastError)
		}
	})
}

// Past the validity bound, an inconclusive verification fails closed:
// ownership is genuinely unknown AND acquirable by others.
func TestVerifyAgentOwnership_InconclusivePastValidityFailsClosed(t *testing.T) {
	logs := captureSlog(t)
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{errors.New("dial tcp: i/o timeout"), errors.New("dial tcp: i/o timeout")},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()
	ap.OwnershipRenewedAt = time.Now().Add(-2 * time.Minute) // past ttl

	if s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned true, want fail-closed kill")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil {
		t.Fatal("LastError = nil, want kill error")
	}
	if !strings.Contains(ap.LastError.Message, "ownership_unverifiable") {
		t.Fatalf("LastError.Message = %q, want reason ownership_unverifiable", ap.LastError.Message)
	}
	if !strings.Contains(logs.String(), "reason=ownership_unverifiable") {
		t.Fatalf("logs missing reason=ownership_unverifiable:\n%s", logs.String())
	}
}

// Direct tests of the pure dual-clock validity predicate.
func TestOwnershipWithinValidity_DualClockClauses(t *testing.T) {
	ttl := time.Minute
	if !ownershipWithinValidity(time.Now(), ttl) {
		t.Fatal("fresh anchor: want within validity")
	}
	// Monotonic clause expired (anchor carries a monotonic reading).
	if ownershipWithinValidity(time.Now().Add(-2*ttl), ttl) {
		t.Fatal("stale monotonic anchor: want past validity")
	}
	// Wall clause expired (monotonic reading stripped via Round(0), so only
	// the wall-clock path is exercised).
	if ownershipWithinValidity(time.Now().Add(-2*ttl).Round(0), ttl) {
		t.Fatal("stale wall-only anchor: want past validity")
	}
	if ownershipWithinValidity(time.Time{}, ttl) {
		t.Fatal("zero anchor: want past validity (fail closed)")
	}
}

func TestOwnershipHeartbeatDelay_CapsAtRemainingValidity(t *testing.T) {
	ttl := 2 * time.Minute
	interval := 30 * time.Second
	ap := newOwnershipVerifyAgent()

	ap.OwnershipRenewedAt = time.Now()
	if got := nextOwnershipHeartbeatDelay(ap, interval, ttl); got != interval {
		t.Fatalf("fresh renewal delay = %v, want base interval %v", got, interval)
	}

	remaining := 3 * time.Second
	ap.OwnershipRenewedAt = time.Now().Add(-(ttl - remaining))
	got := nextOwnershipHeartbeatDelay(ap, interval, ttl)
	if got <= 0 {
		t.Fatalf("near-expiry delay = %v, want positive remaining validity", got)
	}
	if got >= interval {
		t.Fatalf("near-expiry delay = %v, want capped below base interval %v", got, interval)
	}
	if got > remaining {
		t.Fatalf("near-expiry delay = %v, want <= remaining validity %v", got, remaining)
	}

	ap.OwnershipRenewedAt = time.Now().Add(-ttl)
	if got := nextOwnershipHeartbeatDelay(ap, interval, ttl); got != 0 {
		t.Fatalf("expired renewal delay = %v, want immediate verification", got)
	}
}

// The renewal anchor is set from a pre-request time.Now() at BOTH wiring
// sites, and only on success.
func TestHeartbeatSuccess_AdvancesOwnershipRenewedAt(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{heartbeatResults: []error{nil}}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()
	ap.OwnershipRenewedAt = time.Time{}

	before := time.Now()
	if !s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned false, want success")
	}
	after := time.Now()
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.OwnershipRenewedAt.Before(before) || ap.OwnershipRenewedAt.After(after) {
		t.Fatalf("OwnershipRenewedAt = %v, want within [%v, %v]", ap.OwnershipRenewedAt, before, after)
	}
}

func TestHeartbeatFailure_DoesNotAdvanceOwnershipRenewedAt(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{errors.New("dial tcp: i/o timeout"), errors.New("dial tcp: i/o timeout")},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()
	anchor := time.Now() // fresh enough to fail open
	ap.OwnershipRenewedAt = anchor

	if !s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned false, want fail-open continue")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if !ap.OwnershipRenewedAt.Equal(anchor) {
		t.Fatalf("OwnershipRenewedAt = %v, want unchanged %v (no successful renewal)", ap.OwnershipRenewedAt, anchor)
	}
}

func TestAcquireSuccess_SetsOwnershipRenewedAt(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{
		acquireResults: []scriptedAcquireResult{{lease: freshOwnershipLease("TOKEN_FIRST", 1)}},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()
	ap.OwnershipRenewedAt = time.Time{}

	before := time.Now()
	if got := s.acquireAgentOwnership(ap); got != ownershipAcquired {
		t.Fatalf("acquire outcome = %v, want ownershipAcquired", got)
	}
	after := time.Now()
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.OwnershipRenewedAt.Before(before) || ap.OwnershipRenewedAt.After(after) {
		t.Fatalf("OwnershipRenewedAt = %v, want within [%v, %v]", ap.OwnershipRenewedAt, before, after)
	}
}

func TestAcquireAgentOwnership_TriStateOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		result scriptedAcquireResult
		want   ownershipAcquireOutcome
	}{
		{"success", scriptedAcquireResult{lease: freshOwnershipLease("TOKEN_FIRST", 1)}, ownershipAcquired},
		{"held by other (already exists)", scriptedAcquireResult{err: wrappedSentinel(domain.ErrAlreadyExists)}, ownershipHeldByOther},
		{"held by other (conflict)", scriptedAcquireResult{err: wrappedSentinel(domain.ErrConflict)}, ownershipHeldByOther},
		// fleet-db answers a contested acquire with 409 already_claimed,
		// which the HTTP client maps to ErrAlreadyClaimed. This is the
		// ticket's log line: it used to fall through to inconclusive.
		{"held by other (already claimed)", scriptedAcquireResult{err: wrappedSentinel(domain.ErrAlreadyClaimed)}, ownershipHeldByOther},
		{"network error", scriptedAcquireResult{err: errors.New("dial tcp: i/o timeout")}, ownershipAcquireInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &scriptedOwnershipLeaseStore{acquireResults: []scriptedAcquireResult{tc.result}}
			s := newOwnershipVerifyTestSupervisor(fake)
			ap := newOwnershipVerifyAgent()
			if got := s.acquireAgentOwnership(ap); got != tc.want {
				t.Fatalf("outcome = %v, want %v", got, tc.want)
			}
		})
	}
}

// Disabled control plane maps to acquired: non-control-plane supervision
// keeps today's behavior exactly.
func TestAcquireAgentOwnership_NoControlPlaneMapsToAcquired(t *testing.T) {
	s := newOwnershipVerifyTestSupervisor(&scriptedOwnershipLeaseStore{})
	s.ControlStore = nil
	ap := newOwnershipVerifyAgent()
	if got := s.acquireAgentOwnership(ap); got != ownershipAcquired {
		t.Fatalf("outcome = %v, want ownershipAcquired with nil ControlStore", got)
	}
}

// A dead process must never be kept alive server-side: no re-acquire, just
// unwind (superviseAgent handles the exit moments later).
func TestVerifyAgentOwnership_DeadProcessDoesNotResurrect(t *testing.T) {
	fake := &scriptedOwnershipLeaseStore{
		heartbeatResults: []error{wrappedSentinel(domain.ErrGone)},
	}
	s := newOwnershipVerifyTestSupervisor(fake)
	ap := newOwnershipVerifyAgent()
	// Exited-but-unreaped shape: ProcessState already populated. No real
	// process is needed — the dead-guard only checks ProcessState != nil.
	ap.Cmd = &exec.Cmd{ProcessState: &os.ProcessState{}}
	ap.Pid = 4242

	if s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeat returned true, want false for dead process")
	}
	if fake.acquireCalls != 0 {
		t.Fatalf("acquire calls = %d, want 0 (no resurrection)", fake.acquireCalls)
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError != nil {
		t.Fatalf("LastError = %v, want nil (unwind, not a kill)", ap.LastError)
	}
}

// Source-walk guard for the fencing-token env contract: the running
// subprocess keeps its spawn-time LOOM_AGENT_OWNERSHIP_FENCING_TOKEN while
// verify-re-acquire may bump the lease's fencing token. That is harmless
// only while NOTHING at runtime reads the variable. The first real reader
// breaks this test and must confront the re-acquire-and-continue contract
// in ownership.go before merging.
func TestOwnershipFencingEnvHasNoRuntimeReader(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s has no go.mod: %v", root, err)
	}
	allowed := filepath.Join("internal", "cli", "daemon", "supervisor", "spawn.go")
	var goFiles []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "webui" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	var offenders []string
	for _, path := range goFiles {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		if !bytes.Contains(data, []byte("LOOM_AGENT_OWNERSHIP_FENCING_TOKEN")) {
			continue
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			t.Fatalf("rel %s: %v", path, relErr)
		}
		if rel != allowed {
			offenders = append(offenders, rel)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("LOOM_AGENT_OWNERSHIP_FENCING_TOKEN referenced outside the spawn env-injection site: %v\n"+
			"If you added a runtime reader, the verify-re-acquire path in ownership.go must refresh the token (or kill instead of continuing).", offenders)
	}
}
