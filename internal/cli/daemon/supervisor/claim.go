package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
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
	opts, constraints := s.buildClaimOpts(ap, epicID)

	ap.Mu.Lock()
	requestedTaskID := ap.RequestedTaskID
	ap.Mu.Unlock()
	if requestedTaskID != "" {
		return s.claimRequestedTask(ap, opts, requestedTaskID)
	}

	var conflicts claimConflictInfo
	// First try issues already assigned to this agent's worktree before
	// falling back to the global ready queue.
	if ap.Entry.Worktree != "" {
		assignedOpts := opts
		assignedOpts.Assignee = ap.Entry.Worktree
		claimed, decided, attemptConflicts := s.tryClaimFromReady(ap, assignedOpts, constraints)
		conflicts = mergeClaimConflict(conflicts, attemptConflicts)
		if decided {
			return claimed
		}
	}
	claimed, decided, attemptConflicts := s.tryClaimFromReady(ap, opts, constraints)
	conflicts = mergeClaimConflict(conflicts, attemptConflicts)
	if decided {
		return claimed
	}
	if conflicts.seen() {
		s.setPreflightError(ap, agenterr.LockConflict, conflicts.message())
		return false
	}
	s.setPreflightError(ap, agenterr.NoWork, "no claimable tasks")
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
// failure we've already recorded. Claim conflicts that did not hit the local
// cap are returned to the caller so it can try other queues before deciding
// whether the final preflight result is NoWork or LockConflict.
func (s *Supervisor) tryClaimFromReady(ap *AgentProcess, opts backend.ReadyOpts, constraints cli.RoleConstraints) (claimed, decided bool, conflicts claimConflictInfo) {
	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("ready query failed: %v", err))
		return false, true, claimConflictInfo{}
	}
	claimed, failed, conflicts := s.tryClaimBestTask(ap, issues, constraints)
	if claimed {
		return true, true, conflicts
	}
	if failed {
		return false, true, conflicts
	}
	return false, false, conflicts
}

func (s *Supervisor) readyIssues(opts backend.ReadyOpts) ([]backend.IssueData, error) {
	readyCtx, readyCancel := s.operationContext(claimOperationTimeout)
	issues, err := s.IssueBackend.Ready(readyCtx, opts)
	readyCancel()
	return issues, err
}

func (s *Supervisor) claimRequestedTask(ap *AgentProcess, opts backend.ReadyOpts, taskID string) bool {
	if ap.Entry.Worktree != "" {
		opts.Assignee = ap.Entry.Worktree
	}
	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("ready query failed: %v", err))
		return false
	}
	for _, issue := range issues {
		if issue.ID != taskID {
			continue
		}
		if !cli.IsWorkableTask(issue) {
			s.setPreflightError(ap, agenterr.NoWork, fmt.Sprintf("requested task %s is not claimable", taskID))
			return false
		}
		if err := s.claimIssueForAgent(ap, taskID, "requested task"); err != nil {
			if backend.IsKind(err, backend.KindConflict) {
				s.setPreflightError(ap, agenterr.LockConflict, fmt.Sprintf("requested task %s locked by %s", taskID, conflictHolder(err)))
				return false
			}
			s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("claim failed for %s: %v", taskID, err))
			return false
		}
		return true
	}
	s.setPreflightError(ap, agenterr.NoWork, fmt.Sprintf("requested task %s is not ready", taskID))
	return false
}

type claimConflictInfo struct {
	count      int
	lastID     string
	lastHolder string
}

func (c claimConflictInfo) seen() bool { return c.count > 0 }

func (c claimConflictInfo) message() string {
	if c.count == 1 {
		return fmt.Sprintf("no claimable tasks after claim conflict (last: %s locked by %s)", c.lastID, c.lastHolder)
	}
	return fmt.Sprintf("no claimable tasks after %d conflicts (last: %s locked by %s)", c.count, c.lastID, c.lastHolder)
}

func mergeClaimConflict(a, b claimConflictInfo) claimConflictInfo {
	if !b.seen() {
		return a
	}
	a.count += b.count
	a.lastID = b.lastID
	a.lastHolder = b.lastHolder
	return a
}

func (s *Supervisor) tryClaimBestTask(ap *AgentProcess, issues []backend.IssueData, constraints cli.RoleConstraints) (bool, bool, claimConflictInfo) {
	var conflicts claimConflictInfo
	for {
		match := cli.SelectBestTask(issues, constraints)
		if match == nil {
			return false, false, conflicts
		}
		if err := s.claimIssueForAgent(ap, match.Issue.ID, match.Reason); err != nil {
			if backend.IsKind(err, backend.KindConflict) {
				conflicts.count++
				conflicts.lastID = match.Issue.ID
				conflicts.lastHolder = conflictHolder(err)
				if conflicts.count >= claimConflictRetryLimit {
					s.setPreflightError(ap, agenterr.LockConflict, conflicts.message())
					return false, true, conflicts
				}
				issues = removeIssueByID(issues, match.Issue.ID)
				continue
			}
			s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("claim failed for %s: %v", match.Issue.ID, err))
			return false, true, conflicts
		}
		return true, false, conflicts
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
	ap.Mu.Unlock()
	slog.Info("claimed task for agent", "worktree", ap.Entry.Worktree, "task_id", taskID, "reason", reason)
	return nil
}

// operationContext returns a context bounded by both the given timeout and
// the supervisor's Shutdown channel, so a slow backend call doesn't outlive
// supervisor shutdown.
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

func (s *Supervisor) setPreflightError(ap *AgentProcess, class agenterr.ErrorClass, message string) {
	ap.Mu.Lock()
	ap.LastExitCode = 0
	ap.LastExit = time.Now()
	ap.LastError = &agenterr.AgentError{Class: class, Message: message}
	ap.LastNoWork = class == agenterr.NoWork
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
