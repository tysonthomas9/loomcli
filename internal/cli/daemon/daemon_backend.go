package daemon

import (
	"log"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// getEffectiveBackend returns the backend name for the agent's current failover position.
// Index 0 = primary (ap.entry.Backend or d.config.Backend), index 1+ = FallbackBackends[idx-1].
func (d *Daemon) getEffectiveBackend(ap *AgentProcess) string {
	ap.mu.Lock()
	idx := ap.currentBackendIdx
	ap.mu.Unlock()

	cfg := d.configSnapshot()

	if idx == 0 {
		b := ap.entry.Backend
		if b == "" {
			b = ap.roleConfig.Backend
		}
		if b == "" && cfg != nil {
			b = cfg.Backend
		}
		return b
	}

	fbIdx := idx - 1
	if fbIdx < len(ap.entry.FallbackBackends) {
		return ap.entry.FallbackBackends[fbIdx]
	}

	// Beyond fallback list — return primary (caller should have prevented this)
	b := ap.entry.Backend
	if b == "" && cfg != nil {
		b = cfg.Backend
	}
	return b
}

// tryFallbackBackend checks if the agent should fail over to the next backend.
// Returns true if failover was triggered (caller should skip normal restart counting).
// Returns false if no failover is needed or all backends are exhausted.
func (d *Daemon) tryFallbackBackend(ap *AgentProcess) bool {
	ap.mu.Lock()
	lastErr := ap.lastError
	rateCount := ap.rateRetryCount
	currentIdx := ap.currentBackendIdx
	numFallbacks := len(ap.entry.FallbackBackends)

	if lastErr == nil || numFallbacks == 0 {
		ap.mu.Unlock()
		return false
	}

	// Determine if failover should trigger
	shouldFailover := false
	switch {
	case lastErr.Class == agenterr.ModelNotFound:
		shouldFailover = true
	case lastErr.Class == agenterr.RateLimited && rateCount > 3:
		shouldFailover = true
	}

	if !shouldFailover {
		ap.mu.Unlock()
		return false
	}

	// Check if there's a next backend to try
	totalBackends := 1 + numFallbacks
	nextIdx := currentIdx + 1
	if nextIdx >= totalBackends {
		worktree := ap.entry.Worktree
		ap.mu.Unlock()
		log.Printf("[daemon] Agent %s: all backends exhausted (tried %d), no more fallbacks",
			worktree, totalBackends)
		return false
	}

	// Switch to next backend (still holding the lock — no TOCTOU gap)
	ap.currentBackendIdx = nextIdx
	ap.restartCount = 0
	ap.rateRetryCount = 0
	ap.mu.Unlock()

	// Resolve backend name outside the lock for logging (getEffectiveBackend acquires ap.mu)
	nextBackend := d.getEffectiveBackend(ap)
	log.Printf("[daemon] Agent %s: failing over from backend index %d to %d (%s)",
		ap.entry.Worktree, currentIdx, nextIdx, nextBackend)

	return true
}
