//go:build e2e
// +build e2e

package cli

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageRecord is a lightweight fixture struct matching usage.SessionUsage JSON tags.
// We avoid importing internal/usage so E2E tests depend only on the compiled binary.
type usageRecord struct {
	AgentName        string    `json:"agent_name"`
	Backend          string    `json:"backend"`
	TaskID           string    `json:"task_id,omitempty"`
	EpicID           string    `json:"epic_id,omitempty"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	EstimatedCostUSD float64   `json:"estimated_cost_usd"`
	StartedAt        time.Time `json:"started_at"`
	EndedAt          time.Time `json:"ended_at"`
	ExitCode         int       `json:"exit_code"`
	Model            string    `json:"model,omitempty"`
	SessionID        string    `json:"session_id,omitempty"`
}

// writeUsageFixture writes a usage.jsonl file from the given records into dir.
func writeUsageFixture(t *testing.T, dir string, records []usageRecord) {
	t.Helper()
	var lines []byte
	for _, rec := range records {
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal usage record: %v", err)
		}
		lines = append(lines, data...)
		lines = append(lines, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "usage.jsonl"), lines, 0o644); err != nil {
		t.Fatalf("write usage.jsonl: %v", err)
	}
}

// runLoomUsage runs `loom usage <args...>` with Dir set to dir and LOOM_CONFIG_DIR isolated.
func runLoomUsage(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	fullArgs := append([]string{"usage"}, args...)
	cmd := exec.Command(loom, fullArgs...)
	cmd.Dir = dir

	emptyConfigDir := t.TempDir()
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_CONFIG_DIR=") ||
			strings.HasPrefix(e, "LOOM_BACKEND=") ||
			strings.HasPrefix(e, "LOOM_WORKTREES_DIR=") {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered, "LOOM_CONFIG_DIR="+emptyConfigDir)
	filtered = append(filtered, "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = filtered

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom usage: %v", err)
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// sampleRecords returns a reusable fixture set of 4 usage records.
func sampleRecords() []usageRecord {
	return []usageRecord{
		{
			AgentName:        "nova",
			Backend:          "claude",
			EpicID:           "epic-1",
			TaskID:           "task-1",
			InputTokens:      500000,
			OutputTokens:     200000,
			CacheReadTokens:  50000,
			CacheWriteTokens: 25000,
			EstimatedCostUSD: 4.50,
			StartedAt:        time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC),
			EndedAt:          time.Date(2026, 2, 25, 10, 12, 0, 0, time.UTC),
			ExitCode:         0,
		},
		{
			AgentName:        "falcon",
			Backend:          "claude",
			EpicID:           "epic-1",
			TaskID:           "task-2",
			InputTokens:      300000,
			OutputTokens:     150000,
			CacheReadTokens:  30000,
			CacheWriteTokens: 15000,
			EstimatedCostUSD: 2.80,
			StartedAt:        time.Date(2026, 2, 26, 14, 0, 0, 0, time.UTC),
			EndedAt:          time.Date(2026, 2, 26, 14, 20, 0, 0, time.UTC),
			ExitCode:         0,
		},
		{
			AgentName:        "nova",
			Backend:          "codex",
			EpicID:           "epic-2",
			TaskID:           "task-3",
			InputTokens:      100000,
			OutputTokens:     50000,
			CacheReadTokens:  0,
			CacheWriteTokens: 0,
			EstimatedCostUSD: 0.75,
			StartedAt:        time.Date(2026, 2, 27, 9, 0, 0, 0, time.UTC),
			EndedAt:          time.Date(2026, 2, 27, 9, 5, 0, 0, time.UTC),
			ExitCode:         1,
		},
		{
			AgentName:        "drift",
			Backend:          "claude",
			EpicID:           "epic-2",
			TaskID:           "task-4",
			InputTokens:      800000,
			OutputTokens:     400000,
			CacheReadTokens:  100000,
			CacheWriteTokens: 50000,
			EstimatedCostUSD: 8.20,
			StartedAt:        time.Date(2026, 2, 28, 16, 0, 0, 0, time.UTC),
			EndedAt:          time.Date(2026, 2, 28, 16, 45, 0, 0, time.UTC),
			ExitCode:         0,
		},
	}
}

// --- Basic invocation tests ---

func TestE2E_UsageHelp(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	stdout, _, exitCode := runLoomUsage(t, t.TempDir(), "--help")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	for _, want := range []string{"Display token usage and cost summaries", "--agent", "--format", "--verbose"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, stdout)
		}
	}
}

func TestE2E_UsageNoData(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	stdout, _, exitCode := runLoomUsage(t, t.TempDir())
	if exitCode != 0 {
		t.Fatalf("expected exit 0 with no data, got %d", exitCode)
	}
	if !strings.Contains(stdout, "No usage data found. Run agents in auto-mode to generate usage data.") {
		t.Errorf("expected 'No usage data found' message, got:\n%s", stdout)
	}
}

func TestE2E_UsageExitCode(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	_, _, exitCode := runLoomUsage(t, dir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
}

// --- Table output tests ---

func TestE2E_UsageTableOutput(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	for _, want := range []string{
		"USAGE SUMMARY",
		"TOTALS",
		"Input tokens:",
		"Output tokens:",
		"Cache reads:",
		"Cache writes:",
		"Estimated cost:",
		"Sessions:",
		"BY AGENT",
		"nova",
		"falcon",
		"drift",
		"BY BACKEND",
		"claude",
		"codex",
		"╔",
		"╚",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, stdout)
		}
	}

	// Session count should be 4
	if !strings.Contains(stdout, "4") {
		t.Errorf("expected session count 4 in output\nfull output:\n%s", stdout)
	}
}

func TestE2E_UsageTableVerbose(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--verbose")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	for _, want := range []string{
		"SESSIONS",
		"exit:0",
		"exit:1",
		"2026-02-25",
		"task-1",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, stdout)
		}
	}
}

func TestE2E_UsageTableSingleRecord(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords()[:1])

	stdout, _, exitCode := runLoomUsage(t, dir)
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	if !strings.Contains(stdout, "nova") {
		t.Errorf("expected 'nova' in BY AGENT section\nfull output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "claude") {
		t.Errorf("expected 'claude' in BY BACKEND section\nfull output:\n%s", stdout)
	}
	// Session count should show 1
	if !strings.Contains(stdout, "Sessions:") {
		t.Errorf("expected 'Sessions:' in output\nfull output:\n%s", stdout)
	}
}

// --- JSON output tests ---

// usageJSONOutput mirrors the JSON output structure from renderUsageJSON.
type usageJSONOutput struct {
	TotalInputTokens      int64   `json:"total_input_tokens"`
	TotalOutputTokens     int64   `json:"total_output_tokens"`
	TotalCacheReadTokens  int64   `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int64   `json:"total_cache_write_tokens"`
	TotalCost             float64 `json:"total_cost"`
	SessionCount          int     `json:"session_count"`
	ByAgent               []struct {
		Name string  `json:"name"`
		Cost float64 `json:"cost"`
	} `json:"by_agent"`
	ByBackend []struct {
		Name string  `json:"name"`
		Cost float64 `json:"cost"`
	} `json:"by_backend"`
	Sessions []json.RawMessage `json:"sessions"`
}

