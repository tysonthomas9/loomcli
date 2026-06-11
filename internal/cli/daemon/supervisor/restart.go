package supervisor

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

const primaryBackendRetryCooldown = time.Minute

// backendUnavailableRecheckInterval is the fixed delay between retries when the
// backend CLI binary is missing. The agent recovers automatically once the
// binary is installed or PATH is fixed, so we poll on a fixed interval rather
// than an exponential backoff, and never count these retries toward max_retries.
const backendUnavailableRecheckInterval = 30 * time.Second

// defaultMaxRetriesParkInterval is the fixed delay between re-attempts after
// an agent has exhausted its restart budget and parked (policy OnExhaustion
// Park). Rather than abandoning the agent (silent loss until a daemon
// restart), the supervise goroutine parks and retries on this interval so a
// transient root cause (a prerequisite landing, a rate-limit window passing,
// a flaky dependency recovering) lets it self-resume.
const defaultMaxRetriesParkInterval = 60 * time.Second

// shouldRestart determines if the agent should restart by consulting the
// policy disposition for the classified outcome of the most recent exit
// (agentpolicy.Decide). The table owns the per-class verdict; this layer
// owns its counters (RestartCount/RateRetryCount/NoWorkCount/ParkCount) and
// its configured budgets.
func (s *Supervisor) shouldRestart(ap *AgentProcess) bool {
	maxRetries := s.getMaxRetries()

	ap.Mu.Lock()
	defer ap.Mu.Unlock()

	if stopAfterEphemeralTask(ap) {
		return false
	}

	// Clean success (exit 0, no error): always restart, reset counters —
	// including the park-escalation budget ("progress" ends a park spiral).
	if ap.LastExitCode == 0 && ap.LastError == nil {
		s.applyCleanSuccessRestart(ap)
		return true
	}

	var outcome agenterr.Outcome
	if ap.LastError != nil {
		outcome = ap.LastError.Class
	}

	switch d := agentpolicy.Decide(outcome); d.Decision {
	case agentpolicy.StopFatal:
		s.applyFatalStop(ap, outcome)
		return false

	case agentpolicy.FastFail:
		s.applyFastFailStop(ap, outcome)
		return false

	case agentpolicy.Park:
		// BackendUnavailable: fixed recheck without eroding the restart
		// budget — recoverable once the binary returns.
		s.applyBackendUnavailableRestart(ap)
		return true

	case agentpolicy.Failover:
		s.applyFailoverExhaustedStop(ap, outcome)
		return false

	case agentpolicy.RetryUncounted:
		if outcome.Is(agenterr.NoWorkOutcome) {
			s.applyNoWorkRestart(ap)
			return true
		}
		// Rate limits: unlimited uncounted retries by default; the
		// rate_limit_no_count config opt-out routes them through the
		// counted budget instead (the layer's config wins, pt7).
		if s.getRateLimitNoCount() {
			s.applyRateLimitedRestart(ap)
			return true
		}
		return s.applyCountedRestart(ap, d, maxRetries)

	default: // Retry
		return s.applyCountedRestart(ap, d, maxRetries)
	}
}

// stopAfterEphemeralTask stops the supervisor once an ephemeral agent has
// completed its one assigned task cycle cleanly. A NoWork exit still falls
// through so it can re-poll until a task arrives. Caller holds ap.Mu.
func stopAfterEphemeralTask(ap *AgentProcess) bool {
	if ap.Entry.Mode != domain.AgentModeEphemeral || ap.LastExitCode != 0 || ap.LastError != nil || ap.AssignedTaskID == "" {
		return false
	}
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonEphemeralDone
	log.Printf("[daemon] Agent %s: ephemeral task complete, exiting supervisor", ap.Entry.Worktree)
	return true
}

// applyFatalStop stops for auth/billing errors that need human intervention.
// Caller holds ap.Mu.
func (s *Supervisor) applyFatalStop(ap *AgentProcess, outcome agenterr.Outcome) {
	log.Printf("[daemon] Agent %s: fatal error (%s), stopping supervisor",
		ap.Entry.Worktree, outcome)
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonFatalError
}

