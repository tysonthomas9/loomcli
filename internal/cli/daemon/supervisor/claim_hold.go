package supervisor

// Claim hold: the explicitly-owned, persistent refusal to START new work in a
// workspace. Split out of claim.go so both stay under the repo's 1000-line
// ceiling (.loc-allowlist takes no new entries); the pairing with
// claim_hold_test.go was already implied by that file's name.

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// claimHoldStillHeldLogInterval rate-limits the "still held" INFO line emitted
// from gateClaimsHeld. Agents cycle every claimHoldRecheckInterval, so without
// this a held fleet would write one line per agent per re-check.
const claimHoldStillHeldLogInterval = 5 * time.Minute

// claimHoldReloadInterval bounds how often ClaimHoldSnapshot re-reads
// claim-hold.json through the injected ReloadClaimHold hook. The file is the
// durable source of truth, so an external edit — an `rm`, or a release by a
// process that could not reach the control socket — must take effect without a
// daemon restart; this is how fast it does.
const claimHoldReloadInterval = 3 * time.Second

// ClaimHold is a workspace-level, explicitly-owned refusal to START new work.
//
// It gates the claim path ONLY: no yield file is written, no signal is sent,
// and no deadline is imposed on a run that is already in flight. It performs
// zero fleet-db calls by design — its whole purpose is to quiesce a workspace
// while fleet-db itself is being redeployed.
type ClaimHold struct {
	Held      bool      `json:"held"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	Since     time.Time `json:"since"`
	ExpiresAt time.Time `json:"expires_at,omitempty"` // zero = indefinite

	// Repos narrows the hold to these workspace repo IDs. EMPTY means
	// workspace-wide — the zero value, and therefore what every record written
	// before this field existed already means. No migration is needed.
	//
	// A scoped hold is a PARTIAL hold and costs what an unscoped one does not.
	// The gate runs before the Ready query, so at gate time there is no
	// candidate and no source repo to compare against; a scoped hold therefore
	// lets the query run and filters the CANDIDATES instead. Only an unscoped
	// hold keeps the zero-backend-call invariant, so only an unscoped hold is
	// safe while fleet-db itself is being redeployed.
	Repos []string `json:"repos,omitempty"`
}

// Scoped reports whether the hold names repos. Nil-safe. An unscoped hold is
// workspace-wide and is the only form that gates without touching the backend.
func (h *ClaimHold) Scoped() bool {
	return h != nil && len(h.Repos) > 0
}

// HoldsRepo reports whether repo falls inside this hold's scope. Nil-safe, and
// false for a hold that is not held. An unscoped hold covers every named repo;
// a scoped one matches exactly, in the same namespace as issue.SourceRepo.
//
// An EMPTY repo is never held: an issue that names no repo cannot be matched
// against a repo-named hold, and guessing would gate work the hold never
// claimed to cover.
func (h *ClaimHold) HoldsRepo(repo string) bool {
	if h == nil || !h.Held || repo == "" {
		return false
	}
	if len(h.Repos) == 0 {
		return true
	}
	return matchesHeldRepo(h.Repos, repo)
}

// matchesHeldRepo is the exact-match rule the router's repo affinity uses
// (cli.matchesRepo). Repo scoping must be a HARD filter: routing scores a repo
// mismatch 5 and still accepts it, so a scope can never be expressed there.
func matchesHeldRepo(repos []string, repo string) bool {
	for _, r := range repos {
		if r == repo {
			return true
		}
	}
	return false
}

// Active reports whether the hold should gate work at the given instant.
// Nil-safe: a nil hold is never active. A hold with a zero ExpiresAt is
// indefinite.
func (h *ClaimHold) Active(now time.Time) bool {
	if h == nil || !h.Held {
		return false
	}
	if h.ExpiresAt.IsZero() {
		return true
	}
	return now.Before(h.ExpiresAt)
}

// clone returns a copy so callers never hold a pointer into the supervisor's
// mutex-guarded state.
func (h *ClaimHold) clone() *ClaimHold {
	if h == nil {
		return nil
	}
	c := *h
	// Deep-copy the scope: a shallow copy would alias the caller's backing
	// array into the supervisor's mutex-guarded state, which is exactly what
	// clone exists to prevent.
	if h.Repos != nil {
		c.Repos = append([]string(nil), h.Repos...)
	}
	return &c
}

// SetClaimHold applies a claim hold (or clears it when h is nil / not held)
// and persists it through the injected PersistClaimHold hook. A persist
// failure is returned to the caller AND logged: the in-memory hold is still
// applied, so the operator learns the hold will not survive a daemon restart
// rather than silently losing it.
func (s *Supervisor) SetClaimHold(h *ClaimHold) error {
	stored := h.clone()
	if stored != nil && !stored.Held {
		stored = nil
	}
	s.claimHoldMu.Lock()
	s.claimHold = stored
	s.claimHoldExpiryLogged = false
	s.claimHoldLastHeldLog = time.Time{}
	persist := s.PersistClaimHold
	s.claimHoldMu.Unlock()

	if persist == nil {
		return nil
	}
	// File I/O deliberately outside the lock.
	if err := persist(stored); err != nil {
		slog.Error("failed to persist claim hold; it will not survive a daemon restart", "err", err)
		return err
	}
	return nil
}

// ReleaseClaimHold clears an active hold. Releasing a hold owned by a
// DIFFERENT actor requires force — an operator must not silently undo another
// operator's (or a deploy script's) quiesce.
func (s *Supervisor) ReleaseClaimHold(actor string, force bool) error {
	s.claimHoldMu.RLock()
	current := s.claimHold.clone()
	s.claimHoldMu.RUnlock()

	if !current.Active(time.Now()) {
		return s.SetClaimHold(nil)
	}
	if !force && current.Actor != actor {
		slog.Warn("refusing foreign claim-hold release", "holder", current.Actor, "requester", actor)
		return fmt.Errorf("claims held by %s since %s; use --force to release",
			current.Actor, current.Since.Format(time.RFC3339))
	}
	return s.SetClaimHold(nil)
}

// ClaimHoldSnapshot returns a copy of the current hold, evaluating expiry on
// read. On the first observation of an expired hold it clears the in-memory
// hold, clears the persisted file, and logs one WARN — so an expiry is visible
// exactly once rather than per agent per cycle.
func (s *Supervisor) ClaimHoldSnapshot() *ClaimHold {
	now := time.Now()
	s.maybeReloadClaimHold(now)

	s.claimHoldMu.Lock()
	held := s.claimHold
	if held == nil {
		s.claimHoldMu.Unlock()
		return nil
	}
	if held.Active(now) {
		snap := held.clone()
		s.claimHoldMu.Unlock()
		return snap
	}
	expired := held.clone()
	s.claimHold = nil
	first := !s.claimHoldExpiryLogged
	s.claimHoldExpiryLogged = true
	persist := s.PersistClaimHold
	s.claimHoldMu.Unlock()

	if first {
		slog.Warn("claim hold expired; agents will resume claiming",
			"actor", expired.Actor, "reason", expired.Reason,
			"since", expired.Since, "expires_at", expired.ExpiresAt)
		if persist != nil {
			if err := persist(nil); err != nil {
				slog.Error("failed to clear expired claim-hold file", "err", err)
			}
		}
	}
	return nil
}

// maybeReloadClaimHold re-reads claim-hold.json when it changed underneath this
// process and adopts what it finds. The file is authoritative: a foreign write
// (or deletion) wins over the in-memory hold, and the daemon's own writes are
// filtered out by the injected hook, so they never look like an external change.
//
// It FAILS CLOSED. A stat or parse error keeps the in-memory hold and logs one
// WARN — a hold is never dropped because the filesystem hiccuped. Like
// LoadClaimHold, adoption deliberately does NOT persist: the value came from
// the file in the first place.
func (s *Supervisor) maybeReloadClaimHold(now time.Time) {
	s.claimHoldMu.Lock()
	reload := s.ReloadClaimHold
	if reload == nil || (!s.claimHoldLastReload.IsZero() && now.Sub(s.claimHoldLastReload) < claimHoldReloadInterval) {
		s.claimHoldMu.Unlock()
		return
	}
	s.claimHoldLastReload = now
	s.claimHoldMu.Unlock()

	// File I/O deliberately outside the lock, as in SetClaimHold.
	hold, changed, err := reload()
	if err != nil {
		slog.Warn("failed to reload the claim-hold file; keeping the in-memory hold", "err", err)
		return
	}
	if !changed {
		return
	}

	stored := hold.clone()
	if !stored.Active(now) {
		// Covers nil, Held=false and an already-expired record: a reload must
		// never resurrect a hold the file itself says is over.
		stored = nil
	}

	s.claimHoldMu.Lock()
	s.claimHold = stored
	s.claimHoldExpiryLogged = false
	s.claimHoldLastHeldLog = time.Time{}
	s.claimHoldMu.Unlock()

	if stored == nil {
		slog.Info("claim hold reloaded from disk", "state", "released")
		return
	}
	slog.Info("claim hold reloaded from disk", "state", "held",
		"actor", stored.Actor, "reason", stored.Reason, "expires", claimHoldExpiryLabel(stored))
}

// LoadClaimHold hydrates the in-memory hold at daemon startup. It deliberately
// does NOT persist — the value came from the file in the first place.
func (s *Supervisor) LoadClaimHold(h *ClaimHold) {
	stored := h.clone()
	if stored != nil && !stored.Held {
		stored = nil
	}
	s.claimHoldMu.Lock()
	s.claimHold = stored
	s.claimHoldExpiryLogged = false
	s.claimHoldLastHeldLog = time.Time{}
	s.claimHoldMu.Unlock()
}

// gateClaimsHeld is the FIRST gate in preFlightSetup: an active hold stops the
// agent before any backend query, any recovery and any session creation.
// Returns false when the agent must not start.
func (s *Supervisor) gateClaimsHeld(ap *AgentProcess) bool {
	h := s.ClaimHoldSnapshot()
	if !h.Active(time.Now()) {
		s.setHeldRepos(ap, nil)
		s.clearStaleClaimsHeld(ap)
		return true
	}
	// A scoped hold cannot decide here. There is no candidate yet — claimTask
	// runs after this gate — so the only repo fact available is the agent's own
	// static binding, and a repo-unbound agent has none. Carry the scope on the
	// cycle and let claimTask filter the candidates (Level 2) instead.
	//
	// The scope is read ONCE per cycle, from this snapshot, deliberately:
	// ClaimHoldSnapshot has a throttled disk reload and one-shot expiry side
	// effects, so a second read could disagree with this one across an expiry
	// boundary and leave the gate and the filter applying different holds.
	if h.Scoped() && !s.agentStaticallyHeld(ap, h) {
		s.setHeldRepos(ap, h.Repos)
		s.clearStaleClaimsHeld(ap)
		return true
	}
	s.setHeldRepos(ap, nil)
	s.logStillHeld(h)
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome),
		fmt.Sprintf("claims held by %s since %s (%s)", h.Actor, h.Since.Format(time.RFC3339), h.Reason))
	return false
}

// clearStaleClaimsHeld drops a ClaimsHeld error once this gate has decided to
// let the agent proceed. A ClaimsHeld error describes the CURRENT pre-flight
// gate, not a historical run failure, so leaving it set would make `loom daemon
// status` and daemon-agents.json report a running agent as still claim-gated
// after the hold was released.
//
// Both proceed paths need it, not just the released one: under a scoped hold an
// agent bound to an un-held repo also runs on, and it may be carrying the error
// from an earlier cycle when the hold was still workspace-wide.
func (s *Supervisor) clearStaleClaimsHeld(ap *AgentProcess) {
	ap.Mu.Lock()
	if ap.LastError != nil && ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		ap.LastError = nil
	}
	ap.Mu.Unlock()
}

