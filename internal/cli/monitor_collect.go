package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

func collectMonitorData(readyLimit int, branch string) *MonitorData {
	// Copy defaultDeps and override Tracker with the global tracker
	// (respects setDefaultTracker used by tests).
	d := *defaultDeps
	d.Tracker = defaultTracker()
	return collectMonitorDataDeps(&d, readyLimit, branch)
}

func collectMonitorDataDeps(deps *Deps, readyLimit int, branch string) *MonitorData {
	data := &MonitorData{Timestamp: time.Now()}

	// Start stats and sync bd call in parallel with task collection
	var (
		stats      MonitorStats
		syncBdInfo SyncInfo
		wg         sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		stats = collectStatisticsDeps(deps)
	}()

	go func() {
		defer wg.Done()
		syncBdInfo = collectSyncBdStatusDeps(deps)
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

	// Wait for stats and sync bd call to finish
	wg.Wait()

	// Combine sync bd result with agent data for git push/pull counts
	data.SyncStatus = completeSyncStatus(syncBdInfo, data.Agents)
	data.Stats = stats

	// Compute Remaining as the sum of work queue categories so it always tallies.
	// bd stats computes Remaining by subtraction which can disagree with the
	// work queue counts due to issues falling between bd ready / bd blocked.
	data.Stats.Remaining = data.Tasks.NeedsPlanning + data.Tasks.ReadyToImplement +
		data.Tasks.NeedReview + data.Tasks.InProgress + data.Tasks.Backlog
	data.Stats.Total = data.Stats.Remaining + data.Stats.Closed
	if data.Stats.Total > 0 {
		data.Stats.Completion = float64(data.Stats.Closed) / float64(data.Stats.Total) * 100
	} else {
		data.Stats.Completion = 0
	}

	return data
}

func collectAgentStatus(agentTasks map[string]TaskInfo, branch string) ([]AgentStatus, map[string][]string) {
	d := *defaultDeps
	d.Tracker = defaultTracker()
	return collectAgentStatusDeps(&d, agentTasks, branch)
}

func collectAgentStatusDeps(deps *Deps, agentTasks map[string]TaskInfo, branch string) ([]AgentStatus, map[string][]string) {
	worktrees, err := DiscoverWorktrees()
	if err != nil {
		return nil, nil
	}

	// Load daemon-managed agents (if any)
	var daemonManaged map[string]DaemonAgentInfo
	if projectDir, err2 := os.Getwd(); err2 == nil {
		daemonStatePath := ResolveDaemonStatePath(projectDir)
		daemonManaged = loadDaemonManagedAgents(daemonStatePath)
	}

	var agents []AgentStatus
	taskIDToAgents := make(map[string][]string) // Track which agents claim which tasks

	// Global defaults for legacy mode; per-worktree values resolved in loop for workspace mode.
	globalDefaultBranch := GetDefaultBranchForWorktrees(worktrees)
	githubURL := ""
	if len(worktrees) > 0 && worktrees[0].Repo == nil {
		githubURL = getGitHubRemoteURLDeps(deps, worktrees[0].Path)
	}

	for _, wt := range worktrees {
		daemonInfo := daemonManaged[wt.Name]
		agent := AgentStatus{
			Name:          wt.Name,
			Branch:        wt.Branch,
			Workspace:     wt.Workspace,
			Role:          daemonInfo.Role,
			Repo:          daemonInfo.Repo,
			DaemonManaged: daemonInfo.Managed,
		}

		// Check for running agent (lock status)
		lockStatus := GetLockStatus(wt.Path)

		// Also check lock file directly to get TaskID for conflict detection
		if lockInfo, running, _ := CheckLock(wt.Path); running && lockInfo != nil && lockInfo.TaskID != "" {
			taskIDToAgents[lockInfo.TaskID] = append(taskIDToAgents[lockInfo.TaskID], wt.Name)
		}

		if lockStatus != "" {
			// Lock file has status - check if it needs task ID from fallback
			if strings.Contains(lockStatus, "...") {
				if task, ok := agentTasks[wt.Name]; ok {
					// Get actual task status to determine correct state
					taskStatus := getTaskStatusDeps(deps, task.ID)
					// Extract duration part (e.g., " (2m8s)")
					durationIdx := strings.Index(lockStatus, " (")
					durationPart := ""
					if durationIdx != -1 {
						durationPart = lockStatus[durationIdx:]
					}
					// Update state based on task status and agent type
					switch taskStatus {
					case "needs_review":
						// Only show "review" for planning agents
						if strings.HasPrefix(lockStatus, "planning:") {
							lockStatus = fmt.Sprintf("review: %s%s", task.ID, durationPart)
						} else {
							// Implementation agents show "working"
							lockStatus = fmt.Sprintf("working: %s%s", task.ID, durationPart)
						}
					case "closed":
						lockStatus = fmt.Sprintf("done: %s%s", task.ID, durationPart)
					default:
						// Keep original state prefix, just replace "..."
						lockStatus = strings.Replace(lockStatus, "...", task.ID, 1)
					}
				}
			}
			agent.Status = lockStatus
		} else if task, ok := agentTasks[wt.Name]; ok && task.Status == "in_progress" {
			// Task still in_progress but no lock - agent died
			agent.Status = fmt.Sprintf("error: %s", task.ID)
		} else {
			// No lock and no in_progress task - check git status
			// (closed tasks don't trigger "done" fallback - "done" only shows while agent is running)
			clean, _ := isCleanWorkingTreeDeps(deps, wt.Path)
			if clean {
				agent.Status = "ready"
			} else {
				changes := getUncommittedChangesCountDeps(deps, wt.Path)
				if changes > 0 {
					agent.Status = fmt.Sprintf("%d changes", changes)
				} else {
					agent.Status = "dirty"
				}
			}
		}

		// Use per-worktree default branch in workspace mode
		wtDefaultBranch := globalDefaultBranch
		if wt.Repo != nil {
			wtDefaultBranch = DefaultBranchForWorktree(wt)
		}

		// Check ahead/behind integration branch
		agent.Ahead, agent.Behind = getWorktreeGitSyncStatusDeps(deps, wt.Path, wtDefaultBranch, branch)

		// Populate commit details when ahead > 0
		if agent.Ahead > 0 {
			wtGithubURL := githubURL
			if wt.Repo != nil {
				wtGithubURL = getGitHubRemoteURLDeps(deps, wt.Path)
			}
			agent.Commits = getWorktreeCommitDetailsDeps(deps, wt.Path, wtDefaultBranch, 10, wtGithubURL, branch)
		}

		// Populate file changes (returns nil for clean trees)
		agent.Changes = getWorktreeFileChangesDeps(deps, wt.Path)

		agents = append(agents, agent)
	}

	return agents, taskIDToAgents
}

func collectTaskStatus(readyLimit int) (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
	d := *defaultDeps
	d.Tracker = defaultTracker()
	return collectTaskStatusDeps(&d, readyLimit)
}

func collectTaskStatusDeps(deps *Deps, readyLimit int) (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
	var summary TaskSummary
	var needsPlanningTasks []TaskInfo
	var readyToImplementTasks []TaskInfo
	var reviewTasks []TaskInfo
	var inProgressTasks []TaskInfo
	var backlogTasks []TaskInfo
	var closedTasks []TaskInfo
	agentTasks := make(map[string]TaskInfo)

	// Run all 5 typed IssueTracker queries in parallel
	var (
		readyIssues, inProgressIssues, reviewIssues, backlogIssues, closedIssues []BdIssue
		readyErr, inProgressErr, reviewErr, backlogErr, closedErr                error
		wg                                                                       sync.WaitGroup
	)

	tracker := deps.Tracker
	ctx := context.Background()

	wg.Add(5)

	go func() {
		defer wg.Done()
		readyIssues, readyErr = tracker.Ready(ctx, ReadyOpts{Limit: readyLimit})
	}()

	go func() {
		defer wg.Done()
		inProgressIssues, inProgressErr = tracker.List(ctx, ListOpts{Status: "in_progress"})
	}()

	go func() {
		defer wg.Done()
		reviewIssues, reviewErr = tracker.List(ctx, ListOpts{Status: "review"})
	}()

	go func() {
		defer wg.Done()
		backlogIssues, backlogErr = tracker.Blocked(ctx)
	}()

	go func() {
		defer wg.Done()
		closedIssues, closedErr = tracker.List(ctx, ListOpts{Status: "closed", Limit: 50})
	}()

	wg.Wait()

	// Process ready tasks, split by workflow stage
	// Note: bd ready returns tasks not blocked by dependencies (open, in_progress, review)
	if readyErr == nil {
		needsPlanningCount := 0
		readyToImplementCount := 0
		for _, issue := range readyIssues {
			// Skip non-open tasks - they appear in their own sections
			if !IsOpen(issue) {
				continue
			}
			if IsEpic(issue) {
				summary.Epics++
				continue
			}
			if IsNonWorkType(issue) {
				continue
			}

			// Split by workflow stage using shared predicates
			// SYNC: Must match taskfilter.go NeedsPlan() / ReadyToImplement()
			if ReadyToImplement(issue) {
				summary.ReadyToImplement++
				if readyToImplementCount < 5 {
					readyToImplementTasks = append(readyToImplementTasks, TaskInfo{
						ID:       issue.ID,
						Title:    issue.Title,
						Priority: issue.Priority,
					})
					readyToImplementCount++
				}
			} else {
				// NeedsPlan: no design OR needs-revision label
				summary.NeedsPlanning++
				if needsPlanningCount < 5 {
					needsPlanningTasks = append(needsPlanningTasks, TaskInfo{
						ID:       issue.ID,
						Title:    issue.Title,
						Priority: issue.Priority,
					})
					needsPlanningCount++
				}
			}
		}
	}

	// Process in_progress tasks and build agent-task map
	if inProgressErr == nil {
		summary.InProgress = len(inProgressIssues)
		for _, issue := range inProgressIssues {
			taskInfo := TaskInfo{
				ID:       issue.ID,
				Title:    issue.Title,
				Priority: issue.Priority,
				Status:   "in_progress",
			}
			inProgressTasks = append(inProgressTasks, taskInfo)
			// Build agent-task map from assignee field
			if issue.Assignee != "" {
				agentTasks[issue.Assignee] = taskInfo
			}
		}
	}

	// Process need review tasks (top 5)
	// Note: Don't add to agentTasks - these tasks have status=review meaning
	// the planning agent finished and released its lock. The assignee field
	// still points to the planning agent but it's no longer running.
	if reviewErr == nil {
		// All tasks with status=review are review tasks
		summary.NeedReview = len(reviewIssues)
		for i, issue := range reviewIssues {
			if i >= 5 {
				break
			}
			reviewTasks = append(reviewTasks, TaskInfo{
				ID:       issue.ID,
				Title:    issue.Title,
				Priority: issue.Priority,
			})
		}
	}

	// Process backlog tasks
	if backlogErr == nil {
		summary.Backlog += len(backlogIssues)
		// Store up to 20 backlog tasks for display
		for i, issue := range backlogIssues {
			if i >= 20 {
				break
			}
			backlogTasks = append(backlogTasks, TaskInfo{
				ID:       issue.ID,
				Title:    issue.Title,
				Priority: issue.Priority,
				Status:   issue.Status,
			})
		}
	}

	// Process closed tasks (top 50)
	if closedErr == nil {
		for i, issue := range closedIssues {
			if i >= 50 {
				break
			}
			closedTasks = append(closedTasks, TaskInfo{
				ID:       issue.ID,
				Title:    issue.Title,
				Priority: issue.Priority,
				Status:   issue.Status,
			})
		}
	}

	return summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, backlogTasks, closedTasks, agentTasks
}

// collectSyncBdStatus runs the bd sync --status command (safe to call concurrently).
// Uses execCommand directly since sync is an infrastructure operation, not an issue query.
func collectSyncBdStatus() SyncInfo {
	return collectSyncBdStatusDeps(defaultDeps)
}

func collectSyncBdStatusDeps(deps *Deps) SyncInfo {
	var info SyncInfo
	result := deps.Exec.Run(GetBeadsDir(), "bd", "sync", "--status")
	if result.Err == nil {
		info.DBSynced = !strings.Contains(result.Stdout, "error") && !strings.Contains(result.Stdout, "failed")
		info.DBLastSync = "recently"
	} else {
		info.DBError = "unable to check"
	}
	return info
}

// completeSyncStatus combines the bd sync result with agent data for git push/pull counts.
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
	info := collectSyncBdStatus()
	return completeSyncStatus(info, agents)
}

