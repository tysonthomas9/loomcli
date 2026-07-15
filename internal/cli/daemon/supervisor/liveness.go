package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// livenessScanInterval is how often the watchdog scans tick stamps.
const livenessScanInterval = 10 * time.Second

// minLivenessThreshold is the floor for any per-goroutine staleness threshold.
const minLivenessThreshold = 60 * time.Second

// agentLivenessSlack is added to (no_work_backoff + spawn_timeout) when
// computing the per-agent threshold.
const agentLivenessSlack = 30 * time.Second

// livenessStaleScansBeforeFatal is how many consecutive scans a tick must be
// observed stale before the watchdog signals fatal. At livenessScanInterval
// (10s) this adds ~20s of grace beyond the per-name threshold, so a single
// transient stall (one slow control-plane cycle, a brief lock contention, a GC
// blip) is tolerated while a genuinely wedged goroutine — stale every scan — is
// still caught within ~threshold+20s.
const livenessStaleScansBeforeFatal = 3

// livenessFreezeGap is the scan-to-scan wall-clock gap above which the watchdog
// concludes the whole process was suspended (laptop sleep, swap thrash,
// SIGSTOP/debugger) rather than that any goroutine misbehaved. The watchdog
// scans every livenessScanInterval (10s), so a gap this large cannot occur
// under normal scheduling. On such a gap every tick looks ancient through no
// fault of its goroutine, so the watchdog clears all streaks and skips the
// fatal for that scan, giving goroutines a cadence to refresh after thaw.
const livenessFreezeGap = 30 * time.Second

// livenessWatchdog runs forever, scanning every registered tick stamp on
// livenessScanInterval. When any tick is older than its per-name threshold,
// it logs a full goroutine dump and routes a fatal error so the daemon exits
// non-zero. The watchdog itself reads atomic ticks only — it never takes
// AgentsMu — so it stays responsive even when the supervisor is deadlocked.
func (s *Supervisor) livenessWatchdog() {
	ticker := time.NewTicker(livenessScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.Shutdown:
			return
		case <-ticker.C:
			s.RecordTick(GoroutineLivenessWatchdog)
			s.scanTicks(time.Now())
		}
	}
}

// scanTicks evaluates every registered tick against its threshold and signals
// fatal once a tick has been stale for livenessStaleScansBeforeFatal scans in a
// row. A tick that recovers (fresh again this scan) has its streak reset, so
// only sustained staleness — a genuinely wedged goroutine — crashes the daemon;
// transient stalls are ridden out. If the scan-to-scan gap exceeds
// livenessFreezeGap the process was suspended, not misbehaving, so all streaks
// are cleared and the scan is skipped.
func (s *Supervisor) scanTicks(now time.Time) {
	// Process-suspension guard: a watchdog that did not run for far longer than
	// its interval means the whole process was frozen. Every tick then looks
	// ancient regardless of goroutine health — skip the fatal and reset streaks.
	prev := s.lastLivenessScan
	s.lastLivenessScan = now
	if !prev.IsZero() && now.Sub(prev) > livenessFreezeGap {
		slog.Warn("supervisor liveness check skipped: process suspension detected",
			"gap", now.Sub(prev).Truncate(time.Second))
		s.livenessStreak = nil
		return
	}

	var stale []string
	staleNow := make(map[string]struct{})

	s.RangeTicks(func(name string, t time.Time) {
		threshold := s.thresholdFor(name)
		age := now.Sub(t)
		if age <= threshold {
			return
		}
		staleNow[name] = struct{}{}
		if s.livenessStreak == nil {
			s.livenessStreak = make(map[string]int)
		}
		s.livenessStreak[name]++
		if s.livenessStreak[name] >= livenessStaleScansBeforeFatal {
			stale = append(stale, fmt.Sprintf("%s (age=%s, threshold=%s, consecutive_scans=%d)",
				name, age.Truncate(time.Second), threshold, s.livenessStreak[name]))
		}
	})

	// Reset the streak for any tick that was stale before but recovered this
	// scan, so a goroutine that briefly stalled then resumed starts fresh.
	for name := range s.livenessStreak {
		if _, ok := staleNow[name]; !ok {
			delete(s.livenessStreak, name)
		}
	}

	if len(stale) == 0 {
		return
	}

	reason := "liveness watchdog: " + strings.Join(stale, ", ")
	slog.Error("supervisor liveness check failed", "stale", stale)
	DumpGoroutinesToLog(reason)
	s.SignalFatal(GoroutineLivenessWatchdog, fmt.Errorf("%s", reason))
}

