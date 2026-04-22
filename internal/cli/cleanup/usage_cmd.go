package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

var (
	usageAgent   string
	usageBackend string
	usageEpic    string
	usageSince   string
	usageUntil   string
	usageToday   bool
	usageWeek    bool
	usageFormat  string
	usageVerbose bool
)

var usageCmd = &cobra.Command{
	Use:     "usage",
	Short:   "Display token usage and cost summaries",
	GroupID: "agents",
	Long: `Display token usage and cost summaries from agent sessions.

Reads usage data from the local .loom/usage.jsonl file and displays
aggregated token consumption and cost breakdowns by agent and backend.

FLAGS
  --agent <name>      Filter by agent name
  --backend <name>    Filter by backend name
  --epic <id>         Filter by epic ID
  --since <date>      Start date (YYYY-MM-DD)
  --until <date>      End date (YYYY-MM-DD)
  --today             Show only today's usage
  --week              Show last 7 days
  --format <fmt>      Output format: table (default) or json
  --verbose           Show per-session detail

EXAMPLES
  loom usage                        # Show all usage
  loom usage --today                # Today's usage
  loom usage --week                 # Last 7 days
  loom usage --agent nova           # Filter by agent
  loom usage --format json          # JSON output
  loom usage --verbose              # Per-session detail`,
	Args: cobra.NoArgs,
	Run:  runUsage,
}

func init() {
	usageCmd.Flags().StringVar(&usageAgent, "agent", "", "Filter by agent name")
	usageCmd.Flags().StringVar(&usageBackend, "backend", "", "Filter by backend name")
	usageCmd.Flags().StringVar(&usageEpic, "epic", "", "Filter by epic ID")
	usageCmd.Flags().StringVar(&usageSince, "since", "", "Start date (YYYY-MM-DD)")
	usageCmd.Flags().StringVar(&usageUntil, "until", "", "End date (YYYY-MM-DD)")
	usageCmd.Flags().BoolVar(&usageToday, "today", false, "Show only today's usage")
	usageCmd.Flags().BoolVar(&usageWeek, "week", false, "Show last 7 days")
	usageCmd.Flags().StringVar(&usageFormat, "format", "table", "Output format: table or json")
	usageCmd.Flags().BoolVar(&usageVerbose, "verbose", false, "Show per-session detail")
	usageCmd.MarkFlagsMutuallyExclusive("today", "week", "since")
	cli.RegisterCommand(usageCmd)
}

func runUsage(cmd *cobra.Command, _ []string) {
	store, err := usage.NewStoreForWorkspace(workspacemgr.ResolveInitialWorkspaceID(), cli.GetBeadsDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	f, err := buildUsageFilter()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	records, err := store.Read(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading usage data: %v\n", err)
		os.Exit(1)
	}

	if len(records) == 0 {
		fmt.Println("No usage data found. Run agents in auto-mode to generate usage data.")
		return
	}

	if usageFormat == "json" {
		renderUsageJSON(records)
		return
	}

	renderUsageTable(records, f)
}

// buildUsageFilter constructs a usage.Filter from command-line flags.
func buildUsageFilter() (usage.Filter, error) {
	var f usage.Filter
	f.AgentName = usageAgent
	f.Backend = usageBackend
	f.EpicID = usageEpic

	now := time.Now()

	// --today and --week take precedence over --since
	if usageToday {
		f.Since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	} else if usageWeek {
		f.Since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -7)
	} else if usageSince != "" {
		t, err := time.Parse("2006-01-02", usageSince)
		if err != nil {
			return f, fmt.Errorf("invalid --since date format, expected YYYY-MM-DD: %v", err)
		}
		f.Since = t
	}

	if usageUntil != "" {
		t, err := time.Parse("2006-01-02", usageUntil)
		if err != nil {
			return f, fmt.Errorf("invalid --until date format, expected YYYY-MM-DD: %v", err)
		}
		f.Until = t.Add(24*time.Hour - time.Nanosecond)
	}

	if !f.Since.IsZero() && !f.Until.IsZero() && f.Until.Before(f.Since) {
		return f, fmt.Errorf("--until must be after --since")
	}

	return f, nil
}

// usageAggregation holds aggregated usage data.
type usageAggregation struct {
	TotalInput      int64
	TotalOutput     int64
	TotalCacheRead  int64
	TotalCacheWrite int64
	TotalCost       float64
	SessionCount    int
	ByAgent         []agentUsageSummary
	ByBackend       []backendUsageSummary
}

type agentUsageSummary struct {
	Name         string
	Cost         float64
	Sessions     int
	InputTokens  int64
	OutputTokens int64
}

type backendUsageSummary struct {
	Name     string
	Cost     float64
	Sessions int
}