// applyFastFailStop stops for deterministic errors retrying cannot fix.
// Caller holds ap.Mu.
func (s *Supervisor) applyFastFailStop(ap *AgentProcess, outcome agenterr.Outcome) {
	log.Printf("[daemon] Agent %s: deterministic failure (%s), stopping supervisor (fast-fail)",
		ap.Entry.Worktree, outcome)
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonFastFail
}

// applyFailoverExhaustedStop handles a failover-only error after the caller's
// fallback attempt has already failed because no fallback exists or all
// fallbacks are exhausted. Caller holds ap.Mu.
func (s *Supervisor) applyFailoverExhaustedStop(ap *AgentProcess, outcome agenterr.Outcome) {
	log.Printf("[daemon] Agent %s: failover-only error (%s) with no fallback remaining, stopping supervisor (fast-fail)",
		ap.Entry.Worktree, outcome)
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonFastFail
}

// applyCleanSuccessRestart resets every retry counter after a clean exit —
// including the park-escalation budget ("progress" ends a park spiral). Long
// runs (>1 minute) also reset to the primary backend. Caller holds ap.Mu.
func (s *Supervisor) applyCleanSuccessRestart(ap *AgentProcess) {
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.ParkCount = 0
	ap.StopReason = ""
	if time.Since(ap.LastStart) > time.Minute {
		ap.CurrentBackendIdx = 0 // reset to primary backend
	}
}

// applyRateLimitedRestart handles an uncounted rate-limit retry. Caller holds ap.Mu.
func (s *Supervisor) applyRateLimitedRestart(ap *AgentProcess) {
	ap.RateRetryCount++
	ap.NoWorkCount = 0
	ap.StopReason = ""
	log.Printf("[daemon] Agent %s: rate limited (retry %d, not counted toward max_retries)",
		ap.Entry.Worktree, ap.RateRetryCount)
}

// applyCountedRestart handles a counted retry (policy Decision Retry or
// Failover): the failure erodes max_retries, and once the budget is spent the
// disposition's OnExhaustion decides — Park (with ParkBudget escalating to
// FastFail when the agent never makes progress between parks) or FastFail.
// max_retries == 0 stays an explicit fail-fast opt-out; the guard sits after
// the increment so its counter side effect (RestartCount lands at 1) is
// unchanged. Caller holds ap.Mu.
func (s *Supervisor) applyCountedRestart(ap *AgentProcess, d agentpolicy.Disposition, maxRetries int) bool {
	ap.RestartCount++
	ap.RateRetryCount = 0 // reset rate counter on non-rate error
	ap.NoWorkCount = 0
	if ap.RestartCount <= maxRetries {
		ap.StopReason = ""
		return true
	}
	if maxRetries == 0 {
		ap.StopReason = StopReasonMaxRetries
		return false
	}
	switch d.OnExhaustion {
	case agentpolicy.Park:
		if d.ParkBudget > 0 && ap.ParkCount >= d.ParkBudget {
			log.Printf("[daemon] Agent %s: %d park cycles without progress, stopping supervisor (fast-fail)",
				ap.Entry.Worktree, ap.ParkCount)
			ap.StopReason = StopReasonFastFail
			return false
		}
		s.applyMaxRetriesPark(ap)
		return true
	case agentpolicy.FastFail:
		log.Printf("[daemon] Agent %s: restart budget exhausted on a deterministic failure, stopping supervisor (fast-fail)",
			ap.Entry.Worktree)
		ap.StopReason = StopReasonFastFail
		return false
	default:
		ap.StopReason = StopReasonMaxRetries
		return false
	}
}

// applyMaxRetriesPark parks an agent that has exhausted its restart budget so
// it keeps retrying on a fixed interval instead of being abandoned (silent
// loss until a daemon restart). It mirrors applyBackendUnavailableRestart:
// reset the retry counters, set a visible parked stop reason, and let
// computeBackoff return the fixed park interval. RestartCount resets so each
// park cycle gets a fresh max_retries burst and status readers never mistake
// a live parked agent for a failed one. LastError is deliberately left intact
// so last_error_class still surfaces why the agent parked. ParkCount
// increments to drive the ParkBudget escalation and resets only on a clean
// run. Caller holds ap.Mu.
func (s *Supervisor) applyMaxRetriesPark(ap *AgentProcess) {
	ap.ParkCount++
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonMaxRetriesParked
	log.Printf("[daemon] Agent %s: restart budget exhausted, parking (cycle %d) — will recheck in %s",
		ap.Entry.Worktree, ap.ParkCount, s.maxRetriesParkBackoff())
}

