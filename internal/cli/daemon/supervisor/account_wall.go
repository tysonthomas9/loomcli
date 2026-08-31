package supervisor

// The wall subsystem: what stops the daemon from marching agent after agent
// into a credential problem that is not going to move on its own.
//
// A "wall" is a live refusal recorded once and then honoured by the pre-spawn
// gate, so a fleet that cannot authenticate, cannot bill, or has exhausted its
// usage window stops TAKING work instead of claiming it and immediately
// failing it. Walls are in-memory, never shorten, and expire purely on time.
//
// One rule decides a wall's blast radius: a wall is scoped to whatever owns
// the credential it is about. Under per-agent profile isolation (see
// profile.go) an auth failure is a fact about ONE profile root, so it parks
// only the agents running on that root; billing and usage limits are facts
// about the shared subscription, so they park the fleet.

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// defaultAccountWallCooldown is how long the fleet stays parked after an
// account-level wall (auth, billing, usage limit) is observed. It is the
// compromise between resuming quickly once a human fixes billing and burning
// a run per agent per poll against a wall that is not going to move.
const defaultAccountWallCooldown = 15 * time.Minute

// maxAccountWallCooldown caps the recorded wall regardless of what the backend
// suggested. A harness that answers "retry after 86400" must not be able to
// park the whole fleet for a day.
const maxAccountWallCooldown = 1 * time.Hour

// isAccountWallClass reports whether an outcome is an ACCOUNT-level wall —
// one that no other agent on the same account can get past either. Every other
// class (transient, timeout, spawn failure, model-not-found…) is agent-local
// and must never park the fleet.
func isAccountWallClass(o agenterr.Outcome) bool {
	return o.IsClass(wrapper.ErrAuth) ||
		o.IsClass(wrapper.ErrBilling) ||
		o.IsClass(wrapper.ErrRateLimited)
}

// recordAccountWall arms (or extends) the fleet-wide account wall. It takes
// only s.WallMu and reads no AgentProcess field, because its callers hold
// ap.Mu. The wall never shortens: a live wall is only ever pushed further out,
// so a second, smaller observation cannot release the fleet early.
//
// Deliberately in-memory only. A daemon restart drops the wall, which is the
// right trade: a restart is an operator action, the first agent to hit the
// wall re-arms it within one run, and a stale on-disk wall outliving a fixed
// account would be worse than the bug this closes.
func (s *Supervisor) recordAccountWall(outcome agenterr.Outcome, ae *agenterr.AgentError) {
	if !isAccountWallClass(outcome) {
		return
	}
	cooldown := s.getAccountWallCooldown()
	if cooldown <= 0 {
		return // gate disabled: classify and stop the agent, but park nobody
	}
	// A wall that stated its own reset time knows better than the default.
	if ae != nil && ae.RetryAfter > 0 {
		cooldown = ae.RetryAfter
	}
	if cooldown > maxAccountWallCooldown {
		cooldown = maxAccountWallCooldown
	}
	var message string
	if ae != nil {
		message = ae.Message
	}
	until := time.Now().Add(cooldown)

	s.WallMu.Lock()
	defer s.WallMu.Unlock()
	if !until.After(s.WallUntil) {
		return
	}
	s.WallUntil = until
	s.WallClass = outcome
	s.WallMessage = message
}

// accountWallActive reports the time remaining on a live account wall and the
// message recorded with it. Returns ok=false once the wall has expired (or was
// never armed) — expiry is purely time-based, so the fleet resumes on its own.
func (s *Supervisor) accountWallActive() (time.Duration, string, bool) {
	s.WallMu.Lock()
	defer s.WallMu.Unlock()
	if s.WallUntil.IsZero() {
		return 0, "", false
	}
	remaining := time.Until(s.WallUntil)
	if remaining <= 0 {
		return 0, "", false
	}
	return remaining, s.WallMessage, true
}

// getAccountWallCooldown returns the configured fleet-wide wall cooldown.
// 0 disables the gate entirely: walls still classify and still stop their own
// agent, but no other agent is parked. The env override exists so integration
// tests can arm and expire a wall without waiting fifteen minutes.
func (s *Supervisor) getAccountWallCooldown() time.Duration {
	if v := os.Getenv("LOOM_DAEMON_ACCOUNT_WALL_COOLDOWN_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	cfg := s.ConfigSnapshot()
	if cfg != nil && cfg.Daemon.RestartPolicy.AccountWallCooldown != nil {
		return time.Duration(*cfg.Daemon.RestartPolicy.AccountWallCooldown) * time.Second
	}
	return defaultAccountWallCooldown
}

// accountWallBackoff is how long a parked agent sleeps before re-checking the
// gate: exactly what the wall has left to run (itself capped at
// maxAccountWallCooldown), or zero when the wall lifted under us.
func (s *Supervisor) accountWallBackoff() time.Duration {
	remaining, _, ok := s.accountWallActive()
	if !ok {
		return 0
	}
	return remaining
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