func aggregateUsage(records []usage.SessionUsage) usageAggregation {
	var agg usageAggregation
	agg.SessionCount = len(records)

	agentMap := make(map[string]*agentUsageSummary)
	backendMap := make(map[string]*backendUsageSummary)

	for _, rec := range records {
		agg.TotalInput += rec.InputTokens
		agg.TotalOutput += rec.OutputTokens
		agg.TotalCacheRead += rec.CacheReadTokens
		agg.TotalCacheWrite += rec.CacheWriteTokens
		agg.TotalCost += rec.EstimatedCostUSD

		a, ok := agentMap[rec.AgentName]
		if !ok {
			a = &agentUsageSummary{Name: rec.AgentName}
			agentMap[rec.AgentName] = a
		}
		a.Sessions++
		a.InputTokens += rec.InputTokens
		a.OutputTokens += rec.OutputTokens
		a.Cost += rec.EstimatedCostUSD

		b, ok := backendMap[rec.Backend]
		if !ok {
			b = &backendUsageSummary{Name: rec.Backend}
			backendMap[rec.Backend] = b
		}
		b.Sessions++
		b.Cost += rec.EstimatedCostUSD
	}

	agg.ByAgent = make([]agentUsageSummary, 0, len(agentMap))
	for _, a := range agentMap {
		agg.ByAgent = append(agg.ByAgent, *a)
	}
	sort.Slice(agg.ByAgent, func(i, j int) bool {
		return agg.ByAgent[i].Cost > agg.ByAgent[j].Cost
	})

	agg.ByBackend = make([]backendUsageSummary, 0, len(backendMap))
	for _, b := range backendMap {
		agg.ByBackend = append(agg.ByBackend, *b)
	}
	sort.Slice(agg.ByBackend, func(i, j int) bool {
		return agg.ByBackend[i].Cost > agg.ByBackend[j].Cost
	})

	return agg
}

func renderUsageTable(records []usage.SessionUsage, f usage.Filter) {
	agg := aggregateUsage(records)

	var sb strings.Builder

	sb.WriteString(monitor.RenderBoxTop())
	sb.WriteString(monitor.RenderBoxLine(monitor.CenterText("USAGE SUMMARY", monitor.DashboardWidth-4)))

	// Date range line
	dateRange := formatDateRange(f, records)
	sb.WriteString(monitor.RenderBoxLine(monitor.CenterText(dateRange, monitor.DashboardWidth-4)))

	renderUsageTotals(&sb, &agg)
	renderUsageByAgent(&sb, agg.ByAgent)
	renderUsageByBackend(&sb, agg.ByBackend)

	if usageVerbose {
		renderUsageSessions(&sb, records)
	}

	sb.WriteString(monitor.RenderBoxBottom())
	fmt.Print(sb.String())
}

// renderUsageTotals writes the TOTALS section into the string builder.
func renderUsageTotals(sb *strings.Builder, agg *usageAggregation) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" TOTALS"))
	sb.WriteString(monitor.RenderBoxLine(fmt.Sprintf("   Input tokens:  %s    Output tokens:    %s",
		monitor.PadRight(formatTokenCount(agg.TotalInput), 12), formatTokenCount(agg.TotalOutput))))
	sb.WriteString(monitor.RenderBoxLine(fmt.Sprintf("   Cache reads:   %s    Cache writes:     %s",
		monitor.PadRight(formatTokenCount(agg.TotalCacheRead), 12), formatTokenCount(agg.TotalCacheWrite))))
	sb.WriteString(monitor.RenderBoxLine(fmt.Sprintf("   Estimated cost:  %-12s  Sessions:  %d",
		formatCost(agg.TotalCost), agg.SessionCount)))
}

// renderUsageByAgent writes the BY AGENT section into the string builder.
func renderUsageByAgent(sb *strings.Builder, agents []agentUsageSummary) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" BY AGENT"))
	for _, a := range agents {
		line := fmt.Sprintf("   %-12s %s  %2d sessions   input: %s  output: %s",
			monitor.TruncateToWidth(a.Name, 12),
			monitor.PadRight(formatCost(a.Cost), 8),
			a.Sessions,
			formatTokenCountShort(a.InputTokens),
			formatTokenCountShort(a.OutputTokens))
		sb.WriteString(monitor.RenderBoxLine(line))
	}
}

// renderUsageByBackend writes the BY BACKEND section into the string builder.
func renderUsageByBackend(sb *strings.Builder, backends []backendUsageSummary) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" BY BACKEND"))
	for _, b := range backends {
		line := fmt.Sprintf("   %-12s %s  %2d sessions",
			monitor.TruncateToWidth(b.Name, 12),
			monitor.PadRight(formatCost(b.Cost), 8),
			b.Sessions)
		sb.WriteString(monitor.RenderBoxLine(line))
	}
}

