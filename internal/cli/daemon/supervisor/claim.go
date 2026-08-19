package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// actorClaimBackend is the optional richer claim API: when the issue backend
// implements it, claims are recorded against the agent's worktree identifier
// rather than the generic process actor.
type actorClaimBackend interface {
	ClaimIssueAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error
}

// actorReleaseBackend is the optional symmetric counterpart of
// actorClaimBackend. Backends that support this method allow the supervisor
// to release the claim lock on a task when the agent that holds it exits,
// rather than waiting for the lock's TTL to expire. Without this, an exited
// agent's lock blocks every subsequent claim attempt for that issue (whether
// from the same worktree or a different one) until the TTL elapses.
type actorReleaseBackend interface {
	ReleaseIssueAsActor(ctx context.Context, id string, actor string) error
}

const (
	claimReadyLimit         = 256
	claimConflictRetryLimit = 16
	claimOperationTimeout   = 10 * time.Second
)

func (s *Supervisor) claimTask(ap *AgentProcess, epicID string) bool {
	if s.IssueBackend == nil || !shouldClaimTaskForRole(ap) {
		return true
	}

	// Resume-first: re-claim the agent's OWN interrupted task (set by
	// prepareResume) directly, bypassing the ready-queue gate — an in_progress
	// task is never "ready", so the normal claim path can't recover it. On
	// failure, drop the resume target and fall through to a normal claim
	// (cold-start), so resume never strands an agent.
	ap.Mu.Lock()
	resumeTaskID := ap.ResumeTaskID
	ap.Mu.Unlock()
	if resumeTaskID != "" {
		if s.claimResumeTask(ap, resumeTaskID) {
			return true
		}
		ap.Mu.Lock()
		ap.ResumeTaskID = ""
		ap.Mu.Unlock()
	}

	opts, constraints := s.buildClaimOpts(ap, epicID)

	ap.Mu.Lock()
	requestedTaskID := ap.RequestedTaskID
	ap.Mu.Unlock()
	if ap.Entry.Mode == domain.AgentModeEphemeral && requestedTaskID == "" {
		s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), "ephemeral worker requires a requested task")
		return false
	}
	if requestedTaskID != "" {
		return s.claimRequestedTask(ap, opts, requestedTaskID)
	}

	// First try issues already assigned to this agent's worktree before
	// falling back to the global ready queue.
	if ap.Entry.Worktree != "" {
		assignedOpts := opts
		assignedOpts.Assignee = ap.Entry.Worktree
		if claimed, decided := s.tryClaimFromReady(ap, assignedOpts, constraints); decided {
			return claimed
		}
	}
	if claimed, decided := s.tryClaimFromReady(ap, opts, constraints); decided {
		return claimed
	}
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), "no claimable tasks")
	return false
}

// buildClaimOpts assembles the ReadyOpts for an agent's task claim,
// resolving the agent's source repos and merging role constraints.
func (s *Supervisor) buildClaimOpts(ap *AgentProcess, epicID string) (backend.ReadyOpts, cli.RoleConstraints) {
	ae := ap.Entry
	if sourceRepos, err := config.ResolveAgentRepos(ap.Entry, s.Repos); err == nil {
		ae.SourceRepos = sourceRepos
	} else {
		slog.Warn("failed to resolve agent repos for task claim", "worktree", ap.Entry.Worktree, "err", err)
	}
	constraints := cli.MergeRoleConstraints(ap.RoleConfig, ae)
	opts := backend.ReadyOpts{Limit: claimReadyLimit, ParentID: epicID}
	if ap.Entry.Repo != "" {
		opts.Labels = []string{"repo:" + ap.Entry.Repo}
	}
	if len(ae.SourceRepos) > 0 {
		opts.SourceRepos = ae.SourceRepos
	}
	return opts, constraints
}

// tryClaimFromReady runs Ready+claim against the given opts. Returns
// (claimed, decided): decided=false means "no decision, caller may try
// another opts variant"; decided=true means we either succeeded or hit a
// failure we've already recorded.
func (s *Supervisor) tryClaimFromReady(ap *AgentProcess, opts backend.ReadyOpts, constraints cli.RoleConstraints) (claimed, decided bool) {
	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setPreflightError(ap, agenterr.OutcomeFromHarness(wrapper.ErrUnknown), fmt.Sprintf("ready query failed: %v", err))
		return false, true
	}
	claimed, failed := s.tryClaimBestTask(ap, issues, constraints)
	if claimed {
		return true, true
	}
	if failed {
		return false, true
	}
	return false, false
}

func (s *Supervisor) readyIssues(opts backend.ReadyOpts) ([]backend.IssueData, error) {
	readyCtx, readyCancel := s.operationContext(claimOperationTimeout)
	issues, err := s.IssueBackend.Ready(readyCtx, opts)
	readyCancel()
	return issues, err
}

