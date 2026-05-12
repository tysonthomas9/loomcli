package supervisor

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// livenessScanInterval is how often the watchdog scans tick stamps. Short
// enough that staleness is caught within ~10s of the threshold, long enough
// that the scan itself is negligible overhead.
const livenessScanInterval = 10 * time.Second

// minLivenessThreshold is the floor for any per-goroutine staleness threshold.
// Even fast-cadence loops (5s state updater) need headroom for system pauses
// (GC, fs latency) before declaring them stuck.
const minLivenessThreshold = 60 * time.Second

// agentLivenessSlack is added to (no_work_backoff + spawn_timeout) when
// computing the per-agent threshold. Covers worst-case spawn latency on slow
// systems (heavy git clone, large dependency install) without flapping fatal.
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
// fatal on the first staleness it finds. Extracted from livenessWatchdog so
// unit tests can drive it with a synthetic "now" without spinning the ticker.
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
// Cadence loops use 4× their ticker; per-agent loops cover one full
// no_work_backoff + spawn budget cycle plus slack. All values are floored at
// minLivenessThreshold and may be overridden globally via
// Supervisor.LivenessTimeout.
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
		// 30s ticker; 4× cadence covers one missed tick plus GC pauses.
		return 2 * time.Minute
	case GoroutineStateUpdater:
		// 5s ticker; 12× cadence keeps the threshold within minLivenessThreshold.
		return minLivenessThreshold
	case GoroutineLivenessWatchdog:
		// Self-check — if the watchdog hasn't run in 60s, something is very
		// wrong. We still record a tick so a future watchdog incarnation
		// (after a hypothetical recovery) can flag it.
		return minLivenessThreshold
	default:
		return minLivenessThreshold
	}
}

// agentThreshold computes the per-agent superviseAgent staleness threshold.
// An agent loop records a tick at the top of each iteration; a long spawn
// (running subprocess) is the longest in-iteration phase the watchdog must
// tolerate. Threshold = 2 × (no_work_backoff + spawn timeout) + slack.
func agentThreshold(s *Supervisor) time.Duration {
	cfg := s.ConfigSnapshot()
	noWork := 30 * time.Second
	if cfg != nil && cfg.Daemon.RestartPolicy.NoWorkBackoff != nil && *cfg.Daemon.RestartPolicy.NoWorkBackoff > 0 {
		noWork = time.Duration(*cfg.Daemon.RestartPolicy.NoWorkBackoff) * time.Second
	}
	// SpawnTimeout is not always configured; default to a generous 5min to
	// avoid flapping during heavy clone / dependency install on first spawn.
	spawnBudget := 5 * time.Minute
	threshold := 2*(noWork+spawnBudget) + agentLivenessSlack
	if threshold < minLivenessThreshold {
		threshold = minLivenessThreshold
	}
	return threshold
}
