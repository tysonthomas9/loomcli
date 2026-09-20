package supervisor

// The wall subsystem: what stops the daemon from marching agent after agent
// into a credential problem that is not going to move on its own.
//
// A "wall" is a live refusal recorded once and then honored by the pre-spawn
// gate, so a fleet that cannot authenticate, cannot bill, or has exhausted its
// usage window stops TAKING work instead of claiming it and immediately
// failing it. Walls are in-memory, never shorten, and expire purely on time.
//
// One rule decides a wall's blast radius: A WALL IS SCOPED TO WHATEVER OWNS
// THE CREDENTIAL IT IS ABOUT. Under per-agent profile isolation (see
// profile.go) an auth failure is a fact about ONE profile root, so it parks
// only the agents running on that root; billing and usage limits are facts
// about the shared subscription, so they park the fleet. Before profile
// isolation the first half of that rule was vacuous and every wall was
// fleet-wide — which is why one misprovisioned profile could park nine
// healthy agents.

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// defaultAccountWallCooldown is how long agents behind a wall stay parked
// after it is observed. It is the compromise between resuming quickly once a
// human fixes the credential and burning a run per agent per poll against a
// wall that is not going to move.
const defaultAccountWallCooldown = 15 * time.Minute

// maxAccountWallCooldown caps the recorded wall regardless of what the backend
// suggested. A harness that answers "retry after 86400" must not be able to
// park anything for a day.
const maxAccountWallCooldown = 1 * time.Hour

// wallScope is a wall's blast radius.
type wallScope int

const (
	// wallScopeNone: not a wall class at all. Every ordinary failure
	// (transient, timeout, spawn failure, model-not-found…) is agent-local
	// and must park nobody.
	wallScopeNone wallScope = iota
	// wallScopeProfile: one credential set. Parks the agents sharing that
	// profile root and nobody else.
	wallScopeProfile
	// wallScopeAccount: the whole subscription. Parks the fleet.
	wallScopeAccount
)

func (w wallScope) String() string {
	switch w {
	case wallScopeProfile:
		return "profile"
	case wallScopeAccount:
		return "account"
	default:
		return "none"
	}
}

// credentialWall is one live wall: when it lifts, what armed it, and the
// backend's own words for an operator to act on.
type credentialWall struct {
	Until   time.Time
	Class   agenterr.Outcome
	Message string
}

// WallInfo is one live wall rendered for `loom daemon status`. Credential is
// the profile root that owns it, or "shared" for the agents running on the
// operator's inherited harness config.
type WallInfo struct {
	Scope      string    `json:"scope"`
	Credential string    `json:"credential"`
	Class      string    `json:"class"`
	Message    string    `json:"message,omitempty"`
	Until      time.Time `json:"until"`
}

// wallScopeFor returns the blast radius an outcome earns.
//
// Auth is PROFILE-scoped because under profile isolation each agent
// authenticates from its own root with its own token: "this login is dead" is
// a fact about that root, and agents on other roots are unaffected. Billing
// and usage limits stay ACCOUNT-scoped because the subscription and its quota
// are genuinely shared.
//
// Deliberately not changed for rate limits: per-profile setup-token identities
// may in principle bill to distinct accounts, which would make a usage limit
// profile-scoped too — but that is not established, and over-parking on a rate
// limit is self-correcting (the retries are uncounted). Do not narrow it on a
// guess.
func wallScopeFor(o agenterr.Outcome) wallScope {
	switch {
	case o.IsClass(wrapper.ErrAuth):
		return wallScopeProfile
	case o.IsClass(wrapper.ErrBilling), o.IsClass(wrapper.ErrRateLimited):
		return wallScopeAccount
	default:
		return wallScopeNone
	}
}