func TestE2E_UsageJSONOutput(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nraw:\n%s", err, stdout)
	}

	if out.TotalInputTokens != 1700000 {
		t.Errorf("total_input_tokens = %d, want 1700000", out.TotalInputTokens)
	}
	if out.TotalOutputTokens != 800000 {
		t.Errorf("total_output_tokens = %d, want 800000", out.TotalOutputTokens)
	}
	if out.SessionCount != 4 {
		t.Errorf("session_count = %d, want 4", out.SessionCount)
	}
	if math.Abs(out.TotalCost-16.25) > 0.01 {
		t.Errorf("total_cost = %f, want ~16.25", out.TotalCost)
	}
	if len(out.ByAgent) != 3 {
		t.Errorf("by_agent has %d entries, want 3", len(out.ByAgent))
	}
	if len(out.ByBackend) != 2 {
		t.Errorf("by_backend has %d entries, want 2", len(out.ByBackend))
	}
	if len(out.Sessions) != 4 {
		t.Errorf("sessions has %d entries, want 4", len(out.Sessions))
	}
}

func TestE2E_UsageJSONAgentOrder(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if len(out.ByAgent) == 0 {
		t.Fatal("by_agent is empty")
	}
	// drift has highest cost ($8.20), should be first (cost-descending sort)
	if out.ByAgent[0].Name != "drift" {
		t.Errorf("by_agent[0].name = %q, want 'drift' (highest cost)", out.ByAgent[0].Name)
	}
}

