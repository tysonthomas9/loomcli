package monitor

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func CollectMonitorData(readyLimit int, branch string) *MonitorData {
	// Copy cli.GetDeps(nil) and override IssueBackend with the global backend
	// (respects setDefaultIssueBackend used by tests).
	d := *cli.GetDeps(nil)
	d.IssueBackend = cli.DefaultIssueBackend()
	return collectMonitorDataDeps(&d, readyLimit, branch)
}

func CollectMonitorDataWithIssueBackend(issueBackend backend.IssueBackend, readyLimit int, branch string) *MonitorData {
	d := *cli.GetDeps(nil)
	if issueBackend != nil {
		d.IssueBackend = issueBackend
	} else {
		d.IssueBackend = cli.DefaultIssueBackend()
	}
	return collectMonitorDataDeps(&d, readyLimit, branch)
}

func collectMonitorDataDeps(deps *cli.Deps, readyLimit int, branch string) *MonitorData {
	data := &MonitorData{Timestamp: time.Now()}

	// Start stats and store sync collection in parallel with task collection.
	var (
		stats    MonitorStats
		syncInfo SyncInfo
		wg       sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		stats = collectStatisticsDeps(deps)
	}()

	go func() {
		defer wg.Done()
		syncInfo = collectStoreSyncStatusDeps(deps)
	}()

	// Collect tasks (internally parallel) to get agent-task mapping
	data.Tasks, data.NeedsPlanningTasks, data.ReadyToImplement, data.ReviewTasks, data.InProgressTasks, data.BacklogTasks, data.ClosedTasks, data.AgentTasks = collectTaskStatusDeps(deps, readyLimit)

	// Collect agents, passing the task map for fallback lookup
	var taskIDToAgents map[string][]string
	data.Agents, taskIDToAgents = collectAgentStatusDeps(deps, data.AgentTasks, branch)

	// Detect task conflicts (multiple agents claiming same task)
	data.TaskConflicts = make(map[string][]string)
	for taskID, agents := range taskIDToAgents {
		if len(agents) > 1 {
			data.TaskConflicts[taskID] = agents
		}
	}

	// Wait for stats and store sync collection to finish.
	wg.Wait()

	// Combine store sync result with agent data for git push/pull counts.
	data.SyncStatus = completeSyncStatus(syncInfo, data.Agents)
	data.Stats = stats

	return data
}

func collectAgentStatus(agentTasks map[string]TaskInfo, branch string) ([]AgentStatus, map[string][]string) {
	d := *cli.GetDeps(nil)
	d.IssueBackend = cli.DefaultIssueBackend()
	return collectAgentStatusDeps(&d, agentTasks, branch)
}

func collectAgentStatusDeps(deps *cli.Deps, agentTasks map[string]TaskInfo, branch string) ([]AgentStatus, map[string][]string) {
	allWorktrees, err := cli.DiscoverAgentWorktrees()
	if err != nil {
		allWorktrees = nil
	}
	if len(allWorktrees) == 0 {
		allWorktrees, err = cli.DiscoverWorktrees()
		if err != nil {
			return nil, nil
		}
	}
	// The workspace config may list both source repos and agent worktrees.
	// Exclude source repos when linked worktrees exist.
	hasLinked := false
	for _, wt := range allWorktrees {
		if wt.IsLinkedWorktree {
			hasLinked = true
			break
		}
	}
	worktrees := allWorktrees
	if hasLinked {
		worktrees = make([]cli.WorktreeInfo, 0, len(allWorktrees))
		for _, wt := range allWorktrees {
			if wt.IsLinkedWorktree {
				worktrees = append(worktrees, wt)
			}
		}
	}

	var daemonManaged map[string]DaemonAgentInfo
	if projectDir, err2 := os.Getwd(); err2 == nil {
		daemonStatePath := config.ResolveDaemonStatePath(projectDir)
		daemonManaged = LoadDaemonManagedAgents(daemonStatePath)
	}

	taskIDToAgents := make(map[string][]string)
	globalDefaultBranch := cli.GetDefaultBranchForWorktrees(worktrees)
	githubURL := ""
	if len(worktrees) > 0 && worktrees[0].Repo == nil {
		githubURL = getGitHubRemoteURLDeps(deps, worktrees[0].Path)
	}

	agents := make([]AgentStatus, 0, len(worktrees))
	for _, wt := range worktrees {
		agent := buildAgentStatus(deps, wt, daemonManaged, agentTasks, taskIDToAgents, globalDefaultBranch, githubURL, branch)
		agents = append(agents, agent)
	}
	return agents, taskIDToAgents
}

