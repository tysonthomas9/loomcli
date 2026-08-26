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
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// actorClaimBackend is the optional richer claim API: when the issue backend
// implements it, claims are recorded against the agent's worktree identifier
// rather than the generic process actor.
type actorClaimBackend interface {
	ClaimIssueAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error
}

// deregisterWorker removes the worker registration on graceful exit. The
// server-side TTL remains the backstop for non-graceful death.
func (s *Supervisor) deregisterWorker(ap *AgentProcess) {
	if s.ControlStore == nil || s.WorkspaceID == "" || ap.Entry.Worktree == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if err := s.ControlStore.Workers().Deregister(ctx, s.WorkspaceID, ap.Entry.Worktree); err != nil {
		slog.Debug("supervisor worker deregister failed",
			"workspace", s.WorkspaceID, "worker_id", ap.Entry.Worktree, "err", err)
	}
}

// handleSpawnError unwinds state created before a subprocess failed to start.
// Keeping this cleanup out of spawnAndWait makes its single lifecycle path
// visible without obscuring the pre-exec rollback boundary.
func (s *Supervisor) handleSpawnError(ap *AgentProcess, spawnErr error) {
	if errors.Is(spawnErr, ErrBackendUnavailable) {
		// This is a race guard for the backend disappearing after claim and
		// before exec. Release all acquired state so the task is claimable again.
		s.completeBackendUnavailableCleanup(ap)
		s.Concurrency.Release(ap.Entry.Role)
		return
	}
	slog.Warn("spawn failed", "worktree", ap.Entry.Worktree, "err", spawnErr)
	ap.Mu.Lock()
	orphanSess := ap.Session
	ap.Session = nil
	orphanSessionID := ap.AgentSessionID
	ap.AgentSessionID = ""
	orphanLeaseID := ap.AgentLeaseID
	orphanLeaseToken := ap.AgentLeaseToken
	ap.AgentLeaseID = ""
	ap.AgentLeaseToken = ""
	ap.Mu.Unlock()
	if orphanSess != nil {
		_ = orphanSess.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "spawn_failure"})
	}
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:  orphanSessionID,
		leaseID:    orphanLeaseID,
		leaseToken: orphanLeaseToken,
		exitCode:   -1,
		errClass:   "spawn_failure",
		taskID:     s.taskIDForLifecycle(ap, nil),
	})
	s.Concurrency.Release(ap.Entry.Role)
	s.markSpawnFailure(ap, spawnErr)
}

// postMortemRecovery runs recovery after agent exit, skipping yield exits.
func (s *Supervisor) postMortemRecovery(ap *AgentProcess, exitCode int) {
	if IsYieldRequested(ap.WorktreePath) {
		slog.Info("skipping post-mortem recovery for yield exit", "worktree", ap.Entry.Worktree)
		return
	}
	if err := s.recoverAgent(ap, exitCode, isIncompleteRun(ap)); err != nil {
		slog.Warn("post-mortem recovery failed", "worktree", ap.Entry.Worktree, "err", err)
	}
}

// postExitCleanup is the hook point for future cleanup after spawnAndWait.
func (s *Supervisor) postExitCleanup(_ *AgentProcess) {}

// TaskWorktreeManager is the supervisor's preparation seam. Its implementation
// owns task branch naming, dependency ancestry and checkout materialization.
type TaskWorktreeManager interface {
	Prepare(context.Context, TaskWorktreeRequest) (TaskWorktree, error)
	Publish(context.Context, TaskWorktreePublishRequest) (TaskWorktreeRevision, error)
}

type TaskWorktreeLease interface{ Release() error }

type TaskWorktreeRequest struct {
	WorkspacePath     string
	WorkspaceKey      string
	RepoName          string
	RepoPath          string
	TaskID            string
	Remote            string
	DefaultBranch     string
	DependencyTaskIDs []string
	AllowDirtyResume  bool
}

type TaskWorktree struct {
	Path, Branch, InputSHA, TreeSHA string
	Lease                           TaskWorktreeLease
}

type TaskWorktreePublishRequest struct {
	WorkspaceKey, RepoPath, TaskID, Path, Branch, InputSHA string
}

type TaskWorktreeRevision struct{ HeadSHA, TreeSHA string }

