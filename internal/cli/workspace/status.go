package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	localruntime "github.com/tysonthomas9/loomcli/internal/cli/local"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/kv"
)

var (
	statusJSON   bool
	statusBranch string
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show system overview",
	GroupID: "workspace",
	Long: `Display a one-shot snapshot of the entire loom system.

Shows local runtime health, backend config, worktree/agent state, task counts,
git sync status, Redis connectivity, and any detected issues.

Unlike 'loom monitor' (auto-refreshing dashboard), 'loom status' is a
static snapshot optimized for scripting and quick glances.

Examples:
  loom status              # Human-readable overview
  loom status --json       # Machine-readable JSON output`,
	Args: cobra.NoArgs,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output in JSON format")
	statusCmd.Flags().StringVarP(&statusBranch, "branch", "b", "", "Integration branch to compare against (default: LOOM_DEFAULT_BRANCH or main)")
	cli.RegisterCommand(statusCmd)
}

// StatusData is the top-level JSON output for loom status.
type StatusData struct {
	Runtime      RuntimeInfo      `json:"runtime"`
	Backend      BackendInfo      `json:"backend"`
	IssueBackend string           `json:"issue_backend"`
	Worktrees    WorktreesSummary `json:"worktrees"`
	Tasks        TaskSummary      `json:"tasks"`
	Git          GitSummary       `json:"git"`
	Redis        RedisInfo        `json:"redis"`
	Issues       []StatusIssue    `json:"issues,omitempty"`
}

// RuntimeInfo reports the local HTTP runtime used by Desktop deployments.
type RuntimeInfo struct {
	Applicable bool   `json:"applicable"`
	Healthy    bool   `json:"healthy"`
	PID        int    `json:"pid,omitempty"`
	URL        string `json:"url,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Error      string `json:"error,omitempty"`
}

// BackendInfo holds the resolved backend information.
type BackendInfo struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// WorktreesSummary holds the worktree overview.
type WorktreesSummary struct {
	Active int                  `json:"active"`
	Idle   int                  `json:"idle"`
	List   []WorktreeStatusItem `json:"list,omitempty"`
}

// WorktreeStatusItem represents a single worktree in the status output.
type WorktreeStatusItem struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	TaskID string `json:"task_id,omitempty"`
	Title  string `json:"title,omitempty"`
}

// TaskSummary holds task count summaries.
type TaskSummary struct {
	Open       int `json:"open"`
	InProgress int `json:"in_progress"`
	Review     int `json:"review"`
	Closed     int `json:"closed"`
}

// GitSummary holds git sync information.
type GitSummary struct {
	NeedsPush int `json:"needs_push"`
	NeedsPull int `json:"needs_pull"`
}

// RedisInfo holds Redis connectivity status.
type RedisInfo struct {
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected,omitempty"`
	Error      string `json:"error,omitempty"`
}

// StatusIssue represents a detected issue or warning.
type StatusIssue struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	var (
		runtimeInfo RuntimeInfo
		monData     *monitor.MonitorData
		wg          sync.WaitGroup
	)

	// Collect runtime status and monitor data concurrently.
	wg.Add(2)

	go func() {
		defer wg.Done()
		runtimeInfo = collectRuntimeStatus()
	}()

	go func() {
		defer wg.Done()
		monData = monitor.CollectMonitorData(10000, statusBranch)
	}()

	wg.Wait()

	// Build status data from collected information.
	data := buildStatusData(runtimeInfo, monData)

	if statusJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}

	renderStatusHuman(data)
	return nil
}

func collectRuntimeStatus() RuntimeInfo {
	dataDir, err := localruntime.DefaultDataDir()
	if err != nil {
		return RuntimeInfo{Applicable: true, Error: err.Error()}
	}
	snapshot, readErr := localruntime.ReadRuntimeStatus(context.Background(), dataDir)
	local := buildLocalRuntime(snapshot, readErr)
	info := RuntimeInfo{
		Applicable: local.Applicable,
		Healthy:    local.Healthy,
		Reason:     local.Reason,
		Error:      local.Error,
	}
	if local.Runtime != nil {
		info.PID = local.Runtime.PID
		info.URL = local.Runtime.URL
	}
	return info
}

func buildStatusData(runtime RuntimeInfo, mon *monitor.MonitorData) StatusData {
	data := StatusData{
		Runtime:      runtime,
		Backend:      collectBackendInfo(),
		IssueBackend: cli.ResolveIssueBackendType(),
		Redis:        collectRedisStatus(),
	}

	if mon != nil {
		// Worktrees
		for _, agent := range mon.Agents {
			item := WorktreeStatusItem{
				Name:   agent.Name,
				Status: agent.Status,
			}
			// Extract task ID from status string (e.g. "working: loomcli-abc.1 (2m)")
			if parts := strings.SplitN(agent.Status, ": ", 2); len(parts) == 2 {
				taskPart := parts[1]
				if idx := strings.Index(taskPart, " ("); idx != -1 {
					item.TaskID = taskPart[:idx]
				} else {
					item.TaskID = taskPart
				}
			}
			data.Worktrees.List = append(data.Worktrees.List, item)

			if isActiveStatus(agent.Status) {
				data.Worktrees.Active++
			} else {
				data.Worktrees.Idle++
			}
		}

		// Tasks
		data.Tasks = TaskSummary{
			Open:       mon.Stats.Open,
			InProgress: mon.Stats.InProgress,
			Review:     mon.Stats.Review,
			Closed:     mon.Stats.Closed,
		}

		// Git
		data.Git = GitSummary{
			NeedsPush: mon.SyncStatus.GitNeedsPush,
			NeedsPull: mon.SyncStatus.GitNeedsPull,
		}

		// Issues
		data.Issues = detectIssues(mon)
	}

	return data
}

