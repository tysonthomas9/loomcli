package supervisor

import (
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
