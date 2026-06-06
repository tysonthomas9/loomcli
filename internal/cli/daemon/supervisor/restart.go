package supervisor

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

const primaryBackendRetryCooldown = time.Minute

// backendUnavailableRecheckInterval is the fixed delay between retries when the
// backend CLI binary is missing. The agent recovers automatically once the
// binary is installed or PATH is fixed, so we poll on a fixed interval rather
// than an exponential backoff, and never count these retries toward max_retries.
const backendUnavailableRecheckInterval = 30 * time.Second

// shouldRestart determines if agent should restart based on backoff policy
// and the classified error from the most recent exit.
func (s *Supervisor) shouldRestart(ap *AgentProcess) bool {
	maxRetries := s.getMaxRetries()

	ap.Mu.Lock()
	shouldRestart, exhausted := s.shouldRestartLocked(ap, maxRetries)
	ap.Mu.Unlock()

	if exhausted != nil {
		exhausted.Backend = s.GetEffectiveBackend(ap)
		s.handleMaxRetriesExhausted(ap, *exhausted)
	}
	return shouldRestart
}

// shouldRestartLocked returns the restart decision and optional max-retry
// exhaustion snapshot. Caller holds ap.Mu.
func (s *Supervisor) shouldRestartLocked(ap *AgentProcess, maxRetries int) (bool, *maxRetriesExhausted) {
	// Clean success (exit 0, no error): always restart, reset counters.
	// Long runs (>1 minute) also reset primary backend.
	if ap.LastExitCode == 0 && ap.LastError == nil {
		resetAfterCleanSuccessLocked(ap)
		return true, nil
	}

	if ap.LastError != nil && ap.LastError.Class == agenterr.NoWork {
		s.applyNoWorkRestart(ap)
		return true, nil
	}

	// Backend unavailable (CLI binary not on PATH): wait without eroding the
	// restart budget; recoverable once the binary returns.
	if ap.LastError != nil && ap.LastError.Class == agenterr.BackendUnavailable {
		s.applyBackendUnavailableRestart(ap)
		return true, nil
	}

	// Fatal errors: stop immediately, no retries
	if ap.LastError != nil && ap.LastError.IsFatal() {
		log.Printf("[daemon] Agent %s: fatal error (%s), stopping supervisor",
			ap.Entry.Worktree, ap.LastError.Class)
		ap.NoWorkCount = 0
		ap.StopReason = StopReasonFatalError
		return false, nil
	}

	// Rate-limited: unlimited retries (don't count toward max_retries)
	if ap.LastError != nil && ap.LastError.Class == agenterr.RateLimited && s.getRateLimitNoCount() {
		applyRateLimitedRestartLocked(ap)
		return true, nil
	}

	// All other errors: count toward max_retries, then enter a terminal error
	// state on exhaustion. Task mutation is done after releasing ap.Mu because
	// backend calls can block.
	shouldRestart := s.applyGenericErrorRestart(ap, maxRetries)
	if !shouldRestart && ap.StopReason == StopReasonMaxRetries {
		return false, newMaxRetriesExhaustedLocked(ap, maxRetries)
	}
	return shouldRestart, nil
}

// Caller holds ap.Mu.
func resetAfterCleanSuccessLocked(ap *AgentProcess) {
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.StopReason = ""
	if time.Since(ap.LastStart) > time.Minute {
		ap.CurrentBackendIdx = 0 // reset to primary backend
	}
}

// Caller holds ap.Mu.
func applyRateLimitedRestartLocked(ap *AgentProcess) {
	ap.RateRetryCount++
	ap.NoWorkCount = 0
	ap.StopReason = ""
	log.Printf("[daemon] Agent %s: rate limited (retry %d, not counted toward max_retries)",
		ap.Entry.Worktree, ap.RateRetryCount)
}

// applyGenericErrorRestart handles a non-special error (not fatal, NoWork,
// BackendUnavailable, or rate-limited): it counts toward max_retries and stops
// automatic retries when the budget is exhausted. max_retries == 0 is the
// explicit fail-fast policy and reaches the same terminal error path after the
// first failed run. Fatal errors already returned false in shouldRestart before
// reaching here.
// Caller holds ap.Mu.
func (s *Supervisor) applyGenericErrorRestart(ap *AgentProcess, maxRetries int) bool {
	ap.RestartCount++
	ap.RateRetryCount = 0 // reset rate counter on non-rate error
	ap.NoWorkCount = 0
	if ap.RestartCount <= maxRetries {
		ap.StopReason = ""
		return true
	}
	// The guard sits after the increment so the max_retries==0 counter side
	// effect (RestartCount lands at 1) is preserved.
	ap.StopReason = StopReasonMaxRetries
	return false
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

// fixedRestartBackoff returns the fixed (non-exponential) wait for the cases
// that poll on a steady interval rather than backing off a flaky run: NoWork
// and BackendUnavailable. The bool is false when no fixed interval applies and
// the caller falls back to exponential backoff.
func (s *Supervisor) fixedRestartBackoff(lastErr *agenterr.AgentError) (time.Duration, bool) {
	switch {
	case lastErr != nil && lastErr.Class == agenterr.NoWork:
		return time.Duration(s.getNoWorkBackoff()) * time.Second, true
	case lastErr != nil && lastErr.Class == agenterr.BackendUnavailable:
		return s.backendRecheckBackoff(), true
	default:
		return 0, false
	}
}

// computeBackoff returns the sleep duration before next restart.
// Uses error-class-specific initial values and caps.
func (s *Supervisor) computeBackoff(ap *AgentProcess) time.Duration {
	maxBackoff := s.getBackoffMax()

	ap.Mu.Lock()
	lastErr := ap.LastError
	count := ap.RestartCount
	rateCount := ap.RateRetryCount
	ap.Mu.Unlock()

	// Fixed-interval cases (steady polling, not backing off a flaky run):
	// NoWork and BackendUnavailable.
	if d, ok := s.fixedRestartBackoff(lastErr); ok {
		return d
	}

	// Select initial backoff and retry count based on error class
	var initial int
	var retryN int
	if lastErr != nil && lastErr.Class == agenterr.RateLimited {
		initial = s.getRateLimitBackoff()
		retryN = rateCount
		maxBackoff = s.getRateLimitMaxWait()
	} else if lastErr != nil && lastErr.Class == agenterr.Timeout {
		initial = s.getTimeoutBackoff()
		retryN = count
	} else {
		initial = s.getBackoffInitial()
		retryN = count
	}

	// Cap count to prevent integer overflow in bit shift
	if retryN > 30 {
		retryN = 30
	}

	// Exponential: initial * 2^retryN
	backoffSec := initial * (1 << retryN)
	if backoffSec > maxBackoff || backoffSec < 0 {
		backoffSec = maxBackoff
	}

	backoff := time.Duration(backoffSec) * time.Second

	// For rate limits, respect server Retry-After hint if larger
	if lastErr != nil && lastErr.Class == agenterr.RateLimited && lastErr.RetryAfter > backoff {
		backoff = lastErr.RetryAfter
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
