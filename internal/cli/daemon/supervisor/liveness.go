package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
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

// livenessFreezeGap is the scan-to-scan MONOTONIC gap above which the watchdog
// concludes the whole process was suspended (swap thrash, SIGSTOP/debugger)
// rather than that any goroutine misbehaved. The watchdog scans every
// livenessScanInterval (10s), so a gap this large cannot occur under normal
// scheduling. On such a gap every tick looks ancient through no fault of its
// goroutine, so the watchdog re-primes all ticks and skips the fatal.
//
// It is monotonic, not wall-clock, and that is why it alone is not enough: see
// livenessClockSkewSlack.
const livenessFreezeGap = 30 * time.Second

// livenessClockSkewSlack is how far the wall-clock and monotonic scan gaps may
// diverge before the watchdog concludes the process was suspended or the system
// clock stepped. Normal scheduling jitter between the two domains is
// milliseconds; system sleep produces minutes of divergence, because on darwin
// the monotonic clock stops while the machine is asleep while the wall clock
// keeps running.
//
// This divergence is the whole bug this constant exists for: after a wake the
// monotonic gap reads a healthy ~10s (so livenessFreezeGap never fires) while
// every tick age — measured against a time.Unix value with no monotonic
// reading, so Sub falls back to wall clock — includes the entire sleep. Without
// this check the daemon fataled within seconds of every wake.
const livenessClockSkewSlack = 5 * time.Second

// livenessMinStaleSpan is the minimum real (monotonic) time a tick must be
// continuously observed stale before the watchdog may signal fatal, regardless
// of how many scans that took. Under macOS DarkWake the process runs in ~2s
// bursts, so a scan count can accumulate across several wake windows without
// meaningful runtime passing; the count alone is then not the grace period it
// looks like. In production time.Ticker never fires early, so N scans always
// span at least (N-1) intervals and this check is satisfied exactly when the
// count is.
const livenessMinStaleSpan = (livenessStaleScansBeforeFatal - 1) * livenessScanInterval

// livenessWatchdog runs until shutdown, scanning every registered tick stamp on
// livenessScanInterval. When a tick stays older than its per-name threshold for
// long enough (see scanTicks), it logs a throttled goroutine dump and routes a
// fatal error so the daemon exits non-zero and pm2 restarts it — the recovery
// path, since a goroutine wedged in a syscall or on a mutex cannot be restarted
// in place. Once it has signaled fatal it stops scanning. The watchdog itself
// reads atomic ticks only — it never takes AgentsMu — so it stays responsive
// even when the supervisor is deadlocked.
func (s *Supervisor) livenessWatchdog() {
	s.livenessWatchdogEvery(livenessScanInterval)
}

// livenessWatchdogEvery is livenessWatchdog with an explicit scan interval,
// allowing tests to drive the watchdog loop without real-time waits.
func (s *Supervisor) livenessWatchdogEvery(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.Shutdown:
			return
		case <-ticker.C:
			s.RecordTick(GoroutineLivenessWatchdog)
			s.scanTicks(time.Now())
			if s.livenessFatalSignaled {
				// The fatal has been routed and the daemon is already draining
				// toward exit. Scanning on would re-flag the same ticks every
				// interval for the whole (up to ~40s) drain window, each time
				// logging an Error and asking for a goroutine dump.
				//
				// We cannot just return: RunCritical treats a return with
				// s.Shutdown still open as its own fatal ("returned without
				// shutdown"), which FatalOnce swallows but still logs as a
				// misleading Error. So go quiet and wait for the shutdown this
				// fatal is about to cause, then return the way RunCritical
				// expects.
				<-s.Shutdown
				return
			}
		}
	}
}

// scanTicks evaluates every registered tick against its threshold and signals
// fatal once a tick has been stale for livenessStaleScansBeforeFatal scans in a
// row AND that streak has spanned livenessMinStaleSpan of real runtime. A tick
// that recovers (fresh again this scan) has its streak reset, so only sustained
// staleness — a genuinely wedged goroutine — crashes the daemon; transient
// stalls are ridden out.
//
// If the scan-to-scan gap says the process was suspended — in either clock
// domain, see suspendedScanGap — the ticks are stale through no fault of their
// goroutines, so all streaks are cleared, every tick is re-primed, and the scan
// is skipped.
func (s *Supervisor) scanTicks(now time.Time) {
	if s.recordScanGap(now) {
		return
	}

	stale := s.collectStaleTicks(now)
	if len(stale) == 0 {
		return
	}

	reason := "liveness watchdog: " + strings.Join(stale, ", ")
	slog.Error("supervisor liveness check failed", "stale", stale)
	DumpGoroutinesToLogThrottled(reason)
	s.SignalFatal(GoroutineLivenessWatchdog, fmt.Errorf("%s", reason))
	s.livenessFatalSignaled = true
}

