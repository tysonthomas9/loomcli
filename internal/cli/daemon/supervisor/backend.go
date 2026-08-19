package supervisor

import (
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
func (s *Supervisor) gateBackendAvailable(ap *AgentProcess) error {
	backend := s.GetEffectiveBackend(ap)
	if backend == "" {
		// No backend resolved (test fixture or misconfiguration). The
		// regular spawn path will surface whatever the symptom is; the
		// gate's job is specifically "named backend missing on PATH".
		return nil
	}
	info, misses, lookupErr := backendcheck.ConfirmBackend(backend)
	if lookupErr != nil {
		slog.Warn("backend availability check failed; proceeding with spawn",
			"worktree", ap.Entry.Worktree, "backend", backend, "err", lookupErr)
		return nil
	}

	if info.Installed {
		s.noteBackendAvailable(ap, backend, misses)
		return nil
	}
	s.noteBackendUnavailable(ap, backend, info.InstallHint)
	return ErrBackendUnavailable
}

// noteBackendAvailable handles the installed branch of the gate: it clears a
// previous backend-unavailable block so UIs reflect the recovery before the
// spawn proceeds, and reports a miss that ConfirmBackend rode out.
func (s *Supervisor) noteBackendAvailable(ap *AgentProcess, backend string, misses int) {
	ap.Mu.Lock()
	wasUnavailable := ap.StopReason == StopReasonBackendUnavailable
	if wasUnavailable {
		ap.StopReason = ""
		ap.LastError = nil
		ap.BackendStatePatchedAt = time.Now()
	}
	worktree := ap.Entry.Worktree
	ap.Mu.Unlock()

	if misses > 0 {
		// One line per debounced occurrence, never per attempt. This is the
		// observability that replaces the flap pair a transient miss used to
		// produce.
		log.Printf("[daemon] Agent %s: backend %q lookup missed %d time(s), recovered before declaring unavailable",
			worktree, backend, misses)
	}
	if wasUnavailable {
		s.markControlPlaneAgentState(ap, domain.AgentStateActive)
		log.Printf("[daemon] Agent %s: backend %q now on PATH — resuming spawn",
			worktree, backend)
	}
}

// noteBackendUnavailable parks the agent on a confirmed miss.
//
// The control-plane PATCH is edge-triggered: a parked agent re-checks every
// backendUnavailableRecheckInterval, and re-asserting the same state on each of
// those rechecks is what made the flap unbounded. It fires on transition plus a
// bounded level re-assert, so a control-plane row reset out from under a parked
// agent still converges.
func (s *Supervisor) noteBackendUnavailable(ap *AgentProcess, backend, installHint string) {
	// Decide under the lock, act after it — markControlPlaneAgentState makes a
	// network call and must never run while ap.Mu is held.
	ap.Mu.Lock()
	wasUnavailable := ap.StopReason == StopReasonBackendUnavailable
	ap.StopReason = StopReasonBackendUnavailable
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromDomain(agenterr.BackendUnavailableOutcome),
		Message:   installHint,
		Backend:   backend,
		Timestamp: time.Now(),
	}
	worktree := ap.Entry.Worktree
	// A zero BackendStatePatchedAt makes time.Since huge, so the first gate call
	// satisfies the re-assert clause as well as the transition clause.
	shouldPatch := !wasUnavailable ||
		time.Since(ap.BackendStatePatchedAt) >= s.backendStateReassertBackoff()
	if shouldPatch {
		ap.BackendStatePatchedAt = time.Now()
	}
	ap.Mu.Unlock()

	if shouldPatch {
		s.markControlPlaneAgentState(ap, domain.AgentStateBackendUnavailable)
	}
	if !wasUnavailable {
		log.Printf("[daemon] Agent %s: backend %q not on PATH — skipping spawn (%s)",
			worktree, backend, installHint)
	}
}

// backendStateReassertBackoff is how long gateBackendAvailable waits before
// re-asserting an unchanged backend-availability state to the control plane
// (configurable via backendStateReassert; package default otherwise).
func (s *Supervisor) backendStateReassertBackoff() time.Duration {
	if s.backendStateReassert > 0 {
		return s.backendStateReassert
	}
	return backendStateReassertInterval
}

// ErrAccountWall is the sentinel returned by gateAccountWall when an
// account-level wall (auth, billing, usage limit) recorded by ANOTHER agent is
// still live. Like ErrBackendUnavailable it is a clean block: the restart
// budget is preserved and no backoff is set, because the supervise loop
// re-checks each iteration and the agent self-recovers at expiry.
var ErrAccountWall = errors.New("supervisor: account-level wall active")

// gateAccountWall is the pre-spawn fleet-wide wall check. An auth, billing or
// usage wall is a fact about the ACCOUNT: without this gate every agent claims
// a task, spawns, hits the same wall and fails, all within seconds — and a
// usage-limit wall retries uncounted, so it does that forever. The wall is
// recorded once (see recordAccountWall) and this gate parks the rest of the
// fleet until it expires.
//
// It runs before claimTask so a walled fleet stops TAKING work rather than
// claiming it and immediately failing it. It parks; it never kills a running
// agent and never erodes a restart budget.
func (s *Supervisor) gateAccountWall(ap *AgentProcess) error {
	remaining, message, walled := s.accountWallActive()

	if !walled {
		// Recovery branch: the wall expired, so an agent parked by it is
		// cleared before the spawn proceeds — same shape as the
		// backend-unavailable recovery above.
		ap.Mu.Lock()
		wasWalled := ap.StopReason == StopReasonAccountWall
		if wasWalled {
			ap.StopReason = ""
			ap.LastError = nil
		}
		worktree := ap.Entry.Worktree
		ap.Mu.Unlock()
		if wasWalled {
			s.markControlPlaneAgentState(ap, domain.AgentStateActive)
			slog.Info("account wall lifted — resuming spawn", "worktree", worktree)
		}
		return nil
	}

	s.WallMu.Lock()
	class := s.WallClass
	s.WallMu.Unlock()

	ap.Mu.Lock()
	wasWalled := ap.StopReason == StopReasonAccountWall
	ap.StopReason = StopReasonAccountWall
	ap.LastError = &agenterr.AgentError{
		Class:     class,
		Message:   message,
		Timestamp: time.Now(),
	}
	worktree := ap.Entry.Worktree
	ap.Mu.Unlock()

	s.markControlPlaneAgentState(ap, domain.AgentStateIdle)
	// One line per wall transition, not one per agent per poll cycle.
	if !wasWalled {
		slog.Warn("account-level wall active — parking agent",
			"worktree", worktree, "class", class.String(),
			"remaining", remaining.Round(time.Second), "detail", message)
	}
	return ErrAccountWall
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