// agentStaticallyHeld reports whether EVERY repo this agent is bound to is
// named by the hold. Such an agent has nothing claimable regardless of what the
// board holds, so it is gated at Level 1 and issues no backend call at all.
//
// A repo-unbound agent resolves to no repos and is never statically held — on a
// repo-unbound fleet this is every agent, which is why Level 2 exists.
func (s *Supervisor) agentStaticallyHeld(ap *AgentProcess, h *ClaimHold) bool {
	repos := s.agentSourceRepos(ap)
	if len(repos) == 0 {
		return false
	}
	for _, repo := range repos {
		if !h.HoldsRepo(repo) {
			return false
		}
	}
	return true
}

// setHeldRepos records this cycle's repo scope on the agent. Cleared on every
// ungated path so a lifted hold cannot leak into the next cycle's filter.
func (s *Supervisor) setHeldRepos(ap *AgentProcess, repos []string) {
	ap.Mu.Lock()
	if len(repos) == 0 {
		ap.HeldRepos = nil
	} else {
		ap.HeldRepos = append([]string(nil), repos...)
	}
	ap.Mu.Unlock()
}

// logStillHeld emits the rate-limited "still held" INFO line. Rate limiting
// lives here rather than in a ticker goroutine: there is no lifecycle to
// manage, and it only logs while agents are actually cycling.
func (s *Supervisor) logStillHeld(h *ClaimHold) {
	now := time.Now()
	s.claimHoldMu.Lock()
	if !s.claimHoldLastHeldLog.IsZero() && now.Sub(s.claimHoldLastHeldLog) < claimHoldStillHeldLogInterval {
		s.claimHoldMu.Unlock()
		return
	}
	s.claimHoldLastHeldLog = now
	s.claimHoldMu.Unlock()

	gated, running := s.claimHoldGateCounts()
	slog.Info("claims held", "actor", h.Actor, "reason", h.Reason,
		"since", h.Since.Format(time.RFC3339), "expires", claimHoldExpiryLabel(h),
		"repos", ClaimHoldScopeLabel(h), "gated_agents", gated, "running", running)
}

// ClaimHoldScopeLabel renders a hold's repo scope for logs and status output.
// "all" for an unscoped (workspace-wide) hold, else the named repos.
func ClaimHoldScopeLabel(h *ClaimHold) string {
	if !h.Scoped() {
		return "all"
	}
	return strings.Join(h.Repos, ", ")
}

// claimHoldExpiryLabel renders a hold's expiry for logs and status output.
func claimHoldExpiryLabel(h *ClaimHold) string {
	if h == nil || h.ExpiresAt.IsZero() {
		return "never"
	}
	return h.ExpiresAt.Format(time.RFC3339)
}

// claimHoldGateCounts counts, in one pass, the agents currently gated by a
// claim hold and those still running a process. It walks the agent list
// directly rather than going through GetAgents: this runs on the pre-flight
// path, and GetAgents resolves per-agent backend config it does not need.
func (s *Supervisor) claimHoldGateCounts() (gated, running int) {
	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	for _, ap := range snapshot {
		ap.Mu.Lock()
		if ap.LastError != nil && ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
			gated++
		}
		if ap.Pid > 0 {
			running++
		}
		ap.Mu.Unlock()
	}
	return gated, running
}
