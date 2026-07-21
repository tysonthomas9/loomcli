package supervisor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestHeartbeatAgentSessionsOnceRenewsSessionAndLeaseWithoutIPC(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "session-1",
		AgentID:      "agent-1",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	lease, err := st.AgentLeases().Create(ctx, store.AgentLeaseCreate{
		WorkspaceKey: "WS",
		LeaseID:      "lease-1",
		SessionID:    "session-1",
		AgentID:      "agent-1",
		TTL:          time.Second,
	})
	if err != nil {
		t.Fatalf("create lease: %v", err)
	}
	beforeSession, err := st.AgentSessions().Get(ctx, "WS", "session-1")
	if err != nil {
		t.Fatalf("get session before heartbeat: %v", err)
	}
	beforeLease := lease.LastHeartbeat
	time.Sleep(time.Millisecond)

	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "agent-1"}}
	ap.AgentSessionID = "session-1"
	ap.AgentLeaseID = "lease-1"
	ap.AgentLeaseToken = lease.Token
	s := &Supervisor{
		ControlStore:  st,
		WorkspaceID:   "WS",
		Agents:        []*AgentProcess{ap},
		AgentLeaseTTL: 10 * time.Minute,
	}
	result := s.heartbeatAgentSessionsOnce()
	if result.Sessions != 1 || result.Leases != 1 || result.Failures != 0 {
		t.Fatalf("heartbeat result = %+v, want one session and lease with no failures", result)
	}

	afterSession, err := st.AgentSessions().Get(ctx, "WS", "session-1")
	if err != nil {
		t.Fatalf("get session after heartbeat: %v", err)
	}
	afterLease, err := st.AgentLeases().Get(ctx, "WS", "lease-1")
	if err != nil {
		t.Fatalf("get lease after heartbeat: %v", err)
	}
	if !afterSession.LastHeartbeat.After(beforeSession.LastHeartbeat) {
		t.Fatalf("session heartbeat did not advance: before=%v after=%v", beforeSession.LastHeartbeat, afterSession.LastHeartbeat)
	}
	if !afterLease.LastHeartbeat.After(beforeLease) {
		t.Fatalf("lease heartbeat did not advance: before=%v after=%v", beforeLease, afterLease.LastHeartbeat)
	}
	if remaining := time.Until(afterLease.ExpiresAt); remaining < 9*time.Minute {
		t.Fatalf("lease expiry was not renewed to configured TTL: remaining=%v", remaining)
	}
}

func TestHeartbeatAgentSessionsOnceSkipsIncompleteRegistration(t *testing.T) {
	s := &Supervisor{
		ControlStore: memstore.New(),
		WorkspaceID:  "WS",
		Agents: []*AgentProcess{
			{Entry: config.AgentEntry{Worktree: "no-session"}},
			nil,
		},
	}
	if result := s.heartbeatAgentSessionsOnce(); result != (agentSessionHeartbeatResult{}) {
		t.Fatalf("heartbeat result = %+v, want empty", result)
	}
}

func TestAgentSessionFinalizationDrainsInFlightStaleHeartbeat(t *testing.T) {
	ctx := t.Context()
	base := memstore.New()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := base.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "session-race",
		AgentID:      "agent-race",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	staleSessions := &staleWriteAgentSessionStore{
		AgentSessionStore: base.AgentSessions(),
		read:              make(chan struct{}),
		release:           make(chan struct{}),
	}
	controlStore := &heartbeatTestStore{Store: base, sessions: staleSessions, leases: base.AgentLeases()}
	ap := &AgentProcess{AgentSessionID: "session-race"}
	s := &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return &config.DaemonConfig{} },
		ControlStore:   controlStore,
		WorkspaceID:    "WS",
		Agents:         []*AgentProcess{ap},
	}

	heartbeatDone := make(chan agentSessionHeartbeatResult, 1)
	go func() { heartbeatDone <- s.heartbeatAgentSessionsOnce() }()
	select {
	case <-staleSessions.read:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not capture the running record")
	}

	finalizeDone := make(chan struct{})
	go func() {
		s.finalizeAgentSession(ap, 0)
		close(finalizeDone)
	}()
	select {
	case <-finalizeDone:
		t.Fatal("finalization completed before the in-flight heartbeat drained")
	case <-time.After(25 * time.Millisecond):
	}

	close(staleSessions.release)
	select {
	case <-heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not finish after release")
	}
	select {
	case <-finalizeDone:
	case <-time.After(time.Second):
		t.Fatal("finalization did not resume after heartbeat drain")
	}

	final, err := base.AgentSessions().Get(ctx, "WS", "session-race")
	if err != nil {
		t.Fatalf("get finalized session: %v", err)
	}
	if final.Status != domain.AgentSessionCompleted || final.FinishedAt == nil {
		t.Fatalf("final session = status %q finished_at %v, want completed terminal record", final.Status, final.FinishedAt)
	}
}