// buildAgentStatus constructs the status for a single worktree agent.
func buildAgentStatus(deps *cli.Deps, wt cli.WorktreeInfo, daemonManaged map[string]DaemonAgentInfo, agentTasks map[string]TaskInfo, taskIDToAgents map[string][]string, globalDefaultBranch, githubURL, branch string) AgentStatus {
	daemonInfo := daemonManaged[wt.Name]
	agent := AgentStatus{
		Name: wt.Name, Branch: wt.Branch, Workspace: wt.Workspace,
		Role: daemonInfo.Role, Repo: daemonInfo.Repo, DaemonManaged: daemonInfo.Managed,
		CurrentTaskID: daemonInfo.CurrentTaskID,
	}
	if !daemonInfo.LastActivity.IsZero() {
		la := daemonInfo.LastActivity
		agent.LastActivityAt = &la
	}

	if lockInfo, running, _ := cli.CheckLock(wt.Path); running && lockHasActiveTaskClaim(lockInfo) {
		taskIDToAgents[lockInfo.TaskID] = append(taskIDToAgents[lockInfo.TaskID], wt.Name)
		// Daemon state only records CurrentTaskID for daemon-managed agents;
		// auto/manual agents leave it empty. Fall back to the lock's claimed
		// task when the lock is actively executing. Idle auto-mode locks may
		// retain a stale TaskID for no-progress detection and should not satisfy
		// the kanban's live-agent join.
		if agent.CurrentTaskID == "" {
			agent.CurrentTaskID = lockInfo.TaskID
		}
	}

	var idleChanges []FileChange
	var idleHandled bool
	agent.Status, idleChanges, idleHandled = resolveAgentStatus(deps, wt, agentTasks)
	if daemonInfo.Managed && daemonInfo.Status == "error" {
		agent.Status = "error"
	}

	wtDefaultBranch := globalDefaultBranch
	if wt.Repo != nil {
		wtDefaultBranch = cli.DefaultBranchForWorktree(wt)
	}
	agent.Ahead, agent.Behind = collectAgentAheadBehind(deps, wt, agent.Branch, wtDefaultBranch, branch)

	if agent.Ahead > 0 {
		wtGithubURL := githubURL
		if wt.Repo != nil {
			wtGithubURL = getGitHubRemoteURLDeps(deps, wt.Path)
		}
		agent.Commits = collectAgentCommits(deps, wt, agent.Branch, wtDefaultBranch, wtGithubURL, branch)
	}
	if idleHandled {
		agent.Changes = idleChanges
	} else {
		agent.Changes = getWorktreeFileChangesDeps(deps, wt.Path)
	}
	return agent
}

func lockHasActiveTaskClaim(lockInfo *cli.LockInfo) bool {
	return lockInfo != nil && lockInfo.TaskID != "" && lockInfo.State != cli.StateIdle
}

// resolveAgentStatus determines the status string for a worktree.
// When the worktree is idle (no lock, no in-progress task), it also returns
// the file changes derived from the same git status invocation, with idle=true,
// so the caller can avoid a redundant git subprocess.
func resolveAgentStatus(deps *cli.Deps, wt cli.WorktreeInfo, agentTasks map[string]TaskInfo) (status string, changes []FileChange, idle bool) {
	lockStatus := cli.GetLockStatus(wt.Path)
	if lockStatus != "" {
		return refineLockStatus(deps, lockStatus, wt.Name, agentTasks), nil, false
	}
	if task, ok := agentTasks[wt.Name]; ok && task.Status == "in_progress" {
		return fmt.Sprintf("error: %s", task.ID), nil, false
	}
	s, c := resolveIdleStatus(deps, wt.Path)
	return s, c, true
}

