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
// row. A tick that recovers has its streak reset. A stale ClassCore tick (a
// daemon-lifetime singleton) is fatal after the streak threshold; a stale
// ClassAgent tick is quarantined so one wedged agent cannot crash the daemon.
// If the scan-to-scan gap exceeds livenessFreezeGap the process was suspended,
// not misbehaving, so all streaks are cleared and the scan is skipped.
func (s *Supervisor) scanTicks(now time.Time) {
	if s.shouldSkipLivenessScan(now) {
		return
	}

	var fatal []string
	var quarantine []*tickSlot
	staleNow := make(map[string]struct{})

	s.rangeSlots(func(sl *tickSlot) {
		threshold := s.thresholdFor(sl.name)
		age := now.Sub(sl.last())
		if age <= threshold {
			return
		}
		staleNow[sl.name] = struct{}{}
		if s.livenessStreak == nil {
			s.livenessStreak = make(map[string]int)
		}
		s.livenessStreak[sl.name]++
		if s.livenessStreak[sl.name] < livenessStaleScansBeforeFatal {
			return
		}
		if sl.class == ClassAgent {
			quarantine = append(quarantine, sl)
			return
		}
		fatal = append(fatal, fmt.Sprintf("%s (age=%s, threshold=%s, consecutive_scans=%d)",
			sl.name, age.Truncate(time.Second), threshold, s.livenessStreak[sl.name]))
	})

	for name := range s.livenessStreak {
		if _, ok := staleNow[name]; !ok {
			delete(s.livenessStreak, name)
		}
	}

	for _, sl := range quarantine {
		if sl.onStale != nil {
			sl.onStale()
		}
	}

	if len(fatal) == 0 {
		return
	}

	reason := "liveness watchdog: " + strings.Join(fatal, ", ")
	slog.Error("supervisor liveness check failed", "stale", fatal)
	DumpGoroutinesToLog(reason)
	s.SignalFatal(GoroutineLivenessWatchdog, fmt.Errorf("%s", reason))
}

func (s *Supervisor) shouldSkipLivenessScan(now time.Time) bool {
	// Process-suspension guard: a watchdog that did not run for far longer than
	// its interval means the whole process was frozen. Every tick then looks
	// ancient regardless of goroutine health — skip the fatal and reset streaks.
	prev := s.lastLivenessScan
	s.lastLivenessScan = now
	if !prev.IsZero() && now.Sub(prev) > livenessFreezeGap {
		slog.Warn("supervisor liveness check skipped: process suspension detected",
			"gap", now.Sub(prev).Truncate(time.Second))
		s.livenessStreak = nil
		return true
	}
	return false
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
	case GoroutineHealthChecker, GoroutineConfigReconciler, GoroutineNodeHeartbeat:
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
				// Record by slot identity, not by name: if this agent's worktree
				// was re-added, RecordTick(name) would refresh the successor's
				// slot. ap.recordTick only ever refreshes this goroutine's slot.
				ap.recordTick()
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