func TestAgentSessionRunningTransitionDrainsInFlightStaleHeartbeat(t *testing.T) {
	ctx := t.Context()
	base := memstore.New()
	if _, err := base.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := base.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "session-starting-race",
		AgentID:      "agent-race",
		Kind:         domain.AgentSessionKindTask,
		Status:       domain.AgentSessionStarting,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	staleSessions := &staleWriteAgentSessionStore{
		AgentSessionStore: base.AgentSessions(),
		read:              make(chan struct{}),
		release:           make(chan struct{}),
	}
	controlStore := &heartbeatTestStore{Store: base, sessions: staleSessions, leases: base.AgentLeases()}
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "agent-race"},
		AgentSessionID: "session-starting-race",
	}
	s := &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return &config.DaemonConfig{} },
		ControlStore:   controlStore,
		WorkspaceID:    "WS",
		Agents:         []*AgentProcess{ap},
	}

	heartbeatDone := make(chan agentSessionHeartbeatResult, 1)
	go func() { heartbeatDone <- s.heartbeatAgentSessionsOnce() }()
	select {
	case <-staleSessions.read:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not capture the starting record")
	}

	transitionDone := make(chan struct{})
	go func() {
		s.markControlPlaneAgentSessionRunning(ap)
		close(transitionDone)
	}()
	select {
	case <-transitionDone:
		t.Fatal("running transition completed before the in-flight heartbeat drained")
	case <-time.After(25 * time.Millisecond):
	}

	close(staleSessions.release)
	select {
	case <-heartbeatDone:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not finish after release")
	}
	select {
	case <-transitionDone:
	case <-time.After(time.Second):
		t.Fatal("running transition did not resume after heartbeat drain")
	}

	final, err := base.AgentSessions().Get(ctx, "WS", "session-starting-race")
	if err != nil {
		t.Fatalf("get running session: %v", err)
	}
	if final.Status != domain.AgentSessionRunning {
		t.Fatalf("final session status = %q, want running", final.Status)
	}
}