func collectStatistics() MonitorStats {
	d := *defaultDeps
	d.Tracker = defaultTracker()
	return collectStatisticsDeps(&d)
}

func collectStatisticsDeps(deps *Deps) MonitorStats {
	var stats MonitorStats

	bdStats, err := deps.Tracker.Stats(context.Background())
	if err == nil && bdStats != nil {
		stats.Open = bdStats.Summary.OpenIssues
		stats.Closed = bdStats.Summary.ClosedIssues
		stats.Total = bdStats.Summary.TotalIssues
		stats.InProgress = bdStats.Summary.InProgressIssues
		stats.Blocked = bdStats.Summary.BlockedIssues
		if stats.Total > 0 {
			stats.Completion = float64(stats.Closed) / float64(stats.Total) * 100
		}

		// Remaining = total - closed
		// Note: bd stats total_issues already excludes tombstones
		stats.Remaining = stats.Total - stats.Closed
		if stats.Remaining < 0 {
			stats.Remaining = 0
		}

		// Review = total - open - inProgress - closed - blocked - deferred - pinned
		// Note: bd stats total_issues already excludes tombstones
		stats.Review = stats.Total - stats.Open - stats.InProgress - stats.Closed -
			stats.Blocked - bdStats.Summary.DeferredIssues - bdStats.Summary.PinnedIssues
		if stats.Review < 0 {
			stats.Review = 0
		}
	}

	return stats
}

// collectReadyTasksByPriority returns counts of ready tasks grouped by priority (0-4).
// It iterates ready tasks (excluding epics, in_progress, and review) and returns
// a map of priority -> count for Prometheus metrics.
func collectReadyTasksByPriority(readyLimit int) map[int]int {
	counts := make(map[int]int)
	// Initialize all priorities to 0
	for i := 0; i <= 4; i++ {
		counts[i] = 0
	}

	issues, err := defaultTracker().Ready(context.Background(), ReadyOpts{Limit: readyLimit})
	if err != nil {
		return counts
	}

	for _, issue := range issues {
		if !IsOpen(issue) {
			continue
		}
		if IsEpic(issue) {
			continue
		}
		if IsNonWorkType(issue) {
			continue
		}
		// Skip tasks with needs-revision label (these are being re-planned)
		if HasNeedsRevision(issue) {
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