// refineLockStatus enriches a lock status with task details when needed.
func refineLockStatus(deps *cli.Deps, lockStatus, agentName string, agentTasks map[string]TaskInfo) string {
	if !strings.Contains(lockStatus, "...") && !strings.HasPrefix(lockStatus, "idle ") {
		return lockStatus
	}
	task, ok := agentTasks[agentName]
	if !ok {
		return lockStatus
	}
	taskStatus := cli.GetTaskStatusDeps(deps, task.ID)
	durationPart := ""
	if idx := strings.Index(lockStatus, " ("); idx != -1 {
		durationPart = lockStatus[idx:]
	}
	switch taskStatus {
	case "needs_review":
		if strings.HasPrefix(lockStatus, "planning:") {
			return fmt.Sprintf("review: %s%s", task.ID, durationPart)
		}
		return fmt.Sprintf("working: %s%s", task.ID, durationPart)
	case "closed":
		return fmt.Sprintf("done: %s%s", task.ID, durationPart)
	default:
		if strings.HasPrefix(lockStatus, "idle ") {
			return fmt.Sprintf("working: %s%s", task.ID, durationPart)
		}
		return strings.Replace(lockStatus, "...", task.ID, 1)
	}
}

// resolveIdleStatus determines the status for an agent with no lock and no in-progress task.
// It also returns the file changes derived from the single git status invocation
// so the caller can avoid running git status a second time.
//
// Tries the filesystem-level change detector first (zero subprocess cost on hit).
// Falls through to getWorktreeStatus on cache miss or when the gitdir cannot be resolved.
func resolveIdleStatus(deps *cli.Deps, wtPath string) (string, []FileChange) {
	if gitDir, err := resolveGitDir(wtPath); err == nil {
		hit, cachedClean, cachedCount, cachedChanges, headMtime, indexMtime := globalChangeDetector.CheckStatus(gitDir)
		if hit {
			return idleStatusFromValues(cachedClean, cachedCount, cachedChanges)
		}
		clean, count, fileChanges := getWorktreeStatus(deps, wtPath)
		// Store under the mtimes observed before the subprocess ran so a
		// concurrent index update doesn't get our stale snapshot keyed under
		// its newer mtime.
		globalChangeDetector.UpdateStatus(gitDir, headMtime, indexMtime, clean, count, fileChanges)
		return idleStatusFromValues(clean, count, fileChanges)
	}
	clean, count, fileChanges := getWorktreeStatus(deps, wtPath)
	return idleStatusFromValues(clean, count, fileChanges)
}

// idleStatusFromValues formats the (clean, count, changes) triple into the
// (statusString, returnedChanges) pair expected by resolveIdleStatus. When the
// worktree is clean, file changes are nil — preserving the original semantics.
func idleStatusFromValues(clean bool, count int, fileChanges []FileChange) (string, []FileChange) {
	if clean {
		return "ready", nil
	}
	if count > 0 {
		return fmt.Sprintf("%d changes", count), fileChanges
	}
	return "dirty", fileChanges
}

// collectAgentAheadBehind returns the (ahead, behind) counts for an agent's
// branch vs its integration branch, consulting the filesystem ref-SHA cache
// first to avoid spawning git when nothing has changed.
func collectAgentAheadBehind(deps *cli.Deps, wt cli.WorktreeInfo, agentBranch, defaultBranch, overrideBranch string) (ahead, behind int) {
	abBranch := overrideBranch
	if abBranch == "" {
		abBranch = defaultBranch
	}
	gitDir, err := resolveGitDir(wt.Path)
	if err != nil {
		return GetWorktreeGitSyncStatusDeps(deps, wt.Path, defaultBranch, overrideBranch)
	}
	commonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		return GetWorktreeGitSyncStatusDeps(deps, wt.Path, defaultBranch, overrideBranch)
	}
	hit, cachedAhead, cachedBehind, localSHA, remoteSHA := globalChangeDetector.CheckAheadBehind(commonDir, agentBranch, abBranch)
	if hit {
		return cachedAhead, cachedBehind
	}
	ahead, behind = GetWorktreeGitSyncStatusDeps(deps, wt.Path, defaultBranch, overrideBranch)
	// Store under the SHAs observed before the subprocess ran so a ref advance
	// during the rev-list call doesn't pin a stale count under the new SHA.
	globalChangeDetector.UpdateAheadBehind(commonDir, agentBranch, abBranch, localSHA, remoteSHA, ahead, behind)
	return ahead, behind
}