func TestHeartbeatAgentSessionsOnceBoundsManyDegradedAgents(t *testing.T) {
	const agentCount = 128
	controlStore, probe := newBlockingHeartbeatStore(t, agentCount*2)
	shutdown := make(chan struct{})
	t.Cleanup(func() { close(shutdown) })
	s := &Supervisor{
		ControlStore:                controlStore,
		WorkspaceID:                 "WS",
		Agents:                      heartbeatTestAgents(agentCount),
		Shutdown:                    shutdown,
		SessionHeartbeatPassTimeout: 250 * time.Millisecond,
	}
	s.RegisterTick(GoroutineSessionHeartbeat)
	before, ok := s.LoadTick(GoroutineSessionHeartbeat)
	if !ok {
		t.Fatal("session heartbeat tick was not registered")
	}

	startedAt := time.Now()
	resultCh := make(chan agentSessionHeartbeatResult, 1)
	go func() { resultCh <- s.heartbeatAgentSessionsOnce() }()
	for range agentSessionHeartbeatConcurrency {
		select {
		case <-probe.entered:
		case <-time.After(time.Second):
			t.Fatal("bounded heartbeat workers did not all start")
		}
	}
	select {
	case <-probe.entered:
		t.Fatalf("more than %d heartbeat calls started concurrently", agentSessionHeartbeatConcurrency)
	case <-time.After(20 * time.Millisecond):
	}

	var result agentSessionHeartbeatResult
	select {
	case result = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("heartbeat pass exceeded its bounded deadline")
	}
	if elapsed := time.Since(startedAt); elapsed >= time.Second {
		t.Fatalf("heartbeat pass elapsed = %v, want under 1s", elapsed)
	}
	if result.Sessions != 0 || result.Leases != 0 || result.Failures != agentCount*2 {
		t.Fatalf("heartbeat result = %+v, want %d stable failures", result, agentCount*2)
	}
	if got := probe.calls.Load(); got != agentSessionHeartbeatConcurrency {
		t.Fatalf("heartbeat calls = %d, want exactly bounded concurrency %d", got, agentSessionHeartbeatConcurrency)
	}
	if got := probe.maxActive.Load(); got != agentSessionHeartbeatConcurrency {
		t.Fatalf("maximum active heartbeat calls = %d, want %d", got, agentSessionHeartbeatConcurrency)
	}
	if got := probe.active.Load(); got != 0 {
		t.Fatalf("active heartbeat calls after pass = %d, want 0", got)
	}
	after, ok := s.LoadTick(GoroutineSessionHeartbeat)
	if !ok || !after.After(before) {
		t.Fatalf("liveness tick did not advance around bounded pass: before=%v after=%v ok=%v", before, after, ok)
	}
}

func TestHeartbeatPassDeadlineCancelsWhileLifecycleBarrierHeld(t *testing.T) {
	base := memstore.New()
	ap := &AgentProcess{
		Entry:          config.AgentEntry{Worktree: "agent-blocked"},
		AgentSessionID: "session-blocked",
	}
	ap.SessionHeartbeatMu.Lock()
	barrierHeld := true
	t.Cleanup(func() {
		if barrierHeld {
			ap.SessionHeartbeatMu.Unlock()
		}
	})
	s := &Supervisor{
		ControlStore: &heartbeatTestStore{
			Store:    base,
			sessions: base.AgentSessions(),
			leases:   base.AgentLeases(),
		},
		WorkspaceID:                 "WS",
		Agents:                      []*AgentProcess{ap},
		SessionHeartbeatPassTimeout: 25 * time.Millisecond,
	}

	startedAt := time.Now()
	resultCh := make(chan agentSessionHeartbeatResult, 1)
	go func() { resultCh <- s.heartbeatAgentSessionsOnce() }()
	var result agentSessionHeartbeatResult
	select {
	case result = <-resultCh:
	case <-time.After(250 * time.Millisecond):
		ap.SessionHeartbeatMu.Unlock()
		barrierHeld = false
		t.Fatal("heartbeat pass did not honor its deadline while the lifecycle barrier was held")
	}
	ap.SessionHeartbeatMu.Unlock()
	barrierHeld = false
	if elapsed := time.Since(startedAt); elapsed >= 250*time.Millisecond {
		t.Fatalf("heartbeat pass elapsed = %v, want under 250ms", elapsed)
	}
	if result != (agentSessionHeartbeatResult{Failures: 1}) {
		t.Fatalf("heartbeat result = %+v, want one bounded failure", result)
	}
}

