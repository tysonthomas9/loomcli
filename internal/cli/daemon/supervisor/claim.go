package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
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
	// Level 2 of the claim hold: gateClaimsHeld stashed this cycle's scope when
	// the hold named repos. Empty (no hold, or an unscoped one that already
	// gated the agent above) makes the filter a no-op.
	filter := claimHoldFilter{repos: ap.HeldRepos}
	ap.Mu.Unlock()
	if ap.Entry.Mode == domain.AgentModeEphemeral && requestedTaskID == "" {
		s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), "ephemeral worker requires a requested task")
		return false
	}
	if requestedTaskID != "" {
		return s.claimRequestedTask(ap, opts, requestedTaskID, &filter)
	}

	// First try issues already assigned to this agent's worktree before
	// falling back to the global ready queue. One conflict ledger spans both
	// attempts so the failure report below can tell "the queue was empty" from
	// "every candidate was locked".
	var conflicts claimConflicts
	if ap.Entry.Worktree != "" {
		assignedOpts := opts
		assignedOpts.Assignee = ap.Entry.Worktree
		if claimed, decided := s.tryClaimFromReady(ap, assignedOpts, constraints, &conflicts, &filter); decided {
			return claimed
		}
	}
	if claimed, decided := s.tryClaimFromReady(ap, opts, constraints, &conflicts, &filter); decided {
		return claimed
	}
	s.reportNothingClaimed(ap, &conflicts, &filter)
	return false
}

// reportNothingClaimed records WHY a cycle claimed nothing. The three outcomes
// are deliberately distinct: an operator reading status must be able to tell a
// genuinely empty board from lock contention and from a quiesced repo.
func (s *Supervisor) reportNothingClaimed(ap *AgentProcess, conflicts *claimConflicts, filter *claimHoldFilter) {
	// The candidate list can empty through conflicts before the retry limit is
	// reached. Reporting the generic no-work message there would discard the
	// conflict detail and make a pure lock-contention stall indistinguishable
	// from an empty board.
	if conflicts.count > 0 {
		s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome), conflicts.message())
		return
	}
	// The queue was not empty — the hold emptied it. Reporting no-work here
	// would make a quiesced repo read as an empty board in status and in
	// gated_agents, which is the one distinction the hold exists to show.
	if filter.blocked() {
		s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome), filter.message())
		return
	}
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), "no claimable tasks")
}

// claimHoldFilter is Level 2 of a repo-scoped claim hold: it drops ready
// candidates whose source repo is held, before SelectBestTask ever sees them.
// The zero value is a no-op, which is what both "no hold" and "an unscoped
// hold" produce.
type claimHoldFilter struct {
	repos   []string
	dropped []string // held repos actually seen on the queue, first-seen order
}

// blocked reports whether the hold removed at least one candidate this cycle.
func (f *claimHoldFilter) blocked() bool { return len(f.dropped) > 0 }

// holds reports whether a candidate's source repo is inside the scope. An issue
// with an EMPTY source repo is never held — see ClaimHold.HoldsRepo.
func (f *claimHoldFilter) holds(repo string) bool {
	return len(f.repos) > 0 && repo != "" && matchesHeldRepo(f.repos, repo)
}

// apply returns the candidates the hold leaves claimable, recording which held
// repos were seen so the caller can tell "held" from "empty board".
func (f *claimHoldFilter) apply(issues []backend.IssueData) []backend.IssueData {
	if len(f.repos) == 0 {
		return issues
	}
	kept := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if f.holds(issue.SourceRepo) {
			f.record(issue.SourceRepo)
			continue
		}
		kept = append(kept, issue)
	}
	return kept
}

func (f *claimHoldFilter) record(repo string) {
	if matchesHeldRepo(f.dropped, repo) {
		return
	}
	f.dropped = append(f.dropped, repo)
}

func (f *claimHoldFilter) message() string {
	return fmt.Sprintf("claims held for %s; every ready candidate is in a held repo",
		strings.Join(f.dropped, ", "))
}

// claimConflicts accumulates lock-conflict detail across every claim attempt of
// one agent cycle, so the eventual failure names the last contended issue and
// its holder instead of the generic "no claimable tasks".
type claimConflicts struct {
	count      int
	lastID     string
	lastHolder string
}

func (c *claimConflicts) record(id, holder string) {
	c.count++
	c.lastID = id
	c.lastHolder = holder
}