// maxRetriesParkBackoff is the fixed delay between re-attempts for a parked
// agent (configurable via maxRetriesParkInterval; package default otherwise).
func (s *Supervisor) maxRetriesParkBackoff() time.Duration {
	if s.maxRetriesParkInterval > 0 {
		return s.maxRetriesParkInterval
	}
	return defaultMaxRetriesParkInterval
}

// applyNoWorkRestart resets retry counters for a NoWork exit and, if the
// agent has failed over to a fallback backend, periodically returns to the
// primary to test recovery. Caller holds ap.Mu. NoWork never counts toward
// max_retries — task availability is not a backend-health signal.
func (s *Supervisor) applyNoWorkRestart(ap *AgentProcess) {
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount++
	if ap.CurrentBackendIdx > 0 && shouldRetryPrimaryAfterNoWork(ap.NoWorkCount, s.getNoWorkBackoff()) {
		ap.CurrentBackendIdx = 0
	}
	ap.StopReason = ""
}

// applyBackendUnavailableRestart keeps an agent retrying when the backend CLI
// binary is missing (CLI binary not on PATH): recoverable once the binary is
// installed or PATH is fixed, so we keep retrying on a fixed recheck interval
// without eroding the restart budget. Caller holds ap.Mu. Mirrors the pre-spawn
// backend gate for the case where the missing backend is only detected at
// runtime, after the process has already been spawned.
func (s *Supervisor) applyBackendUnavailableRestart(ap *AgentProcess) {
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonBackendUnavailable
	log.Printf("[daemon] Agent %s: backend unavailable, will recheck in %s (not counted toward max_retries)",
		ap.Entry.Worktree, s.backendRecheckBackoff())
}

// backendRecheckBackoff is the fixed delay between BackendUnavailable re-checks
// (configurable via backendRecheckInterval; package default otherwise).
func (s *Supervisor) backendRecheckBackoff() time.Duration {
	if s.backendRecheckInterval > 0 {
		return s.backendRecheckInterval
	}
	return backendUnavailableRecheckInterval
}

// computeBackoff returns the sleep duration before the next restart. The
// policy disposition names the configured bucket (BackoffProfile); this
// layer applies its restart_policy values and counters.
func (s *Supervisor) computeBackoff(ap *AgentProcess) time.Duration {
	maxBackoff := s.getBackoffMax()

	ap.Mu.Lock()
	lastErr := ap.LastError
	count := ap.RestartCount
	rateCount := ap.RateRetryCount
	parked := ap.StopReason == StopReasonMaxRetriesParked
	ap.Mu.Unlock()

	// A parked agent sleeps the fixed park interval — keyed on StopReason,
	// not error class, because any counted class can exhaust the budget.
	if parked {
		return s.maxRetriesParkBackoff()
	}

	var outcome agenterr.Outcome
	if lastErr != nil {
		outcome = lastErr.Class
	}
	d := agentpolicy.Decide(outcome)

	var initial int
	var retryN int
	switch d.Backoff {
	case agentpolicy.BPNoWork:
		// Fixed poll: task availability is not a backend-health signal.
		return time.Duration(s.getNoWorkBackoff()) * time.Second
	case agentpolicy.BPBackendUnavailable:
		// Fixed recheck: waiting for the backend CLI to reappear, not
		// backing off a flaky run.
		return s.backendRecheckBackoff()
	case agentpolicy.BPPark:
		return s.maxRetriesParkBackoff()
	case agentpolicy.BPRateLimit:
		initial = s.getRateLimitBackoff()
		retryN = rateCount
		maxBackoff = s.getRateLimitMaxWait()
	case agentpolicy.BPTimeout:
		initial = s.getTimeoutBackoff()
		retryN = count
	default:
		initial = s.getBackoffInitial()
		retryN = count
	}

	var hint time.Duration
	if d.HonorHint && lastErr != nil {
		hint = lastErr.RetryAfter
	}
	return exponentialBackoff(initial, retryN, maxBackoff, hint)
}