func (s *Supervisor) claimRequestedTask(ap *AgentProcess, opts backend.ReadyOpts, taskID string) bool {
	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setPreflightError(ap, agenterr.OutcomeFromHarness(wrapper.ErrUnknown), fmt.Sprintf("ready query failed: %v", err))
		return false
	}
	for _, issue := range issues {
		if issue.ID != taskID {
			continue
		}
		if !cli.IsWorkableTask(issue) {
			s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), fmt.Sprintf("requested task %s is not claimable", taskID))
			return false
		}
		if err := s.claimIssueForAgent(ap, taskID, "requested task"); err != nil {
			if backend.IsKind(err, backend.KindConflict) {
				s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome), fmt.Sprintf("requested task %s locked by %s", taskID, conflictHolder(err)))
				return false
			}
			s.setPreflightError(ap, agenterr.OutcomeFromHarness(wrapper.ErrUnknown), fmt.Sprintf("claim failed for %s: %v", taskID, err))
			return false
		}
		return true
	}
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), fmt.Sprintf("requested task %s is not ready", taskID))
	return false
}

// claimResumeTask re-acquires the claim on the agent's OWN interrupted task for
// a resume cycle. Unlike claimRequestedTask it does NOT consult the ready queue
// (an in_progress task is never "ready") — recovering your own task is safe, and
// the worktree actor already held the claim. Returns true when the task is ours
// to resume: a successful (re-)claim, or a conflict whose holder is THIS
// worktree (our own claim still within its TTL). Any other failure returns
// false so the caller cold-starts rather than stranding the agent.
func (s *Supervisor) claimResumeTask(ap *AgentProcess, taskID string) bool {
	err := s.claimIssueForAgent(ap, taskID, "resume interrupted task")
	if err == nil {
		return true
	}
	if backend.IsKind(err, backend.KindConflict) && conflictHolder(err) == ap.Entry.Worktree {
		ap.Mu.Lock()
		ap.AssignedTaskID = taskID
		ap.RequestedTaskID = ""
		ap.Mu.Unlock()
		slog.Info("resuming task already claimed by this worktree", "worktree", ap.Entry.Worktree, "task_id", taskID)
		return true
	}
	if backend.ClaimRejectedPermanently(err) {
		// The remnant task can never be claimed again in its current state
		// (e.g. status moved to blocked/closed while the agent was down).
		// Drop it from the lock so the next cycle cold-starts instead of
		// re-issuing the same doomed claim every restart interval.
		slog.Info("resume target no longer claimable; abandoning",
			"worktree", ap.Entry.Worktree, "task_id", taskID, "err", err)
		s.abandonResumeTarget(ap, taskID)
		return false
	}
	slog.Warn("resume re-claim failed; cold-starting", "worktree", ap.Entry.Worktree, "task_id", taskID, "err", err)
	return false
}

func (s *Supervisor) tryClaimBestTask(ap *AgentProcess, issues []backend.IssueData, constraints cli.RoleConstraints) (bool, bool) {
	conflicts := 0
	var lastConflictID, lastConflictHolder string
	for {
		match := cli.SelectBestTask(issues, constraints)
		if match == nil {
			return false, false
		}
		if err := s.claimIssueForAgent(ap, match.Issue.ID, match.Reason); err != nil {
			if backend.IsKind(err, backend.KindConflict) {
				conflicts++
				lastConflictID = match.Issue.ID
				lastConflictHolder = conflictHolder(err)
				if conflicts >= claimConflictRetryLimit {
					msg := fmt.Sprintf("no claimable tasks after %d conflicts (last: %s locked by %s)",
						conflicts, lastConflictID, lastConflictHolder)
					s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome), msg)
					return false, true
				}
				issues = removeIssueByID(issues, match.Issue.ID)
				continue
			}
			s.setPreflightError(ap, agenterr.OutcomeFromHarness(wrapper.ErrUnknown), fmt.Sprintf("claim failed for %s: %v", match.Issue.ID, err))
			return false, true
		}
		return true, false
	}
}

// conflictHolder extracts the holder identity from a KindConflict error's
// structured meta (populated by the fleet error classifier from the server's
// {existing_owner: "..."} response). Returns "unknown" when the holder cannot
// be determined — older fleet-db servers and non-fleet backends won't carry
// the metadata.
func conflictHolder(err error) string {
	var be *backend.BackendError
	if errors.As(err, &be) && be != nil {
		if holder, ok := be.Meta["existing_owner"]; ok && holder != "" {
			return holder
		}
	}
	return "unknown"
}