func (c *claimConflicts) message() string {
	return fmt.Sprintf("no claimable tasks after %d conflicts (last: %s locked by %s)",
		c.count, c.lastID, c.lastHolder)
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

// agentSourceRepos resolves the repos an agent is bound to, nil when it is
// bound to none. A resolution error is reported as "unbound", which every
// consumer already treats as the widest — and therefore fail-safe — answer.
func (s *Supervisor) agentSourceRepos(ap *AgentProcess) []string {
	repos, err := config.ResolveAgentRepos(ap.Entry, s.Repos)
	if err != nil {
		return nil
	}
	return repos
}

// tryClaimFromReady runs Ready+claim against the given opts. Returns
// (claimed, decided): decided=false means "no decision, caller may try
// another opts variant"; decided=true means we either succeeded or hit a
// failure we've already recorded.
func (s *Supervisor) tryClaimFromReady(ap *AgentProcess, opts backend.ReadyOpts, constraints cli.RoleConstraints, conflicts *claimConflicts, filter *claimHoldFilter) (claimed, decided bool) {
	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setIssueBackendError(ap, "ready query failed", err)
		return false, true
	}
	issues = filter.apply(issues)
	claimed, failed := s.tryClaimBestTask(ap, issues, constraints, conflicts)
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

func (s *Supervisor) claimRequestedTask(ap *AgentProcess, opts backend.ReadyOpts, taskID string, filter *claimHoldFilter) bool {
	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setIssueBackendError(ap, "ready query failed", err)
		return false
	}
	for _, issue := range issues {
		if issue.ID != taskID {
			continue
		}
		// A requested task IS new work, so the hold applies to it exactly as it
		// applies to the queue. (Resume is the deliberate exemption: re-claiming
		// a task this agent already holds is not starting anything new.)
		if filter.holds(issue.SourceRepo) {
			s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome),
				fmt.Sprintf("claims held for %s; requested task %s is in a held repo", issue.SourceRepo, taskID))
			return false
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
			s.setIssueBackendError(ap, fmt.Sprintf("claim failed for %s", taskID), err)
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
//
// A repo-scoped claim hold deliberately does NOT apply here. The hold refuses
// to START new work; recovering the task this agent already holds is not new
// work, and abandoning it mid-flight is the very thing the hold promises not to
// do to a run in flight.
func (s *Supervisor) claimResumeTask(ap *AgentProcess, taskID string) bool {
	err := s.claimIssueForAgent(ap, taskID, "resume interrupted task")
	if err == nil {
		return true
	}
	if backend.IsKind(err, backend.KindConflict) && conflictHolder(err) == ap.Entry.Worktree {
		// The backend says the claim is still ours, so re-take the process-local
		// reservation the failed attempt above released. Skipping this would
		// leave the task free for a peer agent to claim underneath us.
		if reserveErr := s.claims.reserve(taskID, claimantID(ap)); reserveErr != nil {
			slog.Warn("resume task reserved by another agent; cold-starting", "worktree", ap.Entry.Worktree, "task_id", taskID, "err", reserveErr)
			return false
		}
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

func (s *Supervisor) tryClaimBestTask(ap *AgentProcess, issues []backend.IssueData, constraints cli.RoleConstraints, conflicts *claimConflicts) (bool, bool) {
	for {
		match := cli.SelectBestTask(issues, constraints)
		if match == nil {
			return false, false
		}
		if err := s.claimIssueForAgent(ap, match.Issue.ID, match.Reason); err != nil {
			if backend.IsKind(err, backend.KindConflict) {
				conflicts.record(match.Issue.ID, conflictHolder(err))
				if conflicts.count >= claimConflictRetryLimit {
					s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.LockConflictOutcome), conflicts.message())
					return false, true
				}
				issues = removeIssueByID(issues, match.Issue.ID)
				continue
			}
			s.setIssueBackendError(ap, fmt.Sprintf("claim failed for %s", match.Issue.ID), err)
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
	claimant := claimantID(ap)
	// Reserve first: this is the mutual exclusion. Losing the reservation race
	// returns a KindConflict indistinguishable from a backend one, so every
	// caller's existing conflict handling applies unchanged.
	if err := s.claims.reserve(taskID, claimant); err != nil {
		return err
	}
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
		s.claims.release(taskID, claimant)
		return err
	}
	// The agent moved on from whatever it reserved before, so anything else
	// still held under this claimant is stale and must not block a peer.
	s.claims.dropOthers(claimant, taskID)
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

// agentClaimantSeq numbers worktree-less agents so that each gets a claim
// identity distinct from its same-role peers. Process-local, which is exactly
// the ledger's scope — reservations never leave this daemon.
var agentClaimantSeq atomic.Uint64

// claimantID is the identity a claim is reserved under. The worktree is the
// identifier the rest of the claim path already uses (it is the fleet actor and
// the conflict holder), so an agent that has one is reserved under it.
//
// An agent configured without a worktree has no name of its own — AgentEntry
// has no such field — so it gets a per-instance identity assigned on first use
// and stable for the rest of its life. Deriving one from the role alone would
// give two worktree-less agents of one role the same identity, and reserve's
// "re-reserving your own task is a no-op" path would then let both through to
// the backend: precisely the simultaneity the ledger exists to remove.
func claimantID(ap *AgentProcess) string {
	if ap.Entry.Worktree != "" {
		return ap.Entry.Worktree
	}
	ap.claimantOnce.Do(func() {
		ap.claimantIdentity = fmt.Sprintf("agent:%s#%d", ap.Entry.Role, agentClaimantSeq.Add(1))
	})
	return ap.claimantIdentity
}

// claimLedger is the process-local mutual-exclusion ledger for task claims:
// task ID -> the claimant (worktree) that holds it. Every agent in this daemon
// claims through it, so of N agents racing for one issue exactly one reaches
// the backend and the rest get a KindConflict that falls through
// tryClaimBestTask's existing conflict path.
//
// It exists because a cold-started daemon spawns every agent at once and their
// claims land in the same millisecond. A backend that does not serialize those
// writes hands success to all of them and persists none, leaving the issue
// `open` in the ready queue while N agents work it (the 2026-08-27 PUPPET-201
// incident: three worktrees, one ticket, no winner). Serializing in-process
// cannot fix a racy backend for claims arriving from other daemons, but it
// removes the only source of simultaneity this fleet actually has.
//
// A reservation is held for as long as the agent holds the task and is dropped
// by release when the claim fails or the agent's session finalizes. The zero
// value is ready to use; the map is lazily initialized under mu.
type claimLedger struct {
	mu           sync.Mutex
	reservations map[string]string
}

// reserve takes the process-local reservation on taskID for claimant. Returns
// a KindConflict carrying the current holder in the same "existing_owner" meta
// key the fleet classifier uses, so conflictHolder names the peer agent rather
// than "unknown". Re-reserving your own task is a no-op, which keeps the resume
// path (which re-claims a task it already holds) working.
func (l *claimLedger) reserve(taskID, claimant string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if holder, ok := l.reservations[taskID]; ok && holder != claimant {
		return &backend.BackendError{
			Kind:    backend.KindConflict,
			Op:      "ClaimIssue",
			Message: fmt.Sprintf("task %s is already claimed by %s in this daemon", taskID, holder),
			Meta:    map[string]string{"existing_owner": holder},
		}
	}
	if l.reservations == nil {
		l.reservations = make(map[string]string)
	}
	l.reservations[taskID] = claimant
	return nil
}

// release drops the reservation on taskID, but only when claimant still holds
// it — a stale release must never free a task another agent has since taken.
func (l *claimLedger) release(taskID, claimant string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if holder, ok := l.reservations[taskID]; ok && holder == claimant {
		delete(l.reservations, taskID)
	}
}

// dropOthers frees every reservation held by claimant except keepID.
func (l *claimLedger) dropOthers(claimant, keepID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, holder := range l.reservations {
		if holder == claimant && id != keepID {
			delete(l.reservations, id)
		}
	}
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

// setIssueBackendError records a failed issue-backend call as a preflight
// error, separating an OUTAGE from every other backend failure.
//
// The distinction is the whole point. An unreachable or auth-rejecting issue
// store is infrastructure: it fails every agent at once and clears on its own,
// so it gets its own domain outcome and an uncounted retry (see
// agentpolicy.decideDomain). Anything else — a malformed response, an
// unexpected server error — stays Unknown and keeps eroding the restart
// budget, because those do not self-heal and an agent spinning on one forever
// is exactly what the budget exists to stop.
func (s *Supervisor) setIssueBackendError(ap *AgentProcess, what string, err error) {
	if issueBackendOutage(err) {
		s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.IssueBackendOutageOutcome),
			fmt.Sprintf("%s: issue backend unavailable: %v", what, err))
		return
	}
	s.setPreflightError(ap, agenterr.OutcomeFromHarness(wrapper.ErrUnknown), fmt.Sprintf("%s: %v", what, err))
}

// issueBackendOutage reports whether err says the issue store could not be
// reached or would not serve us, as opposed to answering with a real error.
//
// KindUnavailable covers both halves of the incident this exists for: the
// fleet client maps transport failures (connection refused, DNS) AND 401/403
// onto it ("authentication failed: workspace access denied"), because a
// credential the daemon cannot fix mid-flight is, to the daemon, the backend
// being unreachable. KindTimeout is the same condition observed through a
// deadline. KindCanceled is deliberately absent: that is our own shutdown.
func issueBackendOutage(err error) bool {
	return backend.IsKind(err, backend.KindUnavailable) || backend.IsKind(err, backend.KindTimeout)
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
	if taskID == "" {
		return
	}
	// Unconditionally, and before every backend-shaped early return below: the
	// process-local reservation is ours whether or not the backend supports
	// actor-scoped release, and leaking one would deadlock the task for the
	// daemon's remaining lifetime.
	s.claims.release(taskID, claimantID(ap))
	if ap.Entry.Worktree == "" || s.IssueBackend == nil {
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
		return true
	}
	s.setHeldRepos(ap, nil)
	s.logStillHeld(h)
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome),
		fmt.Sprintf("claims held by %s since %s (%s)", h.Actor, h.Since.Format(time.RFC3339), h.Reason))
	return false
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
