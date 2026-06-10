package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// livenessScanInterval is how often the watchdog scans tick stamps.
const livenessScanInterval = 10 * time.Second

// minLivenessThreshold is the floor for any per-goroutine staleness threshold.
const minLivenessThreshold = 60 * time.Second

// agentLivenessSlack is added to (no_work_backoff + spawn_timeout) when
// computing the per-agent threshold.
const agentLivenessSlack = 30 * time.Second

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
// fatal on the first staleness it finds.
func (s *Supervisor) scanTicks(now time.Time) {
	var stale []string

	s.RangeTicks(func(name string, t time.Time) {
		threshold := s.thresholdFor(name)
		age := now.Sub(t)
		if age > threshold {
			stale = append(stale, fmt.Sprintf("%s (age=%s, threshold=%s)",
				name, age.Truncate(time.Second), threshold))
		}
	})

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
	case GoroutineHealthChecker, GoroutineConfigReconciler, GoroutineNodeHeartbeat, GoroutineWorkflowRunner:
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