// recordScanGap advances both scan stamps and reports whether this scan must be
// skipped because the process was suspended or the clock stepped.
//
// Both gaps are needed: the monotonic one catches a frozen process whose
// monotonic clock kept running, and the mono/wall divergence catches system
// sleep (which stops darwin's monotonic clock) and clock steps. now.Round(0)
// strips the monotonic reading, forcing a wall-clock comparison — that is the
// crux of the false-fatal-after-wake fix.
//
// On a skip it also clears the streaks and re-primes every tick, because after
// a thaw the ages stay huge and clearing streaks alone only defers the fatal by
// three scans.
func (s *Supervisor) recordScanGap(now time.Time) bool {
	prevMono, prevWall := s.lastLivenessScan, s.lastLivenessScanWall
	s.lastLivenessScan, s.lastLivenessScanWall = now, now.Round(0)
	if prevMono.IsZero() {
		return false
	}

	monoGap := now.Sub(prevMono)
	wallGap := monoGap
	if !prevWall.IsZero() {
		wallGap = now.Round(0).Sub(prevWall)
	}
	reason, ok := suspendedScanGap(monoGap, wallGap)
	if !ok {
		return false
	}

	slog.Warn("supervisor liveness check skipped: process suspension detected",
		"reason", reason,
		"mono_gap", monoGap.Truncate(time.Second),
		"wall_gap", wallGap.Truncate(time.Second))
	s.livenessStreak = nil
	s.livenessStreakStart = nil
	s.primeAllTicks(now)
	return true
}

// collectStaleTicks advances each tick's stale streak and returns a description
// for every tick that has now been stale long enough — in both scan count and
// real runtime — to justify a fatal. Streaks for ticks that recovered this scan
// are dropped, so a goroutine that briefly stalled then resumed starts fresh.
func (s *Supervisor) collectStaleTicks(now time.Time) []string {
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
		if s.livenessStreakStart == nil {
			s.livenessStreakStart = make(map[string]time.Time)
		}
		s.livenessStreak[name]++
		if s.livenessStreak[name] == 1 {
			s.livenessStreakStart[name] = now
			// Early operator signal, without the 1 MiB dump the fatal carries.
			slog.Warn("supervisor goroutine tick stale",
				"goroutine", name,
				"age", age.Truncate(time.Second),
				"threshold", threshold)
		}
		// now and the streak start share a time.Now() lineage, so this Sub uses
		// the monotonic clock and measures real runtime, not wall time.
		if s.livenessStreak[name] >= livenessStaleScansBeforeFatal &&
			now.Sub(s.livenessStreakStart[name]) >= livenessMinStaleSpan {
			stale = append(stale, fmt.Sprintf("%s (age=%s, threshold=%s, consecutive_scans=%d)",
				name, age.Truncate(time.Second), threshold, s.livenessStreak[name]))
		}
	})

	for name := range s.livenessStreak {
		if _, ok := staleNow[name]; !ok {
			delete(s.livenessStreak, name)
			delete(s.livenessStreakStart, name)
		}
	}
	return stale
}

// suspendedScanGap decides, from one scan-to-scan gap measured in each clock
// domain, whether the process was suspended or the system clock stepped — in
// which case tick ages this scan are meaningless and must not be trusted. It
// returns a human-readable reason alongside the verdict.
//
// Both branches are live on every platform, deliberately and without build
// tags: darwin suspends its monotonic clock across system sleep (so only the
// wall/mono divergence sees it), while some Linux configurations count suspend
// time in CLOCK_MONOTONIC (so only the monotonic gap does).
func suspendedScanGap(monoGap, wallGap time.Duration) (string, bool) {
	switch {
	case monoGap > livenessFreezeGap:
		return "process stalled", true
	case wallGap-monoGap > livenessClockSkewSlack:
		// Wall time advanced far more than runtime: the machine slept, or the
		// clock stepped forward.
		return "system sleep or forward clock step", true
	case wallGap < 0 || monoGap-wallGap > livenessClockSkewSlack:
		// Wall time went backwards relative to runtime: ages derived from the
		// wall clock are garbage this scan.
		return "backward clock step", true
	}
	return "", false
}

// primeAllTicks stamps every registered tick to now. It is called only after a
// detected suspension: once the machine thaws, wall-clock tick ages include the
// whole sleep even though no goroutine misbehaved, and clearing streaks alone
// does not help because the ages stay huge until each loop reaches its next
// cadence — which for the slower loops is well past the three scans it takes to
// go fatal. Re-priming grants every loop a fresh full threshold to prove
// liveness; a goroutine that is genuinely wedged is flagged again in the next
// threshold window.
//
// Racing RecordTick is harmless: both are atomic stores of a UnixNano value and
// either value is fresh.
func (s *Supervisor) primeAllTicks(now time.Time) {
	stamp := now.UnixNano()
	s.Ticks.Range(func(_, v any) bool {
		if tick, ok := v.(*atomic.Int64); ok {
			tick.Store(stamp)
		}
		return true
	})
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
