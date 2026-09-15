package supervisor

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/cli/backendcheck"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ErrBackendUnavailable is the sentinel returned by spawnAgent when the
// agent's effective backend CLI is not on PATH. The supervisor's
// lifecycle treats this as a clean block — restart budget is preserved
// and no backoff is set — because the supervise loop re-checks PATH
// each iteration and auto-recovers the agent once the binary appears.
var ErrBackendUnavailable = errors.New("supervisor: backend binary not on PATH")

// gateBackendAvailable is the pre-spawn availability check. If the
// agent's effective backend CLI is not on PATH, the agent is
// transitioned to AgentStateBackendUnavailable (restart budget
// preserved, no backoff scheduled) and ErrBackendUnavailable is
// returned. Caller is responsible for tracing the failure.
//
// A discovery-layer failure (e.g. unreadable embedded versions.json)
// is logged but does not block spawn — the supervisor will surface
// the exec error if the binary is genuinely missing.
//
// ctx carries the caller's span so the control-plane state writes this gate
// makes record as children of it rather than as orphan siblings.
func (s *Supervisor) gateBackendAvailable(ctx context.Context, ap *AgentProcess) error {
	backend := s.GetEffectiveBackend(ap)
	if backend == "" {
		// No backend resolved (test fixture or misconfiguration). The
		// regular spawn path will surface whatever the symptom is; the
		// gate's job is specifically "named backend missing on PATH".
		return nil
	}
	info, lookupErr := backendcheck.CheckBackend(backend)
	if lookupErr != nil {
		slog.Warn("backend availability check failed; proceeding with spawn",
			"worktree", ap.Entry.Worktree, "backend", backend, "err", lookupErr)
		return nil
	}

	if info.Installed {
		// Recovery branch: if the agent was previously blocked for
		// backend-unavailable and the binary is now back, clear the
		// state so UIs reflect the recovery before the spawn proceeds.
		ap.Mu.Lock()
		wasUnavailable := ap.StopReason == StopReasonBackendUnavailable
		if wasUnavailable {
			ap.StopReason = ""
			ap.LastError = nil
		}
		worktree := ap.Entry.Worktree
		ap.Mu.Unlock()
		if wasUnavailable {
			s.markControlPlaneAgentState(ctx, ap, domain.AgentStateActive)
			log.Printf("[daemon] Agent %s: backend %q now on PATH — resuming spawn",
				worktree, backend)
		}
		return nil
	}

	ap.Mu.Lock()
	wasUnavailable := ap.StopReason == StopReasonBackendUnavailable
	ap.StopReason = StopReasonBackendUnavailable
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome),
		Message:   info.InstallHint,
		Backend:   backend,
		Timestamp: time.Now(),
	}
	worktree := ap.Entry.Worktree
	ap.Mu.Unlock()

	s.markControlPlaneAgentState(ctx, ap, domain.AgentStateBackendUnavailable)
	if !wasUnavailable {
		log.Printf("[daemon] Agent %s: backend %q not on PATH — skipping spawn (%s)",
			worktree, backend, info.InstallHint)
	}
	return ErrBackendUnavailable
}

// gateSafetyKnobsEnforceable fails closed when the role carries safety knobs
// the resolved backend cannot enforce, and warns when a knob is in force only
// softly. Spawning under a dropped restriction would be the exact
// config-that-lies failure these knobs used to be, so a tool list the backend
// cannot apply parks the agent with a visible error (SpawnFailure class); the
// daemon's config poll picks up a corrected role without a restart.
//
// read_only on a backend with no hard mechanism is the one knob that
// degrades rather than refuses — see backends.ValidateSafetyKnobs for why —
// and the warning is what keeps that honest. It is logged once per distinct
// message rather than per spawn attempt: this gate runs on every polling
// cycle, including cycles that claim no task, so an unconditional line would
// bury the daemon log.
func (s *Supervisor) gateSafetyKnobsEnforceable(ap *AgentProcess) error {
	backendName := s.GetEffectiveBackend(ap)
	warning, err := backends.ValidateSafetyKnobs(backendName,
		ap.RoleConfig.AllowedTools, ap.RoleConfig.DeniedTools, ap.RoleConfig.ReadOnly)
	if err == nil {
		if warning != "" {
			ap.Mu.Lock()
			repeat := ap.SoftKnobWarning == warning
			ap.SoftKnobWarning = warning
			worktree := ap.Entry.Worktree
			ap.Mu.Unlock()
			if !repeat {
				log.Printf("[daemon] Agent %s: SOFT ENFORCEMENT ONLY — %s", worktree, warning)
			}
		}
		return nil
	}
	ap.Mu.Lock()
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromDomain(agenterr.SpawnFailureOutcome),
		Message:   err.Error(),
		Backend:   backendName,
		Timestamp: time.Now(),
	}
	worktree := ap.Entry.Worktree
	ap.Mu.Unlock()
	log.Printf("[daemon] Agent %s: %v — skipping spawn", worktree, err)
	return err
}

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

	// Determine if failover should trigger: the policy names the classes
	// that fail over immediately (Decision Failover, e.g. ModelNotFound) and
	// the uncounted-retry threshold (FailoverAfter — rate limits fail over on
	// the observation AFTER the threshold, preserving the historical
	// rateCount > 3 behavior).
	d := agentpolicy.Decide(lastErr.Class)
	shouldFailover := d.Decision == agentpolicy.Failover ||
		(d.FailoverAfter > 0 && rateCount > d.FailoverAfter)

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