// thresholdFor returns the staleness threshold for the named goroutine.
func (s *Supervisor) thresholdFor(name string) time.Duration {
	if s.LivenessTimeout > 0 {
		if s.LivenessTimeout < minLivenessThreshold {
			return minLivenessThreshold
		}
		return s.LivenessTimeout
	}

	if strings.HasPrefix(name, GoroutineAgentPrefix) {
		return agentThreshold(s)
	}

	switch name {
	case GoroutineHealthChecker, GoroutineConfigReconciler, GoroutineNodeHeartbeat, GoroutineSessionHeartbeat:
		return 2 * time.Minute
	case GoroutineStateUpdater:
		return minLivenessThreshold
	case GoroutineLivenessWatchdog:
		return minLivenessThreshold
	default:
		return minLivenessThreshold
	}
}

// agentThreshold computes the per-agent superviseAgent staleness threshold.
//
// startAgentWaitHeartbeat keeps the tick fresh for the (unbounded) duration of
// cmd.Wait(), so this threshold only has to cover the non-waiting parts of the
// supervise loop — spawn, pre-flight setup, post-exit cleanup and the no-work /
// restart backoff. The formula budgets two no-work backoffs plus a spawn budget
// so a healthy but momentarily idle agent is never mistaken for a wedged
// supervise goroutine.
func agentThreshold(s *Supervisor) time.Duration {
	noWork := 30 * time.Second
	if s.ConfigSnapshot != nil {
		cfg := s.ConfigSnapshot()
		if cfg != nil && cfg.Daemon.RestartPolicy.NoWorkBackoff != nil && *cfg.Daemon.RestartPolicy.NoWorkBackoff > 0 {
			noWork = time.Duration(*cfg.Daemon.RestartPolicy.NoWorkBackoff) * time.Second
		}
	}
	spawnBudget := 5 * time.Minute
	threshold := 2*(noWork+spawnBudget) + agentLivenessSlack
	if threshold < minLivenessThreshold {
		threshold = minLivenessThreshold
	}
	return threshold
}

// agentTickName returns the liveness tick name for an agent's supervise goroutine.
func agentTickName(ap *AgentProcess) string {
	return GoroutineAgentPrefix + ap.Entry.Worktree
}

// agentWaitHeartbeatInterval is how often waitForAgent refreshes an agent's
// supervise tick while blocked in cmd.Wait(). It must stay comfortably below
// both livenessScanInterval and the agent staleness threshold so a scan never
// observes a stale tick for a healthy, running agent.
const agentWaitHeartbeatInterval = 15 * time.Second

// startAgentWaitHeartbeat refreshes the agent's supervise tick on an interval
// for as long as the supervise goroutine is blocked in cmd.Wait(). It returns a
// stop function that signals the heartbeat goroutine and blocks until it exits.
//
// The tick recorded at the top of superviseAgent would otherwise stay frozen
// for the entire, unbounded lifetime of a running agent, so the liveness
// watchdog would eventually mistake a healthy long-running agent for a wedged
// supervise goroutine and crash the whole daemon. A genuinely silent or hung
// agent is handled independently by the output-timeout watchdog (checkWatchdog),
// which kills the process and unblocks cmd.Wait().
func (s *Supervisor) startAgentWaitHeartbeat(ap *AgentProcess) func() {
	return s.startAgentWaitHeartbeatEvery(ap, agentWaitHeartbeatInterval)
}

