package cleanup

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func captureCleanupStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = r.Close()
	}()

	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stdout: %v", err)
	}
	return buf.String()
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{123456, "123,456"},
		{1234567, "1,234,567"},
		{1000000000, "1,000,000,000"},
	}
	for _, tt := range tests {
		got := formatTokenCount(tt.input)
		if got != tt.expected {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatTokenCountShort(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1K"},
		{1500, "1K"},
		{456000, "456K"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{12345678, "12.3M"},
	}
	for _, tt := range tests {
		got := formatTokenCountShort(tt.input)
		if got != tt.expected {
			t.Errorf("formatTokenCountShort(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "$0.00"},
		{0.1, "$0.10"},
		{12.345, "$12.35"},
		{100.999, "$101.00"},
	}
	for _, tt := range tests {
		got := formatCost(tt.input)
		if got != tt.expected {
			t.Errorf("formatCost(%f) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatUsageDuration(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{30 * time.Second, "30s"},
		{1 * time.Minute, "1m"},
		{5*time.Minute + 30*time.Second, "5m"},
		{1 * time.Hour, "1h"},
		{1*time.Hour + 30*time.Minute, "1h30m"},
		{2*time.Hour + 5*time.Minute, "2h5m"},
	}
	for _, tt := range tests {
		got := formatUsageDuration(tt.input)
		if got != tt.expected {
			t.Errorf("formatUsageDuration(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestAggregateUsage(t *testing.T) {
	records := []usage.SessionUsage{
		{
			AgentName:        "nova",
			Backend:          "claude",
			InputTokens:      100000,
			OutputTokens:     50000,
			CacheReadTokens:  10000,
			CacheWriteTokens: 5000,
			EstimatedCostUSD: 1.50,
		},
		{
			AgentName:        "falcon",
			Backend:          "claude",
			InputTokens:      200000,
			OutputTokens:     100000,
			CacheReadTokens:  20000,
			CacheWriteTokens: 10000,
			EstimatedCostUSD: 3.00,
		},
		{
			AgentName:        "nova",
			Backend:          "codex",
			InputTokens:      50000,
			OutputTokens:     25000,
			CacheReadTokens:  0,
			CacheWriteTokens: 0,
			EstimatedCostUSD: 0.75,
		},
	}

	agg := aggregateUsage(records)

	if agg.SessionCount != 3 {
		t.Errorf("SessionCount = %d, want 3", agg.SessionCount)
	}
	if agg.TotalInput != 350000 {
		t.Errorf("TotalInput = %d, want 350000", agg.TotalInput)
	}
	if agg.TotalOutput != 175000 {
		t.Errorf("TotalOutput = %d, want 175000", agg.TotalOutput)
	}
	if agg.TotalCacheRead != 30000 {
		t.Errorf("TotalCacheRead = %d, want 30000", agg.TotalCacheRead)
	}
	if agg.TotalCacheWrite != 15000 {
		t.Errorf("TotalCacheWrite = %d, want 15000", agg.TotalCacheWrite)
	}
	if agg.TotalCost != 5.25 {
		t.Errorf("TotalCost = %f, want 5.25", agg.TotalCost)
	}

	// ByAgent should be sorted by cost descending
	if len(agg.ByAgent) != 2 {
		t.Fatalf("ByAgent length = %d, want 2", len(agg.ByAgent))
	}
	if agg.ByAgent[0].Name != "falcon" {
		t.Errorf("ByAgent[0].Name = %q, want %q (highest cost)", agg.ByAgent[0].Name, "falcon")
	}
	if agg.ByAgent[0].Cost != 3.00 {
		t.Errorf("ByAgent[0].Cost = %f, want 3.00", agg.ByAgent[0].Cost)
	}
	if agg.ByAgent[1].Name != "nova" {
		t.Errorf("ByAgent[1].Name = %q, want %q", agg.ByAgent[1].Name, "nova")
	}
	if agg.ByAgent[1].Sessions != 2 {
		t.Errorf("ByAgent[1].Sessions = %d, want 2", agg.ByAgent[1].Sessions)
	}

	// ByBackend should be sorted by cost descending
	if len(agg.ByBackend) != 2 {
		t.Fatalf("ByBackend length = %d, want 2", len(agg.ByBackend))
	}
	if agg.ByBackend[0].Name != "claude" {
		t.Errorf("ByBackend[0].Name = %q, want %q", agg.ByBackend[0].Name, "claude")
	}
	if agg.ByBackend[0].Cost != 4.50 {
		t.Errorf("ByBackend[0].Cost = %f, want 4.50", agg.ByBackend[0].Cost)
	}
}

func TestAggregateUsageEmpty(t *testing.T) {
	agg := aggregateUsage(nil)
	if agg.SessionCount != 0 {
		t.Errorf("SessionCount = %d, want 0", agg.SessionCount)
	}
	if len(agg.ByAgent) != 0 {
		t.Errorf("ByAgent length = %d, want 0", len(agg.ByAgent))
	}
	if len(agg.ByBackend) != 0 {
		t.Errorf("ByBackend length = %d, want 0", len(agg.ByBackend))
	}
}

func TestRenderUsageTable(t *testing.T) {
	records := []usage.SessionUsage{
		{
			AgentName:        "nova",
			Backend:          "claude",
			TaskID:           "kv31p.4",
			InputTokens:      456000,
			OutputTokens:     123000,
			CacheReadTokens:  10000,
			CacheWriteTokens: 5000,
			EstimatedCostUSD: 4.56,
			StartedAt:        time.Date(2026, 2, 27, 14, 30, 0, 0, time.UTC),
			EndedAt:          time.Date(2026, 2, 27, 14, 42, 0, 0, time.UTC),
			ExitCode:         0,
		},
	}

	var f usage.Filter

	// We can't easily capture fmt.Print output in a test, but we can verify
	// the helper functions produce correct output for the data
	agg := aggregateUsage(records)
	if agg.SessionCount != 1 {
		t.Fatalf("Expected 1 session, got %d", agg.SessionCount)
	}

	// Verify date range formatting
	dateRange := formatDateRange(f, records)
	if !strings.Contains(dateRange, "2026-02-27") {
		t.Errorf("Expected date range to contain 2026-02-27, got %q", dateRange)
	}

	oldVerbose := usageVerbose
	usageVerbose = true
	t.Cleanup(func() { usageVerbose = oldVerbose })
	out := captureCleanupStdout(t, func() {
		renderUsageTable(records, f)
	})
	for _, want := range []string{"USAGE SUMMARY", "TOTALS", "BY AGENT", "BY BACKEND", "SESSIONS", "nova"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderUsageTable output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderUsageJSON(t *testing.T) {
	records := []usage.SessionUsage{
		{
			AgentName:        "nova",
			Backend:          "claude",
			InputTokens:      100000,
			OutputTokens:     50000,
			EstimatedCostUSD: 1.50,
			StartedAt:        time.Date(2026, 2, 27, 14, 30, 0, 0, time.UTC),
			EndedAt:          time.Date(2026, 2, 27, 14, 42, 0, 0, time.UTC),
		},
	}

	agg := aggregateUsage(records)

	// Verify the JSON output structure by serializing and deserializing
	type jsonOutput struct {
		TotalInputTokens  int64   `json:"total_input_tokens"`
		TotalOutputTokens int64   `json:"total_output_tokens"`
		TotalCost         float64 `json:"total_cost"`
		SessionCount      int     `json:"session_count"`
		ByAgent           []struct {
			Name string  `json:"name"`
			Cost float64 `json:"cost"`
		} `json:"by_agent"`
	}

	data, err := json.Marshal(map[string]any{
		"total_input_tokens":  agg.TotalInput,
		"total_output_tokens": agg.TotalOutput,
		"total_cost":          agg.TotalCost,
		"session_count":       agg.SessionCount,
	})
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var out jsonOutput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if out.TotalInputTokens != 100000 {
		t.Errorf("TotalInputTokens = %d, want 100000", out.TotalInputTokens)
	}
	if out.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", out.SessionCount)
	}

	jsonOut := captureCleanupStdout(t, func() {
		renderUsageJSON(records)
	})
	if !strings.Contains(jsonOut, `"total_input_tokens": 100000`) {
		t.Fatalf("renderUsageJSON output missing totals:\n%s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"name": "nova"`) {
		t.Fatalf("renderUsageJSON output missing agent summary:\n%s", jsonOut)
	}
}

func TestFormatDateRange(t *testing.T) {
	now := time.Now()
	records := []usage.SessionUsage{
		{StartedAt: now.Add(-48 * time.Hour)},
		{StartedAt: now.Add(-24 * time.Hour)},
		{StartedAt: now},
	}

	t.Run("no filter derives from records", func(t *testing.T) {
		result := formatDateRange(usage.Filter{}, records)
		if !strings.Contains(result, "\u2192") {
			t.Errorf("Expected arrow in date range, got %q", result)
		}
	})

	t.Run("with since filter", func(t *testing.T) {
		f := usage.Filter{Since: now.Add(-72 * time.Hour)}
		result := formatDateRange(f, records)
		if !strings.Contains(result, "\u2192") {
			t.Errorf("Expected arrow in date range, got %q", result)
		}
		if !strings.Contains(result, "...") {
			t.Errorf("Expected ... for open-ended until, got %q", result)
		}
	})

	t.Run("empty records no filter", func(t *testing.T) {
		result := formatDateRange(usage.Filter{}, nil)
		if result != "No data" {
			t.Errorf("Expected 'No data', got %q", result)
		}
	})
}

func TestRenderUsageTableBoxDrawing(t *testing.T) {
	records := []usage.SessionUsage{
		{
			AgentName:        "nova",
			Backend:          "claude",
			InputTokens:      456000,
			OutputTokens:     123000,
			EstimatedCostUSD: 4.56,
			StartedAt:        time.Date(2026, 2, 27, 14, 30, 0, 0, time.UTC),
			EndedAt:          time.Date(2026, 2, 27, 14, 42, 0, 0, time.UTC),
		},
	}

	// Verify that the aggregation and formatting helpers produce consistent results
	agg := aggregateUsage(records)

	// Verify agent summary
	if len(agg.ByAgent) != 1 || agg.ByAgent[0].Name != "nova" {
		t.Errorf("Expected 1 agent 'nova', got %v", agg.ByAgent)
	}

	// Verify the box-drawing functions exist and produce correct width
	top := renderBoxTop()
	if !strings.HasPrefix(top, "╔") || !strings.HasSuffix(strings.TrimRight(top, "\n"), "╗") {
		t.Errorf("renderBoxTop format wrong: %q", top)
	}

	line := renderBoxLine(" USAGE SUMMARY")
	if !strings.HasPrefix(line, "║") || !strings.HasSuffix(strings.TrimRight(line, "\n"), "║") {
		t.Errorf("renderBoxLine format wrong: %q", line)
	}

	// Verify display width consistency
	width := displayWidth(strings.TrimRight(top, "\n"))
	lineWidth := displayWidth(strings.TrimRight(line, "\n"))
	if width != lineWidth {
		t.Errorf("Box width mismatch: top=%d, line=%d", width, lineWidth)
	}
}

func TestBuildUsageFilterBranches(t *testing.T) {
	oldAgent, oldBackend, oldEpic := usageAgent, usageBackend, usageEpic
	oldSince, oldUntil := usageSince, usageUntil
	oldToday, oldWeek := usageToday, usageWeek
	t.Cleanup(func() {
		usageAgent, usageBackend, usageEpic = oldAgent, oldBackend, oldEpic
		usageSince, usageUntil = oldSince, oldUntil
		usageToday, usageWeek = oldToday, oldWeek
	})

	usageAgent, usageBackend, usageEpic = "nova", "codex", "epic-1"
	usageSince, usageUntil = "2026-01-02", "2026-01-03"
	usageToday, usageWeek = false, false
	f, err := buildUsageFilter()
	if err != nil {
		t.Fatalf("buildUsageFilter valid dates: %v", err)
	}
	if f.AgentName != "nova" || f.Backend != "codex" || f.EpicID != "epic-1" {
		t.Fatalf("filter identity fields = %+v", f)
	}
	if f.Since.Format("2006-01-02") != "2026-01-02" || f.Until.Format("2006-01-02") != "2026-01-03" {
		t.Fatalf("filter dates = since %s until %s", f.Since, f.Until)
	}

	usageSince, usageUntil = "bad", ""
	if _, err := buildUsageFilter(); err == nil || !strings.Contains(err.Error(), "invalid --since") {
		t.Fatalf("invalid since err = %v", err)
	}

	usageSince, usageUntil = "2026-01-04", "2026-01-03"
	if _, err := buildUsageFilter(); err == nil || !strings.Contains(err.Error(), "after --since") {
		t.Fatalf("until-before-since err = %v", err)
	}

	usageSince, usageUntil = "", "bad"
	if _, err := buildUsageFilter(); err == nil || !strings.Contains(err.Error(), "invalid --until") {
		t.Fatalf("invalid until err = %v", err)
	}

	usageUntil = ""
	usageToday = true
	f, err = buildUsageFilter()
	if err != nil {
		t.Fatalf("today filter: %v", err)
	}
	if f.Since.IsZero() {
		t.Fatal("today filter did not set Since")
	}
	usageToday = false
	usageWeek = true
	f, err = buildUsageFilter()
	if err != nil {
		t.Fatalf("week filter: %v", err)
	}
	if f.Since.IsZero() {
		t.Fatal("week filter did not set Since")
	}
}