// collectAgentCommits returns the recent commit details for an agent, using
// the global commit cache keyed by the local branch's HEAD SHA so that
// unchanged branches return cached results without spawning git log.
func collectAgentCommits(deps *cli.Deps, wt cli.WorktreeInfo, agentBranch, defaultBranch, githubURL, overrideBranch string) []CommitDetail {
	gitDir, err := resolveGitDir(wt.Path)
	if err != nil {
		return getWorktreeCommitDetailsDeps(deps, wt.Path, defaultBranch, 10, githubURL, overrideBranch)
	}
	commonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		return getWorktreeCommitDetailsDeps(deps, wt.Path, defaultBranch, 10, githubURL, overrideBranch)
	}
	headSHA, err := ReadRefSHA(commonDir, "refs/heads/"+agentBranch)
	if err != nil {
		return getWorktreeCommitDetailsDeps(deps, wt.Path, defaultBranch, 10, githubURL, overrideBranch)
	}
	// The commit list depends on `origin/<integrationBranch>..HEAD`, the
	// integration-branch selection, and the github URL embedded in each
	// CommitDetail — all participate in the cache key.
	integrationBranch := overrideBranch
	if integrationBranch == "" {
		integrationBranch = defaultBranch
	}
	remoteSHA, _ := ReadRefSHA(commonDir, "refs/remotes/origin/"+integrationBranch)
	key := commitCacheKey(headSHA, remoteSHA, integrationBranch, githubURL)
	if commits, ok := globalCommitCache.Get(key); ok {
		return commits
	}
	commits := getWorktreeCommitDetailsDeps(deps, wt.Path, defaultBranch, 10, githubURL, overrideBranch)
	globalCommitCache.Set(key, commits)
	return commits
}

func collectTaskStatus(readyLimit int) (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
	d := *cli.GetDeps(nil)
	d.IssueBackend = cli.DefaultIssueBackend()
	return collectTaskStatusDeps(&d, readyLimit)
}

// taskQueryResults holds the raw results from parallel issue queries.
type taskQueryResults struct {
	readyIssues, inProgressIssues, reviewIssues, backlogIssues, closedIssues []backend.IssueData
	readyErr, inProgressErr, reviewErr, backlogErr, closedErr                error
}

func collectTaskStatusDeps(deps *cli.Deps, readyLimit int) (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
	qr := runParallelTaskQueries(deps, readyLimit)

	var summary TaskSummary
	agentTasks := make(map[string]TaskInfo)

	// Defense-in-depth: build a set of blocked issue IDs from the Blocked() query.
	// If a ready issue also appears in the blocked set, skip it.
	var blockedIDs map[string]bool
	if qr.backlogErr == nil {
		blockedIDs = make(map[string]bool, len(qr.backlogIssues))
		for _, issue := range qr.backlogIssues {
			blockedIDs[issue.ID] = true
		}
	}

	needsPlanningTasks, readyToImplementTasks := processReadyIssues(qr.readyIssues, qr.readyErr, &summary, blockedIDs)
	inProgressTasks := processInProgressIssues(qr.inProgressIssues, qr.inProgressErr, &summary, agentTasks)
	reviewTasks := processReviewIssues(qr.reviewIssues, qr.reviewErr, &summary)
	backlogTasks := processBacklogIssues(qr.backlogIssues, qr.backlogErr, &summary)
	closedTasks := processClosedIssues(qr.closedIssues, qr.closedErr)

	return summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, backlogTasks, closedTasks, agentTasks
}

