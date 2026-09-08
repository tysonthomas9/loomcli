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

// claimReleaseBackend is the stronger release: it puts the issue back to
// open/unassigned when it is still in_progress, and drops only the lock
// otherwise. Preferred over actorReleaseBackend on clean exit — releasing the
// lock alone leaves the task in_progress and unclaimable until fleet-db's
// claim reaper reverts it on lock-TTL expiry (~5 min), which is the ~5 minute
// tax every label-only hand-off used to pay. See PUPPET-467.
type claimReleaseBackend interface {
	ReleaseClaim(ctx context.Context, id, actor string) error
}

// configuredActorBackend exposes the fleet-db identity the backend
// authenticates as — the id ClaimIssue auto-registers the worker under, which
// is NOT the agent's worktree name whenever an API key is in play.
type configuredActorBackend interface {
	ConfiguredActor() string
}

// claimActorFor resolves the identity fleet-db attributed this agent's claim
// to, falling back to the worktree name when the backend cannot tell us.
func (s *Supervisor) claimActorFor(ap *AgentProcess) string {
	if b, ok := s.IssueBackend.(configuredActorBackend); ok {
		if a := b.ConfiguredActor(); a != "" {
			return a
		}
	}
	return ap.Entry.Worktree
}

// anotherAgentHolds reports whether a DIFFERENT agent process supervised by
// this daemon currently has taskID assigned. Because every agent authenticates
// to fleet-db as the same actor, the server-side assignee check cannot tell an
// exiting agent apart from the one that just reclaimed its task; this check
// can, for the realistic case of a single daemon owning every agent on a node.
// Cross-daemon reclaims remain out of reach and are not a scenario here.
func (s *Supervisor) anotherAgentHolds(taskID string, self *AgentProcess) bool {
	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	for _, other := range snapshot {
		if other == nil || other == self {
			continue
		}
		other.Mu.Lock()
		held := other.AssignedTaskID
		other.Mu.Unlock()
		if held == taskID {
			return true
		}
	}
	return false
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

	opts, constraints, err := s.buildClaimOpts(ap, epicID)
	if err != nil {
		// Fail closed. An agent that declares a binding we cannot resolve must
		// not fall through to an unfiltered claim: that drops the fetch filter
		// and, with an empty constraint list, the router gate too — a typo in
		// `repos:` would silently promote a bound agent to the whole fleet.
		s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), fmt.Sprintf("repo binding unresolved: %v", err))
		return false
	}

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
//
// It returns an error when the agent declares repo affinity that cannot be
// resolved. That case used to be a warning followed by an unbound claim, which
// is the one outcome a repo binding exists to prevent.
func (s *Supervisor) buildClaimOpts(ap *AgentProcess, epicID string) (backend.ReadyOpts, cli.RoleConstraints, error) {
	ae := ap.Entry
	sourceRepos, err := config.ResolveAgentRepos(ap.Entry, s.Repos)
	if err != nil {
		if len(ap.Entry.Repos) > 0 || len(ap.Entry.RepoGroups) > 0 {
			slog.Warn("agent repo binding unresolved; refusing to claim fleet-wide", "worktree", ap.Entry.Worktree, "err", err)
			return backend.ReadyOpts{}, cli.RoleConstraints{}, err
		}
		slog.Warn("failed to resolve agent repos for task claim", "worktree", ap.Entry.Worktree, "err", err)
	} else {
		ae.SourceRepos = sourceRepos
	}
	constraints := cli.MergeRoleConstraints(ap.RoleConfig, ae)
	opts := backend.ReadyOpts{Limit: claimReadyLimit, ParentID: epicID}
	if ap.Entry.Repo != "" {
		opts.Labels = []string{"repo:" + ap.Entry.Repo}
	}
	if len(ae.SourceRepos) > 0 {
		opts.SourceRepos = ae.SourceRepos
	}
	return opts, constraints, nil
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

// releaseAssignedTaskClaim releases the claim this agent holds on the given
// task. Called from completeControlPlaneAgentSession when the agent process
// exits. Without it the issue stays in_progress with the claim lock held until
// fleet-db's claim reaper reverts it on lock-TTL expiry (~5 min), so the next
// agent gets HTTP 409 KindConflict on every ClaimIssue attempt and silently
// NoWorks in the supervisor's restart backoff — a ~5 minute tax on every
// label-only hand-off (PUPPET-467).
//
// ReleaseClaim is preferred over ReleaseIssueAsActor: the latter drops only
// the operational lock, leaving the issue in_progress and still unclaimable.
//
// The release is best-effort and must never block agent cleanup, but every
// branch that declines to release now says so at Warn — the three silent skips
// this function used to have are exactly what hid the bug.
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
	if s.anotherAgentHolds(taskID, ap) {
		slog.Info("agent task claim release skipped: reclaimed by another agent",
			"task_id", taskID, "worktree", ap.Entry.Worktree)
		return
	}
	ctx, cancel := s.operationContext(claimOperationTimeout)
	defer cancel()
	if releaser, ok := s.IssueBackend.(claimReleaseBackend); ok {
		if err := releaser.ReleaseClaim(ctx, taskID, ap.Entry.Worktree); err != nil {
			slog.Warn("agent task claim release failed",
				"worktree", ap.Entry.Worktree, "task_id", taskID, "err", err)
		}
		return
	}
	if releaser, ok := s.IssueBackend.(actorReleaseBackend); ok {
		if err := releaser.ReleaseIssueAsActor(ctx, taskID, ap.Entry.Worktree); err != nil {
			slog.Warn("agent task lock release failed",
				"worktree", ap.Entry.Worktree, "task_id", taskID, "err", err)
		}
		return
	}
	slog.Warn("agent task claim not released: issue backend supports neither ReleaseClaim nor ReleaseIssueAsActor",
		"worktree", ap.Entry.Worktree, "task_id", taskID,
		"backend_type", fmt.Sprintf("%T", s.IssueBackend))
}