// startAgentWaitHeartbeatEvery is startAgentWaitHeartbeat with an explicit
// interval, allowing tests to drive the heartbeat without real-time waits.
func (s *Supervisor) startAgentWaitHeartbeatEvery(ap *AgentProcess, interval time.Duration) func() {
	tickName := agentTickName(ap)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-s.Shutdown:
				return
			case <-ticker.C:
				s.RecordTick(tickName)
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// workerHeartbeatInterval is how often the supervisor renews a running agent's
// fleet-db worker-registration lease. It must stay well below the server-side
// worker TTL (default 90s) so a live agent is never reaped; 30s gives a 3x
// margin and matches the node-heartbeat cadence. It is a var (not const) so the
// waitForAgent wiring test can drive it without a real-time wait; production
// never reassigns it.
var workerHeartbeatInterval = 30 * time.Second

// startWorkerHeartbeat renews the agent's fleet-db worker-registration lease
// for as long as the supervise goroutine is blocked in cmd.Wait(). The worker
// is keyed by the claim actor (ap.Entry.Worktree, the same identity the
// supervisor claims/releases issues under). It returns a stop function that
// signals the goroutine and blocks until it exits.
//
// Renewal is driven purely by process liveness — a plain ticker that runs only
// while the PID is alive — NOT by agent output. A healthy agent that is quietly
// thinking or compiling emits no output but is alive, so it keeps its lease and
// does not flicker off the board. The server TTL + sweeper remain the backstop
// for non-graceful death.
func (s *Supervisor) startWorkerHeartbeat(ap *AgentProcess) func() {
	return s.startWorkerHeartbeatEvery(ap, workerHeartbeatInterval)
}

// startWorkerHeartbeatEvery is startWorkerHeartbeat with an explicit interval,
// allowing tests to drive the heartbeat without real-time waits. It is a no-op
// (returns a no-op stop) when the control plane is not wired.
func (s *Supervisor) startWorkerHeartbeatEvery(ap *AgentProcess, interval time.Duration) func() {
	if s.ControlStore == nil || s.WorkspaceID == "" || ap.Entry.Worktree == "" {
		return func() {}
	}
	workerID := ap.Entry.Worktree
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-s.Shutdown:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
				err := s.ControlStore.Workers().Heartbeat(ctx, s.WorkspaceID, workerID)
				cancel()
				if err != nil {
					slog.Debug("supervisor worker heartbeat failed",
						"workspace", s.WorkspaceID, "worker_id", workerID, "err", err)
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

type agentSessionHeartbeatResult struct {
	Sessions int
	Leases   int
	Failures int
}

const (
	// agentSessionHeartbeatConcurrency bounds FleetDB pressure independently of
	// the number of configured agents. Session and lease renewals are separate
	// jobs so one degraded record family cannot serialize the other.
	agentSessionHeartbeatConcurrency = 8
	// maxAgentSessionHeartbeatPassTimeout stays at half the minimum configurable
	// liveness threshold. A pass therefore returns and refreshes its loop tick
	// before the watchdog can mistake a large or unreachable backend for a
	// wedged critical goroutine.
	maxAgentSessionHeartbeatPassTimeout     = 30 * time.Second
	defaultAgentSessionHeartbeatPassTimeout = maxAgentSessionHeartbeatPassTimeout
)

type agentSessionHeartbeatJobKind uint8

const (
	agentSessionHeartbeatSession agentSessionHeartbeatJobKind = iota
	agentSessionHeartbeatLease
)

type agentSessionHeartbeatJob struct {
	kind       agentSessionHeartbeatJobKind
	agent      *AgentProcess
	agentName  string
	sessionID  string
	leaseID    string
	leaseToken string
}

type agentSessionHeartbeatOutcome struct {
	attempted bool
	err       error
}

// startAgentSessionHeartbeat registers one supervisor-owned loop that keeps
// every active control-plane AgentSession and AgentLease alive independently of
// daemon IPC traffic. Long-running or temporarily idle agents therefore retain
// their fencing lease even when they have no mutation to send.
func (s *Supervisor) startAgentSessionHeartbeat() {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return
	}
	s.RegisterTick(GoroutineSessionHeartbeat)
	s.RunCritical(GoroutineSessionHeartbeat, func() {
		s.runAgentSessionHeartbeat(s.agentSessionHeartbeatInterval())
	})
}

func (s *Supervisor) runAgentSessionHeartbeat(interval time.Duration) {
	shutdownCtx, releaseShutdownCtx := contextUntilShutdown(s.Shutdown)
	defer releaseShutdownCtx()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.Shutdown:
			return
		case <-ticker.C:
			if shutdownCtx.Err() != nil {
				return
			}
			result := s.heartbeatAgentSessionsOnceContext(shutdownCtx)
			if shutdownCtx.Err() != nil {
				return
			}
			if result.Failures > 0 {
				slog.Warn("agent control-plane heartbeat pass had failures",
					"sessions", result.Sessions, "leases", result.Leases, "failures", result.Failures)
			}
		}
	}
}