// recordWall arms (or extends) the wall the outcome earns, scoped by key —
// the credential owner reported by ProfileCredentialKey, "" for the shared
// legacy environment. It takes only s.WallMu and reads no AgentProcess field,
// because its callers hold ap.Mu.
//
// The wall never shortens: a live wall is only ever pushed further out, so a
// second, smaller observation cannot release a parked agent early.
//
// Deliberately in-memory only. A daemon restart drops every wall, which is the
// right trade: a restart is an operator action, the first agent to hit the
// wall re-arms it within one run, and a stale on-disk wall outliving a fixed
// credential would be worse than the bug this closes.
func (s *Supervisor) recordWall(key string, outcome agenterr.Outcome, ae *agenterr.AgentError) {
	scope := wallScopeFor(outcome)
	if scope == wallScopeNone {
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

	if scope == wallScopeAccount {
		if !until.After(s.WallUntil) {
			return
		}
		s.WallUntil = until
		s.WallClass = outcome
		s.WallMessage = message
		return
	}

	// Profile scope. Prune on write so the map stays bounded by the number of
	// live credential sets rather than by every one ever seen.
	now := time.Now()
	for k, w := range s.ProfileWalls {
		if !w.Until.After(now) {
			delete(s.ProfileWalls, k)
		}
	}
	if existing, ok := s.ProfileWalls[key]; ok && !until.After(existing.Until) {
		return
	}
	if s.ProfileWalls == nil {
		s.ProfileWalls = map[string]credentialWall{}
	}
	s.ProfileWalls[key] = credentialWall{Until: until, Class: outcome, Message: message}
}

// wallActiveFor reports the live wall that applies to a credential key, if
// any. The account wall is checked first and wins when both are live: it is
// the longer-reaching fact and the one an operator must act on, so it is the
// message worth showing.
//
// Expiry is purely time-based for both scopes — no cleanup pass is required
// for correctness, and agents resume on their own.
func (s *Supervisor) wallActiveFor(key string) (time.Duration, string, agenterr.Outcome, wallScope, bool) {
	s.WallMu.Lock()
	defer s.WallMu.Unlock()

	if !s.WallUntil.IsZero() {
		if remaining := time.Until(s.WallUntil); remaining > 0 {
			return remaining, s.WallMessage, s.WallClass, wallScopeAccount, true
		}
	}
	if w, ok := s.ProfileWalls[key]; ok {
		if remaining := time.Until(w.Until); remaining > 0 {
			return remaining, w.Message, w.Class, wallScopeProfile, true
		}
	}
	return 0, "", agenterr.Outcome{}, wallScopeNone, false
}

// accountWallActive reports the time remaining on a live ACCOUNT wall and the
// message recorded with it. Profile walls are deliberately invisible here:
// this answers "is the whole fleet parked", which is a different question from
// "is this agent parked" (wallActiveFor).
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

// getAccountWallCooldown returns the configured wall cooldown, which governs
// both scopes: an auth wall and a billing wall want the same "wait for a
// human" cadence, and a second knob would be a second thing to get wrong.
// 0 disables ALL wall parking: walls still classify and still stop their own
// agent, but nobody is parked. The env override exists so integration tests
// can arm and expire a wall without waiting fifteen minutes.
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

// wallBackoffFor is how long an agent parked behind key's wall sleeps before
// re-checking the gate: exactly what that wall has left to run (itself capped
// at maxAccountWallCooldown), or zero when it lifted under us. Keyed, so an
// agent never sleeps out a wall it does not own.
func (s *Supervisor) wallBackoffFor(key string) time.Duration {
	remaining, _, _, _, ok := s.wallActiveFor(key)
	if !ok {
		return 0
	}
	return remaining
}

// ErrAccountWall is the sentinel returned by gateAccountWall when an
// account-level wall (billing, usage limit) recorded by ANY agent is still
// live. Like ErrBackendUnavailable it is a clean block: the restart budget is
// preserved and no backoff is set, because the supervise loop re-checks each
// iteration and the agent self-recovers at expiry.
var ErrAccountWall = errors.New("supervisor: account-level wall active")

// ErrProfileWall is the same block for a wall that belongs to ONE credential
// set: the agent's own profile root (or the shared operator config it
// inherits) hit an auth failure. Agents on other roots are not gated by it.
var ErrProfileWall = errors.New("supervisor: profile-level wall active")

// gateAccountWall is the pre-spawn wall check. Without it every agent behind a
// live wall claims a task, spawns, hits the same wall and fails, all within
// seconds — and a usage-limit wall retries uncounted, so it does that forever.
// The wall is recorded once (see recordWall) and this gate parks the agents it
// covers until it expires.
//
// WHICH agents it covers is the whole point: an auth wall parks only the
// agents authenticating from the same profile root, so one misprovisioned
// profile can no longer stop the fleet. Billing and usage walls park everyone,
// unchanged.
//
// It runs before claimTask so a walled agent stops TAKING work rather than
// claiming it and immediately failing it. It parks; it never kills a running
// agent and never erodes a restart budget.
func (s *Supervisor) gateAccountWall(ap *AgentProcess) error {
	// Resolved every cycle, and this is the only place it is refreshed, so a
	// profile provisioned while the daemon runs is picked up without a
	// restart. Stat-only by contract — see ProfileCredentialKey.
	key := ProfileCredentialKey(s.ProjectDir, ap.Entry.Worktree)

	remaining, message, class, scope, walled := s.wallActiveFor(key)

	if !walled {
		// Recovery branch: the wall expired, so an agent parked by one — of
		// either scope — is cleared before the spawn proceeds, the same shape
		// as the backend-unavailable recovery.
		ap.Mu.Lock()
		ap.CredentialKey = key
		wasWalled := ap.StopReason.IsWallPark()
		if wasWalled {
			ap.StopReason = ""
			ap.LastError = nil
		}
		worktree := ap.Entry.Worktree
		ap.Mu.Unlock()
		if wasWalled {
			// gateAccountWall runs on the pre-flight path, where no spawn span is
			// open; pass an explicit background context so the missing trace parent
			// is visible here rather than implied.
			s.markControlPlaneAgentState(context.Background(), ap, domain.AgentStateActive)
			slog.Info("wall lifted — resuming spawn", "worktree", worktree)
		}
		return nil
	}

	reason := StopReasonAccountWall
	err := ErrAccountWall
	if scope == wallScopeProfile {
		reason = StopReasonProfileWall
		err = ErrProfileWall
	}

	ap.Mu.Lock()
	ap.CredentialKey = key
	wasWalled := ap.StopReason == reason
	ap.StopReason = reason
	ap.LastError = &agenterr.AgentError{
		Class:     class,
		Message:   message,
		Timestamp: time.Now(),
	}
	worktree := ap.Entry.Worktree
	ap.Mu.Unlock()

	s.markControlPlaneAgentState(context.Background(), ap, domain.AgentStateIdle)
	// One line per wall transition, not one per agent per poll cycle. It names
	// the scope and the credential: the 2026-08-31 outage was slow to diagnose
	// because the line asserted "account-level" about what was really one
	// broken profile.
	if !wasWalled {
		slog.Warn("wall active — parking agent",
			"worktree", worktree, "scope", scope.String(),
			"credential", credentialLabel(key), "class", class.String(),
			"remaining", remaining.Round(time.Second), "detail", message)
	}
	return err
}

// credentialLabel renders a credential key for humans: the empty key is the
// operator's inherited config, which every unprofiled agent shares.
func credentialLabel(key string) string {
	if key == "" {
		return "shared"
	}
	return key
}

// WallSnapshot returns every live wall, account first and then profile walls
// by soonest expiry. It is what lets `loom daemon status` answer "why is
// nothing spawning" — and, crucially, "for whom" — without an operator
// grepping the daemon log.
func (s *Supervisor) WallSnapshot() []WallInfo {
	now := time.Now()

	s.WallMu.Lock()
	defer s.WallMu.Unlock()

	out := []WallInfo{}
	if !s.WallUntil.IsZero() && s.WallUntil.After(now) {
		out = append(out, WallInfo{
			Scope:      wallScopeAccount.String(),
			Credential: credentialLabel(""),
			Class:      s.WallClass.String(),
			Message:    s.WallMessage,
			Until:      s.WallUntil,
		})
	}
	profile := make([]WallInfo, 0, len(s.ProfileWalls))
	for key, w := range s.ProfileWalls {
		if !w.Until.After(now) {
			continue
		}
		profile = append(profile, WallInfo{
			Scope:      wallScopeProfile.String(),
			Credential: credentialLabel(key),
			Class:      w.Class.String(),
			Message:    w.Message,
			Until:      w.Until,
		})
	}
	sort.Slice(profile, func(i, j int) bool {
		if profile[i].Until.Equal(profile[j].Until) {
			return profile[i].Credential < profile[j].Credential
		}
		return profile[i].Until.Before(profile[j].Until)
	})
	return append(out, profile...)
}
