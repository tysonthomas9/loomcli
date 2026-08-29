package cleanup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// setUsageSource swaps the --source flag value for one test and restores it.
func setUsageSource(t *testing.T, src string) {
	t.Helper()
	orig := usageSource
	usageSource = src
	t.Cleanup(func() { usageSource = orig })
}

// writeSessionsIndex writes <dir>/sessions/index.jsonl from raw JSON objects.
func writeSessionsIndex(t *testing.T, dir string, lines ...map[string]any) {
	t.Helper()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("marshal index line: %v", err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(sessDir, "index.jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write index.jsonl: %v", err)
	}
}

// writeLegacyLedger writes <dir>/usage.jsonl through the legacy store.
func writeLegacyLedger(t *testing.T, dir string, recs ...usage.SessionUsage) {
	t.Helper()
	store, err := usage.NewStore(dir)
	if err != nil {
		t.Fatalf("new usage store: %v", err)
	}
	for _, r := range recs {
		if err := store.Append(r); err != nil {
			t.Fatalf("append usage record: %v", err)
		}
	}
}

// TestReadUsageRecords_SourceSelection points the two ledgers at deliberately
// disagreeing data, so a reader that silently fell back to the wrong file
// cannot pass.
func TestReadUsageRecords_SourceSelection(t *testing.T) {
	dir := t.TempDir()
	writeSessionsIndex(t, dir, map[string]any{
		"session_id": "s1", "agent_name": "sessions-agent", "backend": "claude",
		"status": "completed", "started_at": "2026-08-29T09:00:00Z",
		"ended_at": "2026-08-29T09:30:00Z", "duration_s": 1800,
		"input_tokens": 1000, "output_tokens": 2000,
	})
	writeLegacyLedger(t, dir, usage.SessionUsage{
		AgentName:    "legacy-agent",
		Backend:      "codex",
		InputTokens:  7,
		OutputTokens: 9,
		StartedAt:    time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC),
		EndedAt:      time.Date(2026, 8, 29, 9, 1, 0, 0, time.UTC),
	})

	t.Run("default is the sessions index", func(t *testing.T) {
		setUsageSource(t, usageSourceSessions)
		recs, path, err := readUsageRecords(dir, usage.Filter{})
		if err != nil {
			t.Fatalf("readUsageRecords: %v", err)
		}
		if len(recs) != 1 || recs[0].AgentName != "sessions-agent" {
			t.Fatalf("default source read %+v, want the sessions ledger", recs)
		}
		if recs[0].InputTokens != 1000 {
			t.Errorf("input tokens = %d, want the non-zero fixture value 1000", recs[0].InputTokens)
		}
		if !strings.HasSuffix(path, filepath.Join("sessions", "index.jsonl")) {
			t.Errorf("ledger path = %q, want the sessions index", path)
		}
	})

	t.Run("legacy reads usage.jsonl", func(t *testing.T) {
		setUsageSource(t, usageSourceLegacy)
		recs, path, err := readUsageRecords(dir, usage.Filter{})
		if err != nil {
			t.Fatalf("readUsageRecords: %v", err)
		}
		if len(recs) != 1 || recs[0].AgentName != "legacy-agent" {
			t.Fatalf("--source legacy read %+v, want the usage.jsonl ledger", recs)
		}
		if !strings.HasSuffix(path, "usage.jsonl") {
			t.Errorf("ledger path = %q, want usage.jsonl", path)
		}
	})

	t.Run("unknown source is an error", func(t *testing.T) {
		setUsageSource(t, "nonsense")
		if _, _, err := readUsageRecords(dir, usage.Filter{}); err == nil {
			t.Fatal("expected an error for an unknown --source")
		}
	})
}

func TestBuildUsageTable_HeaderNamesLedgerAndCount(t *testing.T) {
	records := []usage.SessionUsage{
		{AgentName: "nova", Backend: "claude", InputTokens: 1000, OutputTokens: 500,
			StartedAt: time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC), Status: "completed"},
		{AgentName: "orion", Backend: "codex", InputTokens: 2000, OutputTokens: 300,
			StartedAt: time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC), Status: "completed"},
	}

	out := buildUsageTable(records, usage.Filter{}, "/w/PUPPET/sessions/index.jsonl")

	if !strings.Contains(out, "sessions/index.jsonl") {
		t.Errorf("header should name the ledger path, got:\n%s", out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("header should carry the record count, got:\n%s", out)
	}
	// Guard: the fixture must have real tokens, or the assertions below are vacuous.
	if !strings.Contains(out, "3,000") {
		t.Errorf("expected the non-zero input total 3,000 in:\n%s", out)
	}
}