func (s *Supervisor) heartbeatAgentSessionsOnce() agentSessionHeartbeatResult {
	shutdownCtx, releaseShutdownCtx := contextUntilShutdown(s.Shutdown)
	defer releaseShutdownCtx()
	return s.heartbeatAgentSessionsOnceContext(shutdownCtx)
}

func (s *Supervisor) heartbeatAgentSessionsOnceContext(parent context.Context) agentSessionHeartbeatResult {
	s.RecordTick(GoroutineSessionHeartbeat)
	defer s.RecordTick(GoroutineSessionHeartbeat)

	if s.ControlStore == nil || s.WorkspaceID == "" {
		return agentSessionHeartbeatResult{}
	}
	if parent == nil {
		parent = context.Background()
	}
	passCtx, cancelPass := context.WithTimeout(parent, s.agentSessionHeartbeatPassTimeout())
	defer cancelPass()

	ttl := s.AgentLeaseTTL
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	jobs := s.snapshotAgentSessionHeartbeatJobs()
	if len(jobs) == 0 {
		return agentSessionHeartbeatResult{}
	}
	outcomes := s.runAgentSessionHeartbeatJobs(passCtx, jobs, ttl)
	return summarizeAgentSessionHeartbeat(jobs, outcomes)
}

func (s *Supervisor) runAgentSessionHeartbeatJobs(
	ctx context.Context,
	jobs []agentSessionHeartbeatJob,
	ttl time.Duration,
) []agentSessionHeartbeatOutcome {
	outcomes := make([]agentSessionHeartbeatOutcome, len(jobs))
	work := make(chan int, len(jobs))
	firstJob := s.reserveAgentSessionHeartbeatWindow(len(jobs))
	for offset := range jobs {
		work <- (firstJob + offset) % len(jobs)
	}
	close(work)

	workerCount := agentSessionHeartbeatConcurrency
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case index, ok := <-work:
					if !ok {
						return
					}
					outcomes[index] = s.runAgentSessionHeartbeatJob(ctx, jobs[index], ttl)
				}
			}
		}()
	}
	workers.Wait()
	return outcomes
}

func summarizeAgentSessionHeartbeat(
	jobs []agentSessionHeartbeatJob,
	outcomes []agentSessionHeartbeatOutcome,
) agentSessionHeartbeatResult {
	result := agentSessionHeartbeatResult{}
	// Aggregate only after every worker exits. Each outcome has one writer, so
	// the counts are deterministic and race-free. Eligible but unattempted jobs
	// (because the pass or shutdown deadline fired) count as failures, preserving
	// successes+failures == jobs regardless of scheduling.
	for index, job := range jobs {
		outcome := outcomes[index]
		if !outcome.attempted || outcome.err != nil {
			result.Failures++
			continue
		}
		switch job.kind {
		case agentSessionHeartbeatSession:
			result.Sessions++
		case agentSessionHeartbeatLease:
			result.Leases++
		}
	}
	return result
}

func (s *Supervisor) snapshotAgentSessionHeartbeatJobs() []agentSessionHeartbeatJob {
	s.AgentsMu.RLock()
	agents := append([]*AgentProcess(nil), s.Agents...)
	s.AgentsMu.RUnlock()

	jobs := make([]agentSessionHeartbeatJob, 0, len(agents)*2)
	for _, ap := range agents {
		if ap == nil {
			continue
		}
		ap.Mu.Lock()
		sessionID := ap.AgentSessionID
		leaseID := ap.AgentLeaseID
		leaseToken := ap.AgentLeaseToken
		agentName := ap.Entry.Worktree
		ap.Mu.Unlock()

		if sessionID != "" {
			jobs = append(jobs, agentSessionHeartbeatJob{
				kind:      agentSessionHeartbeatSession,
				agent:     ap,
				agentName: agentName,
				sessionID: sessionID,
			})
		}
		if leaseID != "" && leaseToken != "" {
			jobs = append(jobs, agentSessionHeartbeatJob{
				kind:       agentSessionHeartbeatLease,
				agent:      ap,
				agentName:  agentName,
				leaseID:    leaseID,
				leaseToken: leaseToken,
			})
		}
	}
	return jobs
}