func TestHeartbeatAgentSessionsOnceRotatesFairlyAcrossCappedPasses(t *testing.T) {
	const agentCount = 16
	base := memstore.New()
	slowSessions := &delayedAgentSessionStore{
		AgentSessionStore: base.AgentSessions(),
		delay:             60 * time.Millisecond,
		succeeded:         make(map[string]int),
	}
	s := &Supervisor{
		ControlStore: &heartbeatTestStore{
			Store:    base,
			sessions: slowSessions,
			leases:   base.AgentLeases(),
		},
		WorkspaceID:                 "WS",
		Agents:                      heartbeatSessionOnlyTestAgents(agentCount),
		Shutdown:                    make(chan struct{}),
		SessionHeartbeatPassTimeout: 100 * time.Millisecond,
	}

	first := s.heartbeatAgentSessionsOnce()
	if first.Sessions == 0 || first.Failures == 0 || first.Sessions+first.Failures != agentCount {
		t.Fatalf("first capped heartbeat result = %+v, want successes and failures totaling %d", first, agentCount)
	}
	if slowSessions.succeededFor("session-015") != 0 {
		t.Fatal("tail session unexpectedly succeeded in the first capped pass")
	}

	second := s.heartbeatAgentSessionsOnce()
	if second.Sessions == 0 || second.Failures == 0 || second.Sessions+second.Failures != agentCount {
		t.Fatalf("second capped heartbeat result = %+v, want successes and failures totaling %d", second, agentCount)
	}
	if got := slowSessions.succeededFor("session-015"); got != 1 {
		t.Fatalf("tail session successes after consecutive capped passes = %d, want 1", got)
	}
}

func TestRunAgentSessionHeartbeatShutdownCancelsActivePass(t *testing.T) {
	controlStore, probe := newBlockingHeartbeatStore(t, agentSessionHeartbeatConcurrency*2)
	shutdown := make(chan struct{})
	s := &Supervisor{
		ControlStore:                controlStore,
		WorkspaceID:                 "WS",
		Agents:                      heartbeatTestAgents(agentSessionHeartbeatConcurrency),
		Shutdown:                    shutdown,
		SessionHeartbeatPassTimeout: maxAgentSessionHeartbeatPassTimeout,
	}
	s.RegisterTick(GoroutineSessionHeartbeat)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runAgentSessionHeartbeat(time.Millisecond)
	}()
	for range agentSessionHeartbeatConcurrency {
		select {
		case <-probe.entered:
		case <-time.After(time.Second):
			close(shutdown)
			<-done
			t.Fatal("heartbeat pass did not start all bounded workers")
		}
	}

	closedAt := time.Now()
	close(shutdown)
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("heartbeat loop did not promptly honor supervisor shutdown")
	}
	if elapsed := time.Since(closedAt); elapsed >= 250*time.Millisecond {
		t.Fatalf("shutdown cancellation elapsed = %v, want under 250ms", elapsed)
	}
	if got := probe.active.Load(); got != 0 {
		t.Fatalf("active heartbeat calls after shutdown = %d, want 0", got)
	}
	if got := probe.calls.Load(); got != agentSessionHeartbeatConcurrency {
		t.Fatalf("heartbeat calls after shutdown = %d, want %d with no queued work started", got, agentSessionHeartbeatConcurrency)
	}
}

func TestAgentSessionHeartbeatPassTimeoutStaysBelowLivenessThreshold(t *testing.T) {
	s := &Supervisor{SessionHeartbeatPassTimeout: time.Hour}
	if got := s.agentSessionHeartbeatPassTimeout(); got != maxAgentSessionHeartbeatPassTimeout {
		t.Fatalf("pass timeout = %v, want cap %v", got, maxAgentSessionHeartbeatPassTimeout)
	}
	if pass, threshold := s.agentSessionHeartbeatPassTimeout(), s.thresholdFor(GoroutineSessionHeartbeat); pass*2 > threshold {
		t.Fatalf("pass timeout %v is not comfortably below liveness threshold %v", pass, threshold)
	}
}

func TestAgentSessionHeartbeatIntervalStaysBelowLivenessThreshold(t *testing.T) {
	tests := []struct {
		name            string
		livenessTimeout time.Duration
		want            time.Duration
	}{
		{name: "default threshold", want: time.Minute},
		{name: "configured threshold", livenessTimeout: 90 * time.Second, want: 45 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Supervisor{
				SessionHeartbeatInterval: 10 * time.Minute,
				LivenessTimeout:          tt.livenessTimeout,
			}
			if got := s.agentSessionHeartbeatInterval(); got != tt.want {
				t.Fatalf("heartbeat interval = %v, want %v", got, tt.want)
			}
			if interval, threshold := s.agentSessionHeartbeatInterval(), s.thresholdFor(GoroutineSessionHeartbeat); interval*2 > threshold {
				t.Fatalf("heartbeat interval %v is not comfortably below liveness threshold %v", interval, threshold)
			}
		})
	}
}

