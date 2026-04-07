package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
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

Shows daemon health, backend config, worktree/agent state, task counts,
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
	Daemon       DaemonInfo       `json:"daemon"`
	Backend      BackendInfo      `json:"backend"`
	IssueBackend string           `json:"issue_backend"`
	Worktrees    WorktreesSummary `json:"worktrees"`
	Beads        BeadsSummary     `json:"beads"`
	Git          GitSummary       `json:"git"`
	Redis        RedisInfo        `json:"redis"`
	Issues       []StatusIssue    `json:"issues,omitempty"`
}

// DaemonInfo holds daemon health information.
type DaemonInfo struct {
	Running  bool   `json:"running"`
	PID      int    `json:"pid,omitempty"`
	Uptime   string `json:"uptime,omitempty"`
	StalePID bool   `json:"stale_pid,omitempty"`
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

// BeadsSummary holds task count summaries.
type BeadsSummary struct {
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
		daemonInfo DaemonInfo
		monData    *monitor.MonitorData
		wg         sync.WaitGroup
	)

	// Collect daemon status and monitor data concurrently.
	wg.Add(2)

	go func() {
		defer wg.Done()
		daemonInfo = collectDaemonStatus()
	}()

	go func() {
		defer wg.Done()
		monData = monitor.CollectMonitorData(100, statusBranch)
	}()

	wg.Wait()

	// Build status data from collected information.
	data := buildStatusData(daemonInfo, monData)

	if statusJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}

	renderStatusHuman(data)
	return nil
}

func collectDaemonStatus() DaemonInfo {
	projectDir, err := os.Getwd()
	if err != nil {
		return DaemonInfo{}
	}
	return collectDaemonStatusForDir(projectDir)
}

func buildStatusData(daemon DaemonInfo, mon *monitor.MonitorData) StatusData {
	data := StatusData{
		Daemon:       daemon,
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

		// Beads
		data.Beads = BeadsSummary{
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
	if pf, err := config.LoadProjectFile(cli.GetBeadsDir()); err == nil && pf != nil && pf.Backend != "" {
		return "project"
	}
	cfg, err := config.LoadConfig()
	if err == nil && cfg != nil && cfg.Backend != "" {
		return "config"
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
	renderStatusDaemon(data.Daemon)
	fmt.Printf("Backend:    %s (via %s)\n", data.Backend.Name, data.Backend.Source)
	fmt.Printf("Issues:     %s\n", data.IssueBackend)
	renderStatusWorktrees(data.Worktrees)
	fmt.Printf("Beads:      %d open, %d in-progress, %d review, %d closed\n",
		data.Beads.Open, data.Beads.InProgress, data.Beads.Review, data.Beads.Closed)
	renderStatusGit(data.Git)
	renderStatusRedis(data.Redis)
	renderStatusIssues(data.Issues)
}

func renderStatusDaemon(d DaemonInfo) {
	switch {
	case d.Running:
		uptime := d.Uptime
		if uptime == "" {
			uptime = "unknown"
		}
		fmt.Printf("Daemon:     running (pid %d, uptime %s)\n", d.PID, uptime)
	case d.StalePID:
		fmt.Println("Daemon:     not running (stale pid file)")
	default:
		fmt.Println("Daemon:     not running")
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

// collectDaemonStatusForDir is an internal helper for testing with a custom project dir.
func collectDaemonStatusForDir(projectDir string) DaemonInfo {
	rt := cli.DetectDaemonRuntime(projectDir)

	if rt.Running {
		info := DaemonInfo{Running: true, PID: rt.PID}
		stateFilePath := config.ResolveDaemonStatePath(projectDir)
		// Read state file for uptime info (inline to avoid daemon/ import cycle)
		if data, err := os.ReadFile(stateFilePath); err == nil { //nolint:gosec // controlled path
			var state struct {
				StartedAt time.Time `json:"started_at"`
			}
			if json.Unmarshal(data, &state) == nil {
				info.Uptime = formatDuration(time.Since(state.StartedAt))
			}
		}
		return info
	}

	// Check for stale PID file (file exists but process not running)
	dcfg, err := config.LoadDaemonConfig(projectDir)
	if err != nil {
		dcfg = &config.DaemonConfig{
			Daemon: config.DaemonSettings{
				PIDFile: ".loom/daemon.pid",
			},
		}
	}
	pidFile := dcfg.Daemon.PIDFile
	if !filepath.IsAbs(pidFile) {
		pidFile = filepath.Join(projectDir, pidFile)
	}
	if _, err := os.Stat(pidFile); err == nil {
		return DaemonInfo{StalePID: true}
	}

	return DaemonInfo{}
}