func (s *Supervisor) claimIssueForAgent(ap *AgentProcess, taskID, reason string) error {
	claimCtx, claimCancel := s.operationContext(claimOperationTimeout)
	var err error
	if ap.Entry.Worktree != "" {
		if actorBackend, ok := s.IssueBackend.(actorClaimBackend); ok {
			err = actorBackend.ClaimIssueAsActor(claimCtx, taskID, 0, ap.Entry.Worktree)
		} else {
			err = s.IssueBackend.ClaimIssue(claimCtx, taskID, 0)
		}
	} else {
		err = s.IssueBackend.ClaimIssue(claimCtx, taskID, 0)
	}
	claimCancel()
	if err != nil {
		return err
	}
	ap.Mu.Lock()
	ap.AssignedTaskID = taskID
	ap.RequestedTaskID = ""
	// The counters still hold the streak that just ended: applyNoWorkRestart
	// increments NoWorkCount, and every reset lives in an exit-path handler
	// that runs on the NEXT cycle. So this claim is also the "left idle" line.
	idlePolls := ap.NoWorkCount
	idleSince := ap.IdleSince
	ap.Mu.Unlock()
	args := []any{"worktree", ap.Entry.Worktree, "task_id", taskID, "reason", reason}
	if idlePolls > 0 {
		args = append(args, "idle_polls", idlePolls, "idle_for", time.Since(idleSince))
	}
	slog.Info("claimed task for agent", args...)
	return nil
}

// operationContext returns a context bounded by both the given timeout and
// the supervisor's Shutdown channel, so a slow backend call doesn't outlive
// supervisor shutdown.
//
// Within this branch every caller passes claimOperationTimeout, but the
// completion-hook line calls this with completionHookTimeout, so the parameter
// is load-bearing once the stacks are combined (proven on the integration
// branch) — hence the unparam waiver rather than a drop.
//
//nolint:unparam
func (s *Supervisor) operationContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if s.Shutdown == nil {
		return ctx, cancel
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-s.Shutdown:
			cancel()
		case <-done:
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

func shouldClaimTaskForRole(ap *AgentProcess) bool {
	return BuiltInRoles[ap.Entry.Role] || ap.RoleConfig.TaskFilter != ""
}

func (s *Supervisor) setPreflightError(ap *AgentProcess, class agenterr.Outcome, message string) {
	ap.Mu.Lock()
	ap.LastExitCode = 0
	ap.LastExit = time.Now()
	ap.LastError = &agenterr.AgentError{Class: class, Message: message}
	ap.LastNoWork = class.Is(agenterr.NoWorkOutcome)
	ap.Mu.Unlock()
}

func removeIssueByID(issues []backend.IssueData, id string) []backend.IssueData {
	out := issues[:0]
	for _, issue := range issues {
		if issue.ID != id {
			out = append(out, issue)
		}
	}
	return out
}

// releaseAssignedTaskClaim releases the issue-claim lock held by this agent on
// the given task. Called from completeControlPlaneAgentSession when the agent
// process exits. Without this, fleet-db's per-issue claim lock leaks until its
// TTL expires (~5 min), so the next agent — even with a fresh assignee — gets
// HTTP 409 KindConflict on every ClaimIssue attempt and silently NoWorks in
// the supervisor's restart backoff. The release is best-effort: if the backend
// does not support actor-scoped release, or if the lock is already gone (e.g.
// the agent already moved status to closed which auto-releases), this logs at
// debug level and returns without affecting the cleanup path.
func (s *Supervisor) releaseAssignedTaskClaim(ap *AgentProcess, taskID string) {
	if taskID == "" || ap.Entry.Worktree == "" || s.IssueBackend == nil {
		return
	}
	releaser, ok := s.IssueBackend.(actorReleaseBackend)
	if !ok {
		return
	}
	ctx, cancel := s.operationContext(claimOperationTimeout)
	defer cancel()
	if err := releaser.ReleaseIssueAsActor(ctx, taskID, ap.Entry.Worktree); err != nil {
		slog.Debug("agent task claim release skipped", "worktree", ap.Entry.Worktree, "task_id", taskID, "err", err)
	}
}

// claimHoldStillHeldLogInterval rate-limits the "still held" INFO line emitted
// from gateClaimsHeld. Agents cycle every claimHoldRecheckInterval, so without
// this a held fleet would write one line per agent per re-check.
const claimHoldStillHeldLogInterval = 5 * time.Minute

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
		return true
	}
	s.logStillHeld(h)
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome),
		fmt.Sprintf("claims held by %s since %s (%s)", h.Actor, h.Since.Format(time.RFC3339), h.Reason))
	return false
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
		"gated_agents", gated, "running", running)
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