type heartbeatTestStore struct {
	store.Store
	sessions store.AgentSessionStore
	leases   store.AgentLeaseStore
}

func (s *heartbeatTestStore) AgentSessions() store.AgentSessionStore { return s.sessions }
func (s *heartbeatTestStore) AgentLeases() store.AgentLeaseStore     { return s.leases }

type blockingAgentSessionStore struct {
	store.AgentSessionStore
	probe *blockingHeartbeatProbe
}

// staleWriteAgentSessionStore models FleetDB Redis' non-CAS heartbeat: it
// captures the full running record before terminal completion, then writes the
// captured status after an independently controlled delay.
type staleWriteAgentSessionStore struct {
	store.AgentSessionStore
	read    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *staleWriteAgentSessionStore) Heartbeat(ctx context.Context, workspaceKey, sessionID string) (*domain.AgentSession, error) {
	captured, err := s.AgentSessionStore.Get(ctx, workspaceKey, sessionID)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() { close(s.read) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	now := time.Now().UTC()
	return s.AgentSessionStore.Update(ctx, workspaceKey, sessionID, store.AgentSessionUpdate{
		Status:        &captured.Status,
		LastHeartbeat: &now,
	})
}

func (s *blockingAgentSessionStore) Heartbeat(ctx context.Context, _, _ string) (*domain.AgentSession, error) {
	return nil, s.probe.wait(ctx)
}

type blockingAgentLeaseStore struct {
	store.AgentLeaseStore
	probe *blockingHeartbeatProbe
}

func (s *blockingAgentLeaseStore) Heartbeat(ctx context.Context, _, _, _ string, _ time.Duration) (*domain.AgentLease, error) {
	return nil, s.probe.wait(ctx)
}

type delayedAgentSessionStore struct {
	store.AgentSessionStore
	delay time.Duration

	mu        sync.Mutex
	succeeded map[string]int
}

func (s *delayedAgentSessionStore) Heartbeat(ctx context.Context, _, sessionID string) (*domain.AgentSession, error) {
	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		s.mu.Lock()
		s.succeeded[sessionID]++
		s.mu.Unlock()
		return &domain.AgentSession{SessionID: sessionID}, nil
	}
}

func (s *delayedAgentSessionStore) succeededFor(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.succeeded[sessionID]
}

type blockingHeartbeatProbe struct {
	active    atomic.Int32
	maxActive atomic.Int32
	calls     atomic.Int32
	entered   chan struct{}
}

func (p *blockingHeartbeatProbe) wait(ctx context.Context) error {
	p.calls.Add(1)
	active := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		maximum := p.maxActive.Load()
		if active <= maximum || p.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	p.entered <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func newBlockingHeartbeatStore(t *testing.T, enterCapacity int) (store.Store, *blockingHeartbeatProbe) {
	t.Helper()
	base := memstore.New()
	probe := &blockingHeartbeatProbe{entered: make(chan struct{}, enterCapacity)}
	return &heartbeatTestStore{
		Store:    base,
		sessions: &blockingAgentSessionStore{AgentSessionStore: base.AgentSessions(), probe: probe},
		leases:   &blockingAgentLeaseStore{AgentLeaseStore: base.AgentLeases(), probe: probe},
	}, probe
}

func heartbeatTestAgents(count int) []*AgentProcess {
	agents := make([]*AgentProcess, 0, count)
	for index := range count {
		suffix := fmt.Sprintf("%03d", index)
		agents = append(agents, &AgentProcess{
			Entry:           config.AgentEntry{Worktree: "agent-" + suffix},
			AgentSessionID:  "session-" + suffix,
			AgentLeaseID:    "lease-" + suffix,
			AgentLeaseToken: "token-" + suffix,
		})
	}
	return agents
}

func heartbeatSessionOnlyTestAgents(count int) []*AgentProcess {
	agents := heartbeatTestAgents(count)
	for _, agent := range agents {
		agent.AgentLeaseID = ""
		agent.AgentLeaseToken = ""
	}
	return agents
}