// --- Filtering tests ---

func TestE2E_UsageFilterByAgent(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json", "--agent", "nova")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if out.SessionCount != 2 {
		t.Errorf("session_count = %d, want 2 (nova has 2 sessions)", out.SessionCount)
	}
}

func TestE2E_UsageFilterByBackend(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json", "--backend", "codex")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if out.SessionCount != 1 {
		t.Errorf("session_count = %d, want 1 (codex has 1 session)", out.SessionCount)
	}
}

func TestE2E_UsageFilterByEpic(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json", "--epic", "epic-1")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if out.SessionCount != 2 {
		t.Errorf("session_count = %d, want 2 (epic-1 has 2 sessions)", out.SessionCount)
	}
}

func TestE2E_UsageFilterBySince(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json", "--since", "2026-02-27")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	// Records 3 (started 2026-02-27) and 4 (started 2026-02-28) match
	if out.SessionCount != 2 {
		t.Errorf("session_count = %d, want 2 (records started on/after 2026-02-27)", out.SessionCount)
	}
}

func TestE2E_UsageFilterByUntil(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json", "--until", "2026-02-26")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	// --until adds 24h-1ns, so 2026-02-26 includes all of that day.
	// Records 1 (ended 2026-02-25) and 2 (ended 2026-02-26) match.
	if out.SessionCount != 2 {
		t.Errorf("session_count = %d, want 2 (records ended on/before 2026-02-26)", out.SessionCount)
	}
}

func TestE2E_UsageFilterByDateRange(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	dir := t.TempDir()
	writeUsageFixture(t, dir, sampleRecords())

	stdout, _, exitCode := runLoomUsage(t, dir, "--format", "json", "--since", "2026-02-26", "--until", "2026-02-27")
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}

	var out usageJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	// Records 2 (started 2026-02-26, ended 2026-02-26) and 3 (started 2026-02-27, ended 2026-02-27)
	if out.SessionCount != 2 {
		t.Errorf("session_count = %d, want 2 (records in date range 2026-02-26 to 2026-02-27)", out.SessionCount)
	}
}

// --- Error handling tests ---

func TestE2E_UsageInvalidSinceDate(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	_, stderr, exitCode := runLoomUsage(t, t.TempDir(), "--since", "not-a-date")
	if exitCode == 0 {
		t.Fatal("expected non-zero exit code for invalid --since date")
	}
	if !strings.Contains(stderr, "Error: invalid --since date format, expected YYYY-MM-DD") {
		t.Errorf("expected --since error message in stderr, got:\n%s", stderr)
	}
}

func TestE2E_UsageInvalidUntilDate(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	_, stderr, exitCode := runLoomUsage(t, t.TempDir(), "--until", "2026/02/27")
	if exitCode == 0 {
		t.Fatal("expected non-zero exit code for invalid --until date")
	}
	if !strings.Contains(stderr, "Error: invalid --until date format, expected YYYY-MM-DD") {
		t.Errorf("expected --until error message in stderr, got:\n%s", stderr)
	}
}

func TestE2E_UsageUntilBeforeSince(t *testing.T) {
	t.Parallel()
	loomBinaryPath(t)

	_, stderr, exitCode := runLoomUsage(t, t.TempDir(), "--since", "2026-03-01", "--until", "2026-02-01")
	if exitCode == 0 {
		t.Fatal("expected non-zero exit code when --until is before --since")
	}
	if !strings.Contains(stderr, "Error: --until must be after --since") {
		t.Errorf("expected reversed date range error in stderr, got:\n%s", stderr)
	}
}