func (s *Supervisor) prepareClaimedTaskWorktree(ctx context.Context, ap *AgentProcess) error {
	if s.TaskWorktrees == nil || ap.AssignedTaskID == "" {
		return nil
	}
	repo := ap.RepoConfig
	var requiredDependencies []string
	if s.IssueBackend != nil {
		issue, err := s.IssueBackend.Get(ctx, ap.AssignedTaskID)
		if err != nil {
			return fmt.Errorf("read task %q dependencies: %w", ap.AssignedTaskID, err)
		}
		requiredDependencies = dependencyTaskIDs(issue)
		repo, err = resolveTaskRepo(repo, issue.SourceRepo, s.FindRepoConfig)
		if err != nil {
			return err
		}
	}
	if repo == nil {
		return fmt.Errorf("task %q has no resolved repository for its worktree", ap.AssignedTaskID)
	}
	prepared, err := s.TaskWorktrees.Prepare(ctx, TaskWorktreeRequest{
		WorkspacePath:     s.ProjectDir,
		WorkspaceKey:      s.WorkspaceID,
		RepoName:          repo.Name,
		RepoPath:          repo.ResolveAbsPath(s.ProjectDir),
		TaskID:            ap.AssignedTaskID,
		Remote:            repo.Remote,
		DefaultBranch:     repo.DefaultBranch,
		DependencyTaskIDs: requiredDependencies,
		AllowDirtyResume:  ap.RecoveryMode == recoverResume || ap.RecoveryMode == recoverCheckpoint,
	})
	if err != nil {
		return err
	}
	ap.Mu.Lock()
	ap.RepoConfig = repo
	ap.WorktreePath = prepared.Path
	ap.TaskBranch = prepared.Branch
	ap.TaskInputSHA = prepared.InputSHA
	ap.TaskTreeSHA = prepared.TreeSHA
	ap.TaskOutputSHA = ""
	ap.TaskOutputTreeSHA = ""
	ap.TaskRepoName = repo.Name
	ap.TaskSourceRepoID = repo.SourceRepoID
	ap.TaskWorktreeLease = prepared.Lease
	ap.Mu.Unlock()
	return nil
}

func releaseTaskWorktreeLease(ap *AgentProcess) {
	ap.Mu.Lock()
	lease := ap.TaskWorktreeLease
	ap.TaskWorktreeLease = nil
	ap.Mu.Unlock()
	if lease != nil {
		_ = lease.Release()
	}
}

func resolveTaskRepo(current *config.RepoConfig, sourceRepo string, find func(string) *config.RepoConfig) (*config.RepoConfig, error) {
	if sourceRepo == "" {
		return current, nil
	}
	if current != nil && (current.Name == sourceRepo || current.SourceRepoID == sourceRepo) {
		return current, nil
	}
	if find != nil {
		if resolved := find(sourceRepo); resolved != nil {
			return resolved, nil
		}
	}
	return nil, fmt.Errorf("claimed task source repository %q is not configured", sourceRepo)
}

func dependencyTaskIDs(issue *backend.IssueDetailData) []string {
	if issue == nil {
		return nil
	}
	ids := make([]string, 0, len(issue.Dependencies))
	for _, dependency := range issue.Dependencies {
		if dependency.Type == "blocks" && dependency.DependsOnID != "" {
			ids = append(ids, dependency.DependsOnID)
		}
	}
	return ids
}

func (s *Supervisor) shouldPublishTaskDelivery(ctx context.Context, ap *AgentProcess) (bool, error) {
	hooks := s.currentCompletionHooks(ap)
	if !completionHooksRequireDeliveryFence(hooks) {
		return true, nil
	}
	if s.IssueBackend == nil {
		return false, fmt.Errorf("delivery-fenced task has no issue backend")
	}
	issue, err := s.IssueBackend.Get(ctx, ap.AssignedTaskID)
	if err != nil {
		return false, fmt.Errorf("verify successful delivery marker: %w", err)
	}
	return issueHasLabel(issue, "delivery-pending"), nil
}

func (s *Supervisor) publishTaskWorktree(ctx context.Context, ap *AgentProcess) error {
	if s.TaskWorktrees == nil || ap.TaskBranch == "" {
		return nil
	}
	revision, err := s.TaskWorktrees.Publish(ctx, TaskWorktreePublishRequest{
		WorkspaceKey: s.WorkspaceID,
		RepoPath:     ap.RepoConfig.ResolveAbsPath(s.ProjectDir),
		TaskID:       ap.AssignedTaskID,
		Path:         ap.WorktreePath,
		Branch:       ap.TaskBranch,
		InputSHA:     ap.TaskInputSHA,
	})
	if err != nil {
		return err
	}
	ap.Mu.Lock()
	ap.TaskOutputSHA = revision.HeadSHA
	ap.TaskOutputTreeSHA = revision.TreeSHA
	ap.Mu.Unlock()
	return nil
}

func detachPublishedTaskWorktree(ap *AgentProcess) {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.TaskOutputSHA == "" || ap.AgentWorktreePath == "" {
		return
	}
	ap.WorktreePath = ap.AgentWorktreePath
	ap.TaskBranch = ""
	ap.TaskInputSHA = ""
	ap.TaskTreeSHA = ""
	ap.TaskOutputSHA = ""
	ap.TaskOutputTreeSHA = ""
	ap.TaskRepoName = ""
	ap.TaskSourceRepoID = ""
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
		ap.RecoveryMode = recoverCold
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
	ap.Mu.Unlock()
	slog.Info("claimed task for agent", "worktree", ap.Entry.Worktree, "task_id", taskID, "reason", reason)
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
