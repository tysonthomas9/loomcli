package cli

import (
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// handleRestartAfterError handles restart logic after spawn failure.
// Returns true if the supervisor should continue, false if it should exit.
func (d *Daemon) handleRestartAfterError(ap *AgentProcess) bool {
	ap.mu.Lock()
	ap.restartCount++
	count := ap.restartCount
	ap.mu.Unlock()

	maxRetries := d.getMaxRetries()
	if count > maxRetries {
		log.Printf("[daemon] Agent %s: max retries exceeded after spawn error", ap.entry.Worktree)
		return false
	}

	backoff := d.computeBackoff(ap)
	log.Printf("[daemon] Agent %s: spawn failed, waiting %v before retry (attempt %d/%d)",
		ap.entry.Worktree, backoff, count, maxRetries)

	select {
	case <-time.After(backoff):
		return true
	case <-d.shutdown:
		return false
	}
}

// shouldRestart determines if agent should restart based on backoff policy
// and the classified error from the most recent exit.
func (d *Daemon) shouldRestart(ap *AgentProcess) bool {
	maxRetries := d.getMaxRetries()

	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Clean success (exit 0, no error): always restart, reset counters.
	// Long runs (>1 minute) also reset primary backend.
	if ap.lastExitCode == 0 && ap.lastError == nil {
		ap.restartCount = 0
		ap.rateRetryCount = 0
		if time.Since(ap.lastStart) > time.Minute {
			ap.currentBackendIdx = 0 // reset to primary backend
		}
		return true
	}

	// NoWork: no claimable tasks — always restart, never count toward max_retries.
	// Preserve currentBackendIdx: NoWork is about task availability, not backend health.
	// If the agent failed over to a fallback backend, it should stay on that backend.
	if ap.lastError != nil && ap.lastError.Class == agenterr.NoWork {
		ap.restartCount = 0
		ap.rateRetryCount = 0
		return true
	}

	// Fatal errors: stop immediately, no retries
	if ap.lastError != nil && ap.lastError.IsFatal() {
		log.Printf("[daemon] Agent %s: fatal error (%s), stopping supervisor",
			ap.entry.Worktree, ap.lastError.Class)
		return false
	}

	// Rate-limited: unlimited retries (don't count toward max_retries)
	if ap.lastError != nil && ap.lastError.Class == agenterr.RateLimited && d.getRateLimitNoCount() {
		ap.rateRetryCount++
		log.Printf("[daemon] Agent %s: rate limited (retry %d, not counted toward max_retries)",
			ap.entry.Worktree, ap.rateRetryCount)
		return true
	}

	// All other errors: count toward max_retries
	ap.restartCount++
	ap.rateRetryCount = 0 // reset rate counter on non-rate error
	return ap.restartCount <= maxRetries
}

// computeBackoff returns the sleep duration before next restart.
// Uses error-class-specific initial values and caps.
func (d *Daemon) computeBackoff(ap *AgentProcess) time.Duration {
	maxBackoff := d.getBackoffMax()

	ap.mu.Lock()
	lastErr := ap.lastError
	count := ap.restartCount
	rateCount := ap.rateRetryCount
	ap.mu.Unlock()

	// NoWork: fixed interval, no exponential growth
	if lastErr != nil && lastErr.Class == agenterr.NoWork {
		return time.Duration(d.getNoWorkBackoff()) * time.Second
	}

	// Select initial backoff and retry count based on error class
	var initial int
	var retryN int
	if lastErr != nil && lastErr.Class == agenterr.RateLimited {
		initial = d.getRateLimitBackoff()
		retryN = rateCount
		maxBackoff = d.getRateLimitMaxWait()
	} else if lastErr != nil && lastErr.Class == agenterr.Timeout {
		initial = d.getTimeoutBackoff()
		retryN = count
	} else {
		initial = d.getBackoffInitial()
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

// Helper functions to safely access RestartPolicy fields with defaults.
func (d *Daemon) getMaxRetries() int {
	if d.config.Daemon.RestartPolicy.MaxRetries != nil {
		return *d.config.Daemon.RestartPolicy.MaxRetries
	}
	return 3 // default
}

func (d *Daemon) getBackoffInitial() int {
	if d.config.Daemon.RestartPolicy.BackoffInitial != nil {
		return *d.config.Daemon.RestartPolicy.BackoffInitial
	}
	return 2 // default seconds
}

func (d *Daemon) getBackoffMax() int {
	if d.config.Daemon.RestartPolicy.BackoffMax != nil {
		return *d.config.Daemon.RestartPolicy.BackoffMax
	}
	return 300 // default seconds
}

func (d *Daemon) getOutputTimeout() int {
	if d.config.Daemon.RestartPolicy.OutputTimeout != nil {
		return *d.config.Daemon.RestartPolicy.OutputTimeout
	}
	return 900 // default: 15 minutes
}

func (d *Daemon) getRateLimitBackoff() int {
	if d.config.Daemon.RestartPolicy.RateLimitBackoff != nil {
		return *d.config.Daemon.RestartPolicy.RateLimitBackoff
	}
	return 30 // default seconds
}

func (d *Daemon) getRateLimitMaxWait() int {
	if d.config.Daemon.RestartPolicy.RateLimitMaxWait != nil {
		return *d.config.Daemon.RestartPolicy.RateLimitMaxWait
	}
	return 300 // default seconds
}

func (d *Daemon) getRateLimitNoCount() bool {
	if d.config.Daemon.RestartPolicy.RateLimitNoCount != nil {
		return *d.config.Daemon.RestartPolicy.RateLimitNoCount
	}
	return true // default: rate-limit retries don't count toward max_retries
}

func (d *Daemon) getTimeoutBackoff() int {
	if d.config.Daemon.RestartPolicy.TimeoutBackoff != nil {
		return *d.config.Daemon.RestartPolicy.TimeoutBackoff
	}
	return 5 // default seconds
}

func (d *Daemon) getNoWorkBackoff() int {
	if d.config.Daemon.RestartPolicy.NoWorkBackoff != nil {
		return *d.config.Daemon.RestartPolicy.NoWorkBackoff
	}
	return 30 // default seconds
}

func (d *Daemon) getIdlePollInterval() int {
	if d.config.Daemon.RestartPolicy.IdlePollInterval != nil {
		return *d.config.Daemon.RestartPolicy.IdlePollInterval
	}
	return 30 // default seconds
}
