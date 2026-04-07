package supervisor

import (
	"log"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// GetEffectiveBackend returns the backend name for the agent's current failover position.
// Index 0 = primary (ap.Entry.Backend or config.Backend), index 1+ = FallbackBackends[idx-1].
func (s *Supervisor) GetEffectiveBackend(ap *AgentProcess) string {
	ap.Mu.Lock()
	idx := ap.CurrentBackendIdx
	ap.Mu.Unlock()

	cfg := s.ConfigSnapshot()

	if idx == 0 {
		b := ap.Entry.Backend
		if b == "" {
			b = ap.RoleConfig.Backend
		}
		if b == "" && cfg != nil {
			b = cfg.Backend
		}
		return b
	}

	fbIdx := idx - 1
	if fbIdx < len(ap.Entry.FallbackBackends) {
		return ap.Entry.FallbackBackends[fbIdx]
	}

	// Beyond fallback list — return primary (caller should have prevented this)
	b := ap.Entry.Backend
	if b == "" && cfg != nil {
		b = cfg.Backend
	}
	return b
}

// tryFallbackBackend checks if the agent should fail over to the next backend.
// Returns true if failover was triggered (caller should skip normal restart counting).
// Returns false if no failover is needed or all backends are exhausted.
func (s *Supervisor) tryFallbackBackend(ap *AgentProcess) bool {
	ap.Mu.Lock()
	lastErr := ap.LastError
	rateCount := ap.RateRetryCount
	currentIdx := ap.CurrentBackendIdx
	numFallbacks := len(ap.Entry.FallbackBackends)

	if lastErr == nil || numFallbacks == 0 {
		ap.Mu.Unlock()
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
		ap.Mu.Unlock()
		return false
	}

	// Check if there's a next backend to try
	totalBackends := 1 + numFallbacks
	nextIdx := currentIdx + 1
	if nextIdx >= totalBackends {
		worktree := ap.Entry.Worktree
		ap.Mu.Unlock()
		log.Printf("[daemon] Agent %s: all backends exhausted (tried %d), no more fallbacks",
			worktree, totalBackends)
		return false
	}

	// Switch to next backend (still holding the lock — no TOCTOU gap)
	ap.CurrentBackendIdx = nextIdx
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.Mu.Unlock()

	// Resolve backend name outside the lock for logging (GetEffectiveBackend acquires ap.Mu)
	nextBackend := s.GetEffectiveBackend(ap)
	log.Printf("[daemon] Agent %s: failing over from backend index %d to %d (%s)",
		ap.Entry.Worktree, currentIdx, nextIdx, nextBackend)

	return true
}