func runParallelTaskQueries(deps *cli.Deps, readyLimit int) taskQueryResults {
	var qr taskQueryResults
	var wg sync.WaitGroup
	ib := deps.IssueBackend
	ctx := cmdstore.RootContext()

	wg.Add(5)
	go func() {
		defer wg.Done()
		qr.readyIssues, qr.readyErr = ib.Ready(ctx, backend.ReadyOpts{Limit: readyLimit})
	}()
	// Pass an explicit large limit so the monitor counts and displayed slices
	// reflect the full queue.
	go func() {
		defer wg.Done()
		qr.inProgressIssues, qr.inProgressErr = ib.List(ctx, backend.ListOpts{Status: "in_progress", Limit: 10000})
	}()
	go func() {
		defer wg.Done()
		qr.reviewIssues, qr.reviewErr = ib.List(ctx, backend.ListOpts{Status: "review", Limit: 10000})
	}()
	go func() { defer wg.Done(); qr.backlogIssues, qr.backlogErr = ib.Blocked(ctx, backend.BlockedOpts{}) }()
	go func() {
		defer wg.Done()
		qr.closedIssues, qr.closedErr = ib.List(ctx, backend.ListOpts{Status: "closed", Limit: 50})
	}()
	wg.Wait()

	return qr
}

func processReadyIssues(issues []backend.IssueData, err error, summary *TaskSummary, blockedIDs map[string]bool) ([]TaskInfo, []TaskInfo) {
	if err != nil {
		return nil, nil
	}
	var needsPlanning, readyToImpl []TaskInfo
	for _, issue := range issues {
		if !cli.IsOpen(issue) {
			continue
		}
		if cli.IsEpic(issue) {
			summary.Epics++
			continue
		}
		if cli.IsNonWorkType(issue) {
			continue
		}
		// Defense-in-depth: skip issues that the Blocked() query identifies as
		// blocked, even if the ready query returned them.
		if blockedIDs != nil && blockedIDs[issue.ID] {
			continue
		}
		ti := TaskInfo{ID: issue.ID, Title: issue.Title, Priority: issue.Priority}
		if cli.ReadyToImplement(issue) {
			summary.ReadyToImplement++
			if len(readyToImpl) < 5 {
				readyToImpl = append(readyToImpl, ti)
			}
		} else {
			summary.NeedsPlanning++
			if len(needsPlanning) < 5 {
				needsPlanning = append(needsPlanning, ti)
			}
		}
	}
	return needsPlanning, readyToImpl
}

func processInProgressIssues(issues []backend.IssueData, err error, summary *TaskSummary, agentTasks map[string]TaskInfo) []TaskInfo {
	if err != nil {
		return nil
	}
	summary.InProgress = len(issues)
	var tasks []TaskInfo
	for _, issue := range issues {
		ti := TaskInfo{ID: issue.ID, Title: issue.Title, Priority: issue.Priority, Status: "in_progress"}
		tasks = append(tasks, ti)
		if issue.Assignee != "" {
			agentTasks[issue.Assignee] = ti
		}
	}
	return tasks
}

func processReviewIssues(issues []backend.IssueData, err error, summary *TaskSummary) []TaskInfo {
	if err != nil {
		return nil
	}
	summary.NeedReview = len(issues)
	var tasks []TaskInfo
	for i, issue := range issues {
		if i >= 5 {
			break
		}
		tasks = append(tasks, TaskInfo{ID: issue.ID, Title: issue.Title, Priority: issue.Priority})
	}
	return tasks
}

func processBacklogIssues(issues []backend.IssueData, err error, summary *TaskSummary) []TaskInfo {
	if err != nil {
		return nil
	}
	summary.Backlog += len(issues)
	var tasks []TaskInfo
	for i, issue := range issues {
		if i >= 20 {
			break
		}
		tasks = append(tasks, TaskInfo{ID: issue.ID, Title: issue.Title, Priority: issue.Priority, Status: issue.Status})
	}
	return tasks
}