// exponentialBackoff computes initial * 2^retryN seconds capped at maxBackoff
// (both in seconds), preferring a larger harness Retry-After hint when one
// was carried through (hint is zero when the policy says not to honor it).
func exponentialBackoff(initial, retryN, maxBackoff int, hint time.Duration) time.Duration {
	// Cap count to prevent integer overflow in bit shift
	if retryN > 30 {
		retryN = 30
	}
	backoffSec := initial * (1 << retryN)
	if backoffSec > maxBackoff || backoffSec < 0 {
		backoffSec = maxBackoff
	}
	backoff := time.Duration(backoffSec) * time.Second
	if hint > backoff {
		backoff = hint
		if backoff > time.Duration(maxBackoff)*time.Second {
			backoff = time.Duration(maxBackoff) * time.Second
		}
	}
	return backoff
}

func shouldRetryPrimaryAfterNoWork(noWorkCount, noWorkBackoffSeconds int) bool {
	if noWorkCount <= 0 || noWorkBackoffSeconds <= 0 {
		return false
	}
	return time.Duration(noWorkCount)*time.Duration(noWorkBackoffSeconds)*time.Second >= primaryBackendRetryCooldown
}

// Helper functions to safely access config.RestartPolicy fields with defaults.
// All use ConfigSnapshot() to avoid data races with hot-reload.
func (s *Supervisor) getMaxRetries() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.MaxRetries != nil {
		return *cfg.Daemon.RestartPolicy.MaxRetries
	}
	return 3 // default
}

func (s *Supervisor) getBackoffInitial() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.BackoffInitial != nil {
		return *cfg.Daemon.RestartPolicy.BackoffInitial
	}
	return 2 // default seconds
}

func (s *Supervisor) getBackoffMax() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.BackoffMax != nil {
		return *cfg.Daemon.RestartPolicy.BackoffMax
	}
	return 300 // default seconds
}

// GetOutputTimeout returns the configured output timeout in seconds.
// LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS env var is honored when set — useful
// for integration tests that need to trip the watchdog quickly (e.g.
// test/playground/scenarios/). The env var wins over fleet-db config
// because fleet-db's wire schema does not currently persist this field
// (see internal/infra/fleetdb/daemon.go).
func (s *Supervisor) GetOutputTimeout() int {
	if v := os.Getenv("LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.OutputTimeout != nil {
		return *cfg.Daemon.RestartPolicy.OutputTimeout
	}
	return 900 // default: 15 minutes
}

func (s *Supervisor) getRateLimitBackoff() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.RateLimitBackoff != nil {
		return *cfg.Daemon.RestartPolicy.RateLimitBackoff
	}
	return 30 // default seconds
}

func (s *Supervisor) getRateLimitMaxWait() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.RateLimitMaxWait != nil {
		return *cfg.Daemon.RestartPolicy.RateLimitMaxWait
	}
	return 300 // default seconds
}

func (s *Supervisor) getRateLimitNoCount() bool {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.RateLimitNoCount != nil {
		return *cfg.Daemon.RestartPolicy.RateLimitNoCount
	}
	return true // default: rate-limit retries don't count toward max_retries
}

func (s *Supervisor) getTimeoutBackoff() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.TimeoutBackoff != nil {
		return *cfg.Daemon.RestartPolicy.TimeoutBackoff
	}
	return 5 // default seconds
}

func (s *Supervisor) getNoWorkBackoff() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.NoWorkBackoff != nil {
		return *cfg.Daemon.RestartPolicy.NoWorkBackoff
	}
	return 30 // default seconds
}

// GetIdlePollInterval returns the configured idle poll interval in seconds.
func (s *Supervisor) GetIdlePollInterval() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.IdlePollInterval != nil {
		return *cfg.Daemon.RestartPolicy.IdlePollInterval
	}
	return 30 // default seconds
}

// DefaultSigtermTimeout is the default SIGTERM-to-SIGKILL window in seconds.
const DefaultSigtermTimeout = 300

// GetSigtermTimeout returns the configured SIGTERM timeout duration.
func (s *Supervisor) GetSigtermTimeout() time.Duration {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.SigtermTimeout != nil && *cfg.Daemon.RestartPolicy.SigtermTimeout > 0 {
		return time.Duration(*cfg.Daemon.RestartPolicy.SigtermTimeout) * time.Second
	}
	return time.Duration(DefaultSigtermTimeout) * time.Second
}