// renderUsageSessions writes the verbose per-session detail section.
func renderUsageSessions(sb *strings.Builder, records []usage.SessionUsage) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" SESSIONS"))
	// Sort by start time descending (most recent first)
	sorted := make([]usage.SessionUsage, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].StartedAt.After(sorted[j].StartedAt)
	})
	for _, rec := range sorted {
		duration := rec.EndedAt.Sub(rec.StartedAt)
		taskID := rec.TaskID
		if taskID == "" {
			taskID = "-"
		}
		line := fmt.Sprintf("   %s  %-10s %-8s %-12s %s  %s  exit:%d",
			rec.StartedAt.Format("2006-01-02 15:04"),
			monitor.TruncateToWidth(rec.AgentName, 10),
			monitor.TruncateToWidth(rec.Backend, 8),
			monitor.TruncateToWidth(taskID, 12),
			monitor.PadRight(formatCost(rec.EstimatedCostUSD), 7),
			monitor.PadRight(formatUsageDuration(duration), 4),
			rec.ExitCode)
		sb.WriteString(monitor.RenderBoxLine(line))
	}
}

func renderUsageJSON(records []usage.SessionUsage) {
	agg := aggregateUsage(records)

	type jsonAgent struct {
		Name         string  `json:"name"`
		Cost         float64 `json:"cost"`
		Sessions     int     `json:"sessions"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
	}
	type jsonBackend struct {
		Name     string  `json:"name"`
		Cost     float64 `json:"cost"`
		Sessions int     `json:"sessions"`
	}
	type jsonOutput struct {
		TotalInputTokens      int64                `json:"total_input_tokens"`
		TotalOutputTokens     int64                `json:"total_output_tokens"`
		TotalCacheReadTokens  int64                `json:"total_cache_read_tokens"`
		TotalCacheWriteTokens int64                `json:"total_cache_write_tokens"`
		TotalCost             float64              `json:"total_cost"`
		SessionCount          int                  `json:"session_count"`
		ByAgent               []jsonAgent          `json:"by_agent"`
		ByBackend             []jsonBackend        `json:"by_backend"`
		Sessions              []usage.SessionUsage `json:"sessions"`
	}

	out := jsonOutput{
		TotalInputTokens:      agg.TotalInput,
		TotalOutputTokens:     agg.TotalOutput,
		TotalCacheReadTokens:  agg.TotalCacheRead,
		TotalCacheWriteTokens: agg.TotalCacheWrite,
		TotalCost:             agg.TotalCost,
		SessionCount:          agg.SessionCount,
		Sessions:              records,
	}
	for _, a := range agg.ByAgent {
		out.ByAgent = append(out.ByAgent, jsonAgent(a))
	}
	for _, b := range agg.ByBackend {
		out.ByBackend = append(out.ByBackend, jsonBackend(b))
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}
}

// formatTokenCount formats an int64 token count with commas (e.g., 1,234,567).
// formatTokenCount formats a non-negative int64 token count with commas (e.g., 1,234,567).
func formatTokenCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result strings.Builder
	remainder := len(s) % 3
	if remainder > 0 {
		result.WriteString(s[:remainder])
	}
	for i := remainder; i < len(s); i += 3 {
		if result.Len() > 0 {
			result.WriteByte(',')
		}
		result.WriteString(s[i : i+3])
	}
	return result.String()
}

// formatTokenCountShort formats tokens in short form (e.g., 456K, 1.2M).
func formatTokenCountShort(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// formatCost formats a USD cost (e.g., $12.35).
func formatCost(cost float64) string {
	return fmt.Sprintf("$%.2f", cost)
}

// formatDuration formats a duration as a short string (e.g., "12m", "1h5m").
func formatUsageDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// formatDateRange returns a display string for the effective date range.
func formatDateRange(f usage.Filter, records []usage.SessionUsage) string {
	if f.Since.IsZero() && f.Until.IsZero() {
		// Derive from records
		if len(records) == 0 {
			return "No data"
		}
		earliest := records[0].StartedAt
		latest := records[0].StartedAt
		for _, r := range records[1:] {
			if r.StartedAt.Before(earliest) {
				earliest = r.StartedAt
			}
			if r.StartedAt.After(latest) {
				latest = r.StartedAt
			}
		}
		return fmt.Sprintf("%s \u2192 %s", earliest.Format("2006-01-02"), latest.Format("2006-01-02"))
	}
	since := "..."
	if !f.Since.IsZero() {
		since = f.Since.Format("2006-01-02")
	}
	until := "..."
	if !f.Until.IsZero() {
		until = f.Until.Format("2006-01-02")
	}
	return fmt.Sprintf("%s \u2192 %s", since, until)
}