func TestBuildUsageTable_CostNotReported(t *testing.T) {
	base := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)

	t.Run("all zero cost degrades to a plain statement", func(t *testing.T) {
		records := []usage.SessionUsage{
			{AgentName: "nova", Backend: "claude", InputTokens: 1000, StartedAt: base, Status: "completed"},
			{AgentName: "nova", Backend: "claude", InputTokens: 0, StartedAt: base, Status: "failed"},
		}
		out := buildUsageTable(records, usage.Filter{}, "/w/sessions/index.jsonl")
		if !strings.Contains(out, "not reported by backend") {
			t.Errorf("expected 'not reported by backend', got:\n%s", out)
		}
		if strings.Contains(out, "$0.00") {
			t.Errorf("a cost nobody reported must not render as $0.00, got:\n%s", out)
		}
		if !strings.Contains(out, "Sessions:  2") {
			t.Errorf("zero-token failed sessions must still be counted, got:\n%s", out)
		}
	})

	t.Run("a reported cost still renders as a number", func(t *testing.T) {
		records := []usage.SessionUsage{
			{AgentName: "nova", Backend: "claude", InputTokens: 10, EstimatedCostUSD: 1.25,
				StartedAt: base, Status: "completed"},
		}
		out := buildUsageTable(records, usage.Filter{}, "/w/sessions/index.jsonl")
		if !strings.Contains(out, "$1.25") {
			t.Errorf("expected $1.25, got:\n%s", out)
		}
		if strings.Contains(out, "not reported by backend") {
			t.Errorf("a reported cost must not degrade, got:\n%s", out)
		}
	})

	t.Run("a genuine zero in a mixed set still prints as a number", func(t *testing.T) {
		records := []usage.SessionUsage{
			{AgentName: "nova", Backend: "claude", EstimatedCostUSD: 1.0, StartedAt: base},
			{AgentName: "orion", Backend: "codex", EstimatedCostUSD: -1.0, StartedAt: base},
		}
		out := buildUsageTable(records, usage.Filter{}, "/w/sessions/index.jsonl")
		if !strings.Contains(out, "$0.00") {
			t.Errorf("a measured total of zero must print as $0.00, got:\n%s", out)
		}
		if strings.Contains(out, "not reported by backend") {
			t.Errorf("cost was reported; must not degrade, got:\n%s", out)
		}
	})
}

func TestSessionDuration(t *testing.T) {
	start := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)

	cases := []struct {
		name string
		rec  usage.SessionUsage
		want time.Duration
	}{
		{"duration_s wins", usage.SessionUsage{StartedAt: start, EndedAt: end, DurationS: 600}, 10 * time.Minute},
		{"falls back to ended_at", usage.SessionUsage{StartedAt: start, EndedAt: end}, 30 * time.Minute},
		{"running session has no end", usage.SessionUsage{StartedAt: start, Status: "running"}, 0},
		{"ended before started", usage.SessionUsage{StartedAt: end, EndedAt: start}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionDuration(tc.rec); got != tc.want {
				t.Errorf("sessionDuration = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBuildUsageTable_RunningSessionDuration guards the regression this ticket
// fixes: a running session's zero EndedAt used to render as a multi-thousand
// hour duration.
func TestBuildUsageTable_RunningSessionDuration(t *testing.T) {
	orig := usageVerbose
	usageVerbose = true
	t.Cleanup(func() { usageVerbose = orig })

	records := []usage.SessionUsage{{
		AgentName: "nova", Backend: "claude", TaskID: "T-1",
		InputTokens: 1234, StartedAt: time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC),
		Status: "running",
	}}
	out := buildUsageTable(records, usage.Filter{}, "/w/sessions/index.jsonl")
	if !strings.Contains(out, "0s") {
		t.Errorf("running session should render a zero duration, got:\n%s", out)
	}
	if strings.Contains(out, "h") && strings.Contains(out, "0000h") {
		t.Errorf("running session rendered a nonsense duration:\n%s", out)
	}
}

func TestEmptyUsageMessage(t *testing.T) {
	t.Run("sessions source suggests the legacy ledger", func(t *testing.T) {
		setUsageSource(t, usageSourceSessions)
		msg := emptyUsageMessage("/w/PUPPET/sessions/index.jsonl")
		if !strings.Contains(msg, "/w/PUPPET/sessions/index.jsonl") {
			t.Errorf("message should name the empty file, got %q", msg)
		}
		if !strings.Contains(msg, "--source legacy") {
			t.Errorf("message should suggest --source legacy, got %q", msg)
		}
	})
	t.Run("legacy source does not suggest itself", func(t *testing.T) {
		setUsageSource(t, usageSourceLegacy)
		msg := emptyUsageMessage("/w/PUPPET/usage.jsonl")
		if !strings.Contains(msg, "usage.jsonl") {
			t.Errorf("message should name the empty file, got %q", msg)
		}
		if strings.Contains(msg, "--source legacy") {
			t.Errorf("legacy source should not suggest itself, got %q", msg)
		}
	})
}

func TestBuildUsageFilter_StatusFlag(t *testing.T) {
	orig := usageStatus
	usageStatus = "failed"
	t.Cleanup(func() { usageStatus = orig })

	f, err := buildUsageFilter()
	if err != nil {
		t.Fatalf("buildUsageFilter: %v", err)
	}
	if f.Status != "failed" {
		t.Errorf("filter status = %q, want failed", f.Status)
	}
}

// TestReadUsageRecords_StatusFilterNarrows proves --status reaches the session
// index rather than being accepted and ignored.
func TestReadUsageRecords_StatusFilterNarrows(t *testing.T) {
	setUsageSource(t, usageSourceSessions)
	dir := t.TempDir()
	writeSessionsIndex(t, dir,
		map[string]any{
			"session_id": "ok1", "agent_name": "nova", "backend": "claude",
			"status": "completed", "started_at": "2026-08-29T09:00:00Z",
			"ended_at": "2026-08-29T09:30:00Z", "input_tokens": 500,
		},
		map[string]any{
			"session_id": "bad1", "agent_name": "nova", "backend": "claude",
			"status": "failed", "started_at": "2026-08-29T10:00:00Z",
			"ended_at": "2026-08-29T10:00:05Z", "input_tokens": 0, "exit_code": 1,
		},
	)

	all, _, err := readUsageRecords(dir, usage.Filter{})
	if err != nil {
		t.Fatalf("readUsageRecords: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered read got %d records, want 2", len(all))
	}

	failed, _, err := readUsageRecords(dir, usage.Filter{Status: "failed"})
	if err != nil {
		t.Fatalf("readUsageRecords: %v", err)
	}
	if len(failed) != 1 || failed[0].SessionID != "bad1" {
		t.Fatalf("--status failed got %+v, want only bad1", failed)
	}
}