func collectBackendInfo() BackendInfo {
	name := cli.ResolveBackendName()
	source := resolveBackendSource()
	return BackendInfo{Name: name, Source: source}
}

func resolveBackendSource() string {
	if cli.GetBackendFlag() != "" {
		return "flag"
	}
	if os.Getenv("LOOM_BACKEND") != "" {
		return "env"
	}
	return "default"
}

func collectRedisStatus() RedisInfo {
	addr := os.Getenv("LOOM_REDIS_ADDR")
	if addr == "" {
		return RedisInfo{Configured: false}
	}

	info := RedisInfo{Configured: true}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	password := os.Getenv("LOOM_REDIS_PASSWORD")
	client := kv.NewClient(addr, password, 0)
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx); err != nil {
		info.Error = err.Error()
		return info
	}

	info.Connected = true
	return info
}

func detectIssues(mon *monitor.MonitorData) []StatusIssue {
	var issues []StatusIssue

	// Detect stale locks (lock file exists but process not running)
	worktrees, err := cli.DiscoverWorktrees()
	if err == nil {
		for _, wt := range worktrees {
			info, running, checkErr := cli.CheckLock(wt.Path)
			if checkErr != nil || info == nil {
				continue
			}
			if !running {
				age := formatDuration(time.Since(info.StartedAt))
				issues = append(issues, StatusIssue{
					Level:   "warning",
					Message: fmt.Sprintf("worktree %s has stale lock (%s)", wt.Name, age),
				})
			}
		}
	}

	// Detect orphaned in_progress tasks (no running agent)
	for _, task := range mon.InProgressTasks {
		hasAgent := false
		for _, agent := range mon.Agents {
			if strings.Contains(agent.Status, task.ID) {
				hasAgent = true
				break
			}
		}
		if !hasAgent {
			issues = append(issues, StatusIssue{
				Level:   "warning",
				Message: fmt.Sprintf("task %s is in_progress with no running agent", task.ID),
			})
		}
	}

	return issues
}

func isActiveStatus(status string) bool {
	return strings.HasPrefix(status, "working:") ||
		strings.HasPrefix(status, "planning:") ||
		strings.HasPrefix(status, "review:") ||
		strings.HasPrefix(status, "done:")
}

func renderStatusHuman(data StatusData) {
	renderStatusRuntime(data.Runtime)
	fmt.Printf("Backend:    %s (via %s)\n", data.Backend.Name, data.Backend.Source)
	fmt.Printf("Issues:     %s\n", data.IssueBackend)
	renderStatusWorktrees(data.Worktrees)
	fmt.Printf("Tasks:      %d open, %d in-progress, %d review, %d closed\n",
		data.Tasks.Open, data.Tasks.InProgress, data.Tasks.Review, data.Tasks.Closed)
	renderStatusGit(data.Git)
	renderStatusRedis(data.Redis)
	renderStatusIssues(data.Issues)
}

func renderStatusRuntime(runtime RuntimeInfo) {
	switch {
	case !runtime.Applicable:
		fmt.Printf("Runtime:    not applicable (%s)\n", runtime.Reason)
	case runtime.Healthy:
		fmt.Printf("Runtime:    healthy (pid %d, url %s)\n", runtime.PID, runtime.URL)
	case runtime.Error != "":
		fmt.Printf("Runtime:    unavailable (%s)\n", runtime.Error)
	default:
		fmt.Println("Runtime:    unavailable")
	}
}

func renderStatusWorktrees(wt WorktreesSummary) {
	if len(wt.List) == 0 {
		fmt.Println("Worktrees:  none")
		return
	}
	fmt.Printf("Worktrees:  %d active, %d idle\n", wt.Active, wt.Idle)
	for i, item := range wt.List {
		prefix := "  \u251c\u2500\u2500 "
		if i == len(wt.List)-1 {
			prefix = "  \u2514\u2500\u2500 "
		}
		fmt.Printf("%s%-14s %s\n", prefix, item.Name, item.Status)
	}
}

func renderStatusGit(g GitSummary) {
	if g.NeedsPush == 0 && g.NeedsPull == 0 {
		fmt.Println("Git:        all synced")
		return
	}
	var parts []string
	if g.NeedsPush > 0 {
		parts = append(parts, fmt.Sprintf("%d need push", g.NeedsPush))
	}
	if g.NeedsPull > 0 {
		parts = append(parts, fmt.Sprintf("%d need pull", g.NeedsPull))
	}
	fmt.Printf("Git:        %s\n", strings.Join(parts, ", "))
}

func renderStatusRedis(r RedisInfo) {
	switch {
	case !r.Configured:
		fmt.Println("Redis:      not configured")
	case r.Connected:
		fmt.Println("Redis:      connected (fleet mode)")
	default:
		fmt.Printf("Redis:      error (%s)\n", r.Error)
	}
}

func renderStatusIssues(issues []StatusIssue) {
	if len(issues) == 0 {
		return
	}
	fmt.Printf("Issues:     %d detected\n", len(issues))
	for _, issue := range issues {
		icon := "\u26a0"
		if issue.Level == "error" {
			icon = "\u2717"
		}
		fmt.Printf("  %s %s\n", icon, issue.Message)
	}
}

// formatDuration returns a human-friendly duration string (e.g., "2h3m", "45m", "3s").
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}
