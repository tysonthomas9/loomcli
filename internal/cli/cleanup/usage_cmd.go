package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agentprofiles"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
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
	usageSource  string
	usageStatus  string
)

// Usage data sources. "sessions" is the authoritative ledger every finalized
// agent run writes to; "legacy" is the old .loom/usage.jsonl, which only
// `loom auto` ever wrote and which is kept readable for historical data.
const (
	usageSourceSessions = "sessions"
	usageSourceLegacy   = "legacy"
)

var usageCmd = &cobra.Command{
	Use:     "usage",
	Short:   "Display token usage and cost summaries",
	GroupID: "agents",
	Long: `Display token usage and cost summaries from agent sessions.

Reads the session ledger at <workspace>/sessions/index.jsonl — the file every
finalized agent run is written to — and displays aggregated token consumption
by agent and backend. The ledger path and record count are printed in the
header, so a number can always be traced back to its source.

Cost is a pass-through: it is reported only when the backend reported one.
When no record carries a cost, the summary says so rather than printing $0.00.

--source legacy reads the old .loom/usage.jsonl ledger instead, which only
` + "`loom auto`" + ` ever wrote. It is kept for historical data.

FLAGS
  --agent <name>      Filter by agent name
  --backend <name>    Filter by backend name
  --epic <id>         Filter by epic ID
  --status <status>   Filter by session status (running, completed, failed, aborted)
  --since <date>      Start date (YYYY-MM-DD)
  --until <date>      End date (YYYY-MM-DD)
  --today             Show only today's usage
  --week              Show last 7 days
  --source <src>      Ledger to read: sessions (default) or legacy
  --format <fmt>      Output format: table (default) or json
  --verbose           Show per-session detail

EXAMPLES
  loom usage                        # Show all usage
  loom usage --today                # Today's usage
  loom usage --week                 # Last 7 days
  loom usage --agent nova           # Filter by agent
  loom usage --status failed        # Only failed sessions
  loom usage --source legacy        # Read the legacy usage.jsonl ledger
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
	usageCmd.Flags().StringVar(&usageStatus, "status", "", "Filter by session status (running, completed, failed, aborted)")
	usageCmd.Flags().StringVar(&usageSource, "source", usageSourceSessions,
		"Ledger to read: sessions (sessions/index.jsonl, default) or legacy (usage.jsonl)")
	usageCmd.MarkFlagsMutuallyExclusive("today", "week", "since")
	cli.RegisterCommand(usageCmd)
}

func runUsage(cmd *cobra.Command, _ []string) {
	loomDir := cli.GetWorkspaceRuntimeDir()
	if loomDir == "" {
		loomDir = "."
	}

	f, err := buildUsageFilter()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	records, ledgerPath, err := readUsageRecords(loomDir, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading usage data: %v\n", err)
		os.Exit(1)
	}

	if len(records) == 0 {
		fmt.Println(emptyUsageMessage(ledgerPath))
		return
	}

	if usageFormat == "json" {
		renderUsageJSON(records)
		return
	}

	renderUsageTable(records, f, ledgerPath)
}

// readUsageRecords reads usage records from the selected ledger and returns
// them alongside the resolved path of the file they came from.
//
// Both ledgers are scoped to the same allowlist, so `--source legacy` cannot
// contradict the default view under the same flags.
func readUsageRecords(loomDir string, f usage.Filter) ([]usage.SessionUsage, string, error) {
	// Reporting reads are scoped to the workspace's configured agents so stray
	// ledger rows cannot move the totals; empty means unfiltered (PUPPET-340).
	// f is a value parameter, so this mutates nothing the caller sees.
	f.KnownAgents = agentprofiles.ConfiguredAgentNames(loomDir)
	switch usageSource {
	case usageSourceSessions, "":
		return usage.ReadSessionUsage(loomDir, f)
	case usageSourceLegacy:
		store, err := usage.NewStore(loomDir)
		if err != nil {
			return nil, "", err
		}
		records, err := store.Read(f)
		return records, filepath.Join(loomDir, "usage.jsonl"), err
	default:
		return nil, "", fmt.Errorf("invalid --source %q: expected %q or %q",
			usageSource, usageSourceSessions, usageSourceLegacy)
	}
}

// emptyUsageMessage names the file that turned out to be empty, so "no usage
// data" is an answer about a specific ledger rather than a shrug.
func emptyUsageMessage(ledgerPath string) string {
	msg := fmt.Sprintf("No usage data found in %s.", ledgerPath)
	if usageSource == usageSourceLegacy {
		return msg
	}
	return msg + "\nIf you are looking for historical auto-mode data, try: loom usage --source legacy"
}

// buildUsageFilter constructs a usage.Filter from command-line flags.
func buildUsageFilter() (usage.Filter, error) {
	var f usage.Filter
	f.AgentName = usageAgent
	f.Backend = usageBackend
	f.EpicID = usageEpic
	f.Status = usageStatus

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

func renderUsageTable(records []usage.SessionUsage, f usage.Filter, ledgerPath string) {
	fmt.Print(buildUsageTable(records, f, ledgerPath))
}

// buildUsageTable renders the summary box and returns it, so the rendering can
// be asserted without capturing stdout.
func buildUsageTable(records []usage.SessionUsage, f usage.Filter, ledgerPath string) string {
	agg := aggregateUsage(records)

	var sb strings.Builder

	sb.WriteString(monitor.RenderBoxTop())
	sb.WriteString(monitor.RenderBoxLine(monitor.CenterText("USAGE SUMMARY", monitor.DashboardWidth-4)))

	// Date range line
	dateRange := formatDateRange(f, records)
	sb.WriteString(monitor.RenderBoxLine(monitor.CenterText(dateRange, monitor.DashboardWidth-4)))

	// Source line: which ledger these numbers came from, and how many records
	// of it were counted.
	sb.WriteString(monitor.RenderBoxLine(monitor.CenterText(
		formatLedgerSource(ledgerPath, len(records)), monitor.DashboardWidth-4)))

	haveCost := costReported(records)
	renderUsageTotals(&sb, &agg, haveCost)
	renderUsageByAgent(&sb, agg.ByAgent, haveCost)
	renderUsageByBackend(&sb, agg.ByBackend, haveCost)

	if usageVerbose {
		renderUsageSessions(&sb, records, haveCost)
	}

	sb.WriteString(monitor.RenderBoxBottom())
	return sb.String()
}

// formatLedgerSource renders the "<ledger> — N sessions" header line. Long
// paths are trimmed from the left, because the tail is the identifying part.
func formatLedgerSource(ledgerPath string, count int) string {
	const maxPath = 44
	shown := ledgerPath
	if len([]rune(shown)) > maxPath {
		r := []rune(shown)
		shown = "\u2026" + string(r[len(r)-maxPath:])
	}
	return fmt.Sprintf("%s \u2014 %d sessions", shown, count)
}

// costReported reports whether any record carries a backend-reported cost.
// When none does, the summary says the cost was not reported instead of
// printing a $0.00 that reads like a measurement.
func costReported(records []usage.SessionUsage) bool {
	for _, rec := range records {
		if rec.EstimatedCostUSD != 0 {
			return true
		}
	}
	return false
}

// renderUsageTotals writes the TOTALS section into the string builder.
func renderUsageTotals(sb *strings.Builder, agg *usageAggregation, haveCost bool) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" TOTALS"))
	sb.WriteString(monitor.RenderBoxLine(fmt.Sprintf("   Input tokens:  %s    Output tokens:    %s",
		monitor.PadRight(formatTokenCount(agg.TotalInput), 12), formatTokenCount(agg.TotalOutput))))
	sb.WriteString(monitor.RenderBoxLine(fmt.Sprintf("   Cache reads:   %s    Cache writes:     %s",
		monitor.PadRight(formatTokenCount(agg.TotalCacheRead), 12), formatTokenCount(agg.TotalCacheWrite))))
	cost := formatCostOrUnreported(agg.TotalCost, haveCost, "not reported by backend")
	sb.WriteString(monitor.RenderBoxLine(fmt.Sprintf("   Estimated cost:  %-12s  Sessions:  %d",
		cost, agg.SessionCount)))
}

// formatCostOrUnreported renders a cost, degrading to unreported when the
// total is zero solely because no backend reported a cost. See
// usage.SessionUsage: Loom never derives cost from token counts, so a $0.00
// there would read like a measurement that was never taken. A genuine zero in
// a set that does carry costs still prints as a number.
func formatCostOrUnreported(total float64, haveCost bool, unreported string) string {
	if total == 0 && !haveCost {
		return unreported
	}
	return formatCost(total)
}

// renderUsageByAgent writes the BY AGENT section into the string builder.
func renderUsageByAgent(sb *strings.Builder, agents []agentUsageSummary, haveCost bool) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" BY AGENT"))
	for _, a := range agents {
		line := fmt.Sprintf("   %-12s %s  %2d sessions   input: %s  output: %s",
			monitor.TruncateToWidth(a.Name, 12),
			monitor.PadRight(formatCostOrUnreported(a.Cost, haveCost, "n/a"), 8),
			a.Sessions,
			formatTokenCountShort(a.InputTokens),
			formatTokenCountShort(a.OutputTokens))
		sb.WriteString(monitor.RenderBoxLine(line))
	}
}

// renderUsageByBackend writes the BY BACKEND section into the string builder.
func renderUsageByBackend(sb *strings.Builder, backends []backendUsageSummary, haveCost bool) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" BY BACKEND"))
	for _, b := range backends {
		line := fmt.Sprintf("   %-12s %s  %2d sessions",
			monitor.TruncateToWidth(b.Name, 12),
			monitor.PadRight(formatCostOrUnreported(b.Cost, haveCost, "n/a"), 8),
			b.Sessions)
		sb.WriteString(monitor.RenderBoxLine(line))
	}
}

// renderUsageSessions writes the verbose per-session detail section.
func renderUsageSessions(sb *strings.Builder, records []usage.SessionUsage, haveCost bool) {
	sb.WriteString(monitor.RenderBoxSeparator())
	sb.WriteString(monitor.RenderBoxLine(" SESSIONS"))
	// Sort by start time descending (most recent first)
	sorted := make([]usage.SessionUsage, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].StartedAt.After(sorted[j].StartedAt)
	})
	for _, rec := range sorted {
		duration := sessionDuration(rec)
		taskID := rec.TaskID
		if taskID == "" {
			taskID = "-"
		}
		line := fmt.Sprintf("   %s  %-10s %-8s %-12s %s  %s  exit:%d",
			rec.StartedAt.Format("2006-01-02 15:04"),
			monitor.TruncateToWidth(rec.AgentName, 10),
			monitor.TruncateToWidth(rec.Backend, 8),
			monitor.TruncateToWidth(taskID, 12),
			monitor.PadRight(formatCostOrUnreported(rec.EstimatedCostUSD, haveCost, "n/a"), 7),
			monitor.PadRight(formatUsageDuration(duration), 4),
			rec.ExitCode)
		sb.WriteString(monitor.RenderBoxLine(line))
	}
}

// sessionDuration returns a session's wall-clock duration. DurationS, recorded
// at finalize, is authoritative; EndedAt is only subtracted when it is actually
// set. A running session has no end time, and subtracting a zero time would
// print a nonsense multi-thousand-hour duration.
func sessionDuration(rec usage.SessionUsage) time.Duration {
	if rec.DurationS > 0 {
		return time.Duration(rec.DurationS * float64(time.Second))
	}
	if rec.EndedAt.IsZero() || rec.StartedAt.IsZero() || rec.EndedAt.Before(rec.StartedAt) {
		return 0
	}
	return rec.EndedAt.Sub(rec.StartedAt)
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
