package supervisor

import (
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// handleRestartAfterError handles restart logic after spawn failure.
// Returns true if the supervisor should continue, false if it should exit.
func (s *Supervisor) handleRestartAfterError(ap *AgentProcess) bool {
	ap.Mu.Lock()
	ap.RestartCount++
	count := ap.RestartCount
	ap.Mu.Unlock()

	maxRetries := s.getMaxRetries()
	if count > maxRetries {
		log.Printf("[daemon] Agent %s: max retries exceeded after spawn error", ap.Entry.Worktree)
		ap.Mu.Lock()
		ap.StopReason = StopReasonMaxRetries
		ap.Mu.Unlock()
		return false
	}

	backoff := s.computeBackoff(ap)
	log.Printf("[daemon] Agent %s: spawn failed, waiting %v before retry (attempt %d/%d)",
		ap.Entry.Worktree, backoff, count, maxRetries)

	ap.Mu.Lock()
	ap.BackoffUntil = time.Now().Add(backoff)
	ap.Mu.Unlock()

	var shouldContinue bool
	select {
	case <-time.After(backoff):
		shouldContinue = true
	case <-s.Shutdown:
		ap.Mu.Lock()
		ap.StopReason = StopReasonShutdown
		ap.Mu.Unlock()
		shouldContinue = false
	}

	ap.Mu.Lock()
	ap.BackoffUntil = time.Time{}
	ap.Mu.Unlock()

	return shouldContinue
}

// shouldRestart determines if agent should restart based on backoff policy
// and the classified error from the most recent exit.
func (s *Supervisor) shouldRestart(ap *AgentProcess) bool {
	maxRetries := s.getMaxRetries()

	ap.Mu.Lock()
	defer ap.Mu.Unlock()

	// Clean success (exit 0, no error): always restart, reset counters.
	// Long runs (>1 minute) also reset primary backend.
	if ap.LastExitCode == 0 && ap.LastError == nil {
		ap.RestartCount = 0
		ap.RateRetryCount = 0
		ap.NoWorkCount = 0
		ap.StopReason = ""
		if time.Since(ap.LastStart) > time.Minute {
			ap.CurrentBackendIdx = 0 // reset to primary backend
		}
		return true
	}

	// NoWork: no claimable tasks — always restart, never count toward max_retries.
	// Preserve CurrentBackendIdx: NoWork is about task availability, not backend health.
	// If the agent failed over to a fallback backend, it should stay on that backend.
	if ap.LastError != nil && ap.LastError.Class == agenterr.NoWork {
		ap.RestartCount = 0
		ap.RateRetryCount = 0
		ap.NoWorkCount++
		ap.StopReason = ""
		return true
	}

	// Fatal errors: stop immediately, no retries
	if ap.LastError != nil && ap.LastError.IsFatal() {
		log.Printf("[daemon] Agent %s: fatal error (%s), stopping supervisor",
			ap.Entry.Worktree, ap.LastError.Class)
		ap.NoWorkCount = 0
		ap.StopReason = StopReasonFatalError
		return false
	}

	// Rate-limited: unlimited retries (don't count toward max_retries)
	if ap.LastError != nil && ap.LastError.Class == agenterr.RateLimited && s.getRateLimitNoCount() {
		ap.RateRetryCount++
		ap.NoWorkCount = 0
		ap.StopReason = ""
		log.Printf("[daemon] Agent %s: rate limited (retry %d, not counted toward max_retries)",
			ap.Entry.Worktree, ap.RateRetryCount)
		return true
	}

	// All other errors: count toward max_retries
	ap.RestartCount++
	ap.RateRetryCount = 0 // reset rate counter on non-rate error
	ap.NoWorkCount = 0
	if ap.RestartCount <= maxRetries {
		ap.StopReason = ""
		return true
	}
	ap.StopReason = StopReasonMaxRetries
	return false
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

	// NoWork: fixed interval, no exponential growth
	if lastErr != nil && lastErr.Class == agenterr.NoWork {
		return time.Duration(s.getNoWorkBackoff()) * time.Second
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
func (s *Supervisor) GetOutputTimeout() int {
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