func (s *Supervisor) runAgentSessionHeartbeatJob(ctx context.Context, job agentSessionHeartbeatJob, ttl time.Duration) agentSessionHeartbeatOutcome {
	if ctx.Err() != nil {
		return agentSessionHeartbeatOutcome{}
	}
	if job.agent != nil {
		job.agent.SessionHeartbeatMu.RLock()
		defer job.agent.SessionHeartbeatMu.RUnlock()
		if !agentSessionHeartbeatJobIsCurrent(job) {
			// The session was finalized after this pass took its snapshot. Treat
			// retirement as a successful no-op: it no longer needs renewal.
			return agentSessionHeartbeatOutcome{attempted: true}
		}
	}
	s.RecordTick(GoroutineSessionHeartbeat)
	operationCtx, cancelOperation := context.WithTimeout(ctx, controlPlaneOperationTimeout)
	defer cancelOperation()

	var err error
	switch job.kind {
	case agentSessionHeartbeatSession:
		_, err = s.ControlStore.AgentSessions().Heartbeat(operationCtx, s.WorkspaceID, job.sessionID)
		if err != nil {
			slog.Debug("agent session heartbeat failed", "agent", job.agentName, "session_id", job.sessionID, "err", err)
		}
	case agentSessionHeartbeatLease:
		_, err = s.ControlStore.AgentLeases().Heartbeat(operationCtx, s.WorkspaceID, job.leaseID, job.leaseToken, ttl)
		if err != nil {
			slog.Debug("agent lease heartbeat failed", "agent", job.agentName, "lease_id", job.leaseID, "err", err)
		}
	default:
		return agentSessionHeartbeatOutcome{}
	}
	s.RecordTick(GoroutineSessionHeartbeat)
	return agentSessionHeartbeatOutcome{attempted: true, err: err}
}

func agentSessionHeartbeatJobIsCurrent(job agentSessionHeartbeatJob) bool {
	job.agent.Mu.Lock()
	defer job.agent.Mu.Unlock()
	switch job.kind {
	case agentSessionHeartbeatSession:
		return job.agent.AgentSessionID == job.sessionID
	case agentSessionHeartbeatLease:
		return job.agent.AgentLeaseID == job.leaseID && job.agent.AgentLeaseToken == job.leaseToken
	default:
		return false
	}
}

func (s *Supervisor) agentSessionHeartbeatPassTimeout() time.Duration {
	timeout := s.SessionHeartbeatPassTimeout
	if timeout <= 0 {
		return defaultAgentSessionHeartbeatPassTimeout
	}
	if timeout > maxAgentSessionHeartbeatPassTimeout {
		return maxAgentSessionHeartbeatPassTimeout
	}
	return timeout
}

func (s *Supervisor) agentSessionHeartbeatInterval() time.Duration {
	interval := s.SessionHeartbeatInterval
	if interval <= 0 {
		interval = defaultSessionHeartbeatInterval
	}
	// RegisterTick happens before the ticker starts. Keep the first and every
	// later cadence at most halfway to the effective watchdog threshold so an
	// explicit oversized interval cannot make the loop stale while it waits.
	maximum := s.thresholdFor(GoroutineSessionHeartbeat) / 2
	if interval > maximum {
		return maximum
	}
	return interval
}

// reserveAgentSessionHeartbeatWindow returns the first job for this pass and
// advances the shared cursor by one fixed worker window. Even when every pass
// expires after its initial calls, consecutive passes therefore cover the
// entire stable job list instead of repeatedly favoring index zero.
func (s *Supervisor) reserveAgentSessionHeartbeatWindow(jobCount int) int {
	if jobCount <= 0 {
		return 0
	}
	window := agentSessionHeartbeatConcurrency
	if window > jobCount {
		window = jobCount
	}
	s.sessionHeartbeatCursorMu.Lock()
	defer s.sessionHeartbeatCursorMu.Unlock()
	first := s.sessionHeartbeatCursor % jobCount
	s.sessionHeartbeatCursor = (first + window) % jobCount
	return first
}

// contextUntilShutdown converts the supervisor's close-only shutdown signal
// into a context parent for FleetDB calls. The release function cancels and
// joins the bridge goroutine, so a completed pass cannot leak it.
func contextUntilShutdown(shutdown <-chan struct{}) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	if shutdown == nil {
		return ctx, cancel
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		cancel()
		<-done
	}
}
