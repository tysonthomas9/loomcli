package monitor

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

const (
	DashboardWidth = 70 // Width of the monitor dashboard box
)

var (
	monitorNoWatch  bool
	monitorInterval int
	monitorBranch   string
)

var monitorCmd = &cobra.Command{
	Use:     "monitor",
	Aliases: []string{"mon", "status"},
	Short:   "Display comprehensive agent and task dashboard",
	Long: `Display a dashboard showing agents, tasks, sync status, and statistics.

Sections:
  AGENTS     - Worktree status (running/idle, branch, dirty/clean)
  TASKS      - Ready, in_progress, need review, backlog counts
  SYNC       - Database and git sync status
  STATS      - Overall issue counts and completion rate

Flags:
  -b, --branch      Integration branch to compare against
  -n, --no-watch    One-shot mode (disable auto-refresh)
  -i, --interval    Refresh interval in seconds (default: 5)

Examples:
  loom monitor              # Auto-refresh (default)
  loom monitor -n           # One-shot display
  loom monitor -i 10        # Refresh every 10 seconds`,
	Args: cobra.NoArgs,
	Run:  runMonitor,
}

func init() {
	monitorCmd.Flags().StringVarP(&monitorBranch, "branch", "b", "", "Integration branch to compare against (default: LOOM_DEFAULT_BRANCH or main)")
	monitorCmd.Flags().BoolVarP(&monitorNoWatch, "no-watch", "n", false, "Disable auto-refresh (one-shot mode)")
	monitorCmd.Flags().IntVarP(&monitorInterval, "interval", "i", 5, "Refresh interval in seconds")
	cli.RegisterCommand(monitorCmd)
}

func runMonitor(cmd *cobra.Command, args []string) {
	if !monitorNoWatch {
		// Watch mode - show loading message while first data collection runs
		fmt.Print("\033[?25l") // Hide cursor
		fmt.Print("\033[H")    // Move to home position
		fmt.Print("\033[J")    // Clear screen
		fmt.Print("Loading...")
		fmt.Print("\033[?25h") // Show cursor

		// Collect first batch before entering loop (loading message visible during this).
		// Limit 10000: ready queues include open + review + in_progress; a small limit can
		// push the few truly-open tasks past the cutoff when review items are dense.
		data := CollectMonitorData(10000, monitorBranch)
		output := renderDashboard(data)
		fullOutput := output + fmt.Sprintf("\nPress Ctrl+C to exit (refreshing every %ds)", monitorInterval)
		fmt.Print("\033[?25l")
		fmt.Print("\033[H")
		fmt.Print(fullOutput)
		fmt.Print("\033[J")
		fmt.Print("\033[?25h")

		// Watch mode - refresh in place without flickering
		for {
			time.Sleep(time.Duration(monitorInterval) * time.Second)
			data = CollectMonitorData(10000, monitorBranch)
			output = renderDashboard(data)

			// Build complete output including status line (no trailing newline)
			fullOutput := output + fmt.Sprintf("\nPress Ctrl+C to exit (refreshing every %ds)", monitorInterval)

			fmt.Print("\033[?25l") // Hide cursor
			fmt.Print("\033[H")    // Move to home position
			fmt.Print(fullOutput)
			fmt.Print("\033[J")    // Clear from cursor to end of screen
			fmt.Print("\033[?25h") // Show cursor
		}
	} else {
		// One-shot mode - show loading message on stderr
		fmt.Fprint(os.Stderr, "Loading...")
		data := CollectMonitorData(10000, monitorBranch)
		fmt.Fprint(os.Stderr, "\r          \r") // Clear loading message
		fmt.Print(renderDashboard(data))
	}
}

// CollectMonitorDataForServer gathers all dashboard data with default limit.
// Exported for use by the HTTP server.
func CollectMonitorDataForServer(branch string) *MonitorData {
	return CollectMonitorData(10000, branch)
}

// CollectAgentStatusOnly returns just agent status without task context.
// Exported for use by the HTTP server.
func CollectAgentStatusOnly(branch string) []AgentStatus {
	agents, _ := collectAgentStatus(nil, branch)
	return agents
}