func processClosedIssues(issues []backend.IssueData, err error) []TaskInfo {
	if err != nil {
		return nil
	}
	var tasks []TaskInfo
	for i, issue := range issues {
		if i >= 50 {
			break
		}
		tasks = append(tasks, TaskInfo{ID: issue.ID, Title: issue.Title, Priority: issue.Priority, Status: issue.Status})
	}
	return tasks
}

// collectStoreSyncStatus reports the active issue store sync state. FleetDB no
// longer shells out to a local issue tracker; remote sync health is represented
// by the store/client calls used elsewhere in monitor collection.
func collectStoreSyncStatus() SyncInfo {
	return collectStoreSyncStatusDeps(cli.GetDeps(nil))
}

func collectStoreSyncStatusDeps(_ *cli.Deps) SyncInfo {
	return SyncInfo{
		DBSynced:   true,
		DBLastSync: "live",
	}
}

// completeSyncStatus combines store sync health with agent git push/pull counts.
func completeSyncStatus(info SyncInfo, agents []AgentStatus) SyncInfo {
	for _, agent := range agents {
		if agent.Ahead > 0 {
			info.GitNeedsPush++
			info.GitPushDetails = append(info.GitPushDetails, WorktreeSyncDetail{
				Name:  agent.Name,
				Count: agent.Ahead,
			})
		}
		if agent.Behind > 0 {
			info.GitNeedsPull++
			info.GitPullDetails = append(info.GitPullDetails, WorktreeSyncDetail{
				Name:  agent.Name,
				Count: agent.Behind,
			})
		}
	}
	return info
}

// collectSyncStatus is the original sequential version, kept for external callers.
func collectSyncStatus(agents []AgentStatus) SyncInfo {
	info := collectStoreSyncStatus()
	return completeSyncStatus(info, agents)
}

func collectStatistics() MonitorStats {
	d := *cli.GetDeps(nil)
	d.IssueBackend = cli.DefaultIssueBackend()
	return collectStatisticsDeps(&d)
}

func collectStatisticsDeps(deps *cli.Deps) MonitorStats {
	var stats MonitorStats

	statsData, err := deps.IssueBackend.Stats(cmdstore.RootContext())
	if err == nil && statsData != nil {
		stats.Open = statsData.OpenIssues
		stats.Closed = statsData.ClosedIssues
		stats.Total = statsData.TotalIssues
		stats.InProgress = statsData.InProgressIssues
		stats.Blocked = statsData.BlockedIssues
		if stats.Total > 0 {
			stats.Completion = float64(stats.Closed) / float64(stats.Total) * 100
		}

		// Remaining = total - closed
		stats.Remaining = stats.Total - stats.Closed
		if stats.Remaining < 0 {
			stats.Remaining = 0
		}

		// Review = total - open - inProgress - closed - blocked - deferred - pinned
		stats.Review = stats.Total - stats.Open - stats.InProgress - stats.Closed -
			stats.Blocked - statsData.DeferredIssues - statsData.PinnedIssues
		if stats.Review < 0 {
			stats.Review = 0
		}
	}

	return stats
}

// collectReadyTasksByPriority returns counts of ready tasks grouped by priority (0-4).
// It iterates ready tasks (excluding epics, in_progress, and review) and returns
// a map of priority -> count for Prometheus metrics.
func CollectReadyTasksByPriority(readyLimit int) map[int]int {
	counts := make(map[int]int)
	// Initialize all priorities to 0
	for i := 0; i <= 4; i++ {
		counts[i] = 0
	}

	issues, err := cli.DefaultIssueBackend().Ready(cmdstore.RootContext(), backend.ReadyOpts{Limit: readyLimit})
	if err != nil {
		return counts
	}

	for _, issue := range issues {
		if !cli.IsOpen(issue) {
			continue
		}
		if cli.IsEpic(issue) {
			continue
		}
		if cli.IsNonWorkType(issue) {
			continue
		}
		// Skip tasks with needs-revision label (these are being re-planned)
		if cli.HasNeedsRevision(issue) {
			continue
		}
		p := issue.Priority
		if p < 0 || p > 4 {
			p = 4
		}
		counts[p]++
	}

	return counts
}
