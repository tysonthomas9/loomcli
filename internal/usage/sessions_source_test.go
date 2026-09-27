package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// indexRecord is the on-disk shape of one sessions/index.jsonl line. It is
// written as a literal map so the test fixture does not depend on the sessions
// package's Go types — the file format is the contract under test.
type indexRecord map[string]any

func writeIndex(t *testing.T, dir string, recs ...indexRecord) {
	t.Helper()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(sessDir, "index.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create index.jsonl: %v", err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, rec := range recs {
		if err := enc.Encode(rec); err != nil {
			t.Fatalf("encode record: %v", err)
		}
	}
}

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return v
}

// fixtureIndex writes a three-session ledger:
//   - s1 appears TWICE (a running entry then its finalized entry), which is
//     exactly how the real append-only index is written; only the finalized
//     record must count, or every total roughly doubles.
//   - s2 is a second completed session on a different agent/backend/epic.
//   - s3 is a failed session with zero tokens (a failed spawn) — a legitimate
//     row that must still be counted as a session.
func fixtureIndex(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeIndex(t, dir,
		indexRecord{
			"session_id": "s1", "agent_name": "nova", "backend": "claude",
			"task_id": "T-1", "epic_id": "E-1", "status": "running",
			"started_at":   "2026-08-28T10:00:00Z",
			"input_tokens": 1, "output_tokens": 1,
		},
		indexRecord{
			"session_id": "s1", "agent_name": "nova", "backend": "claude",
			"task_id": "T-1", "epic_id": "E-1", "status": "completed",
			"started_at": "2026-08-28T10:00:00Z", "ended_at": "2026-08-28T10:30:00Z",
			"duration_s":   1800,
			"input_tokens": 100, "output_tokens": 200,
			"cache_read_tokens": 300, "cache_write_tokens": 400,
		},
		indexRecord{
			"session_id": "s2", "agent_name": "orion", "backend": "codex",
			"task_id": "T-2", "epic_id": "E-2", "status": "completed",
			"started_at": "2026-08-29T09:00:00Z", "ended_at": "2026-08-29T09:10:00Z",
			"duration_s":   600,
			"input_tokens": 10, "output_tokens": 20,
			"cache_read_tokens": 30, "cache_write_tokens": 40,
		},
		indexRecord{
			"session_id": "s3", "agent_name": "nova", "backend": "claude",
			"task_id": "T-3", "status": "failed", "exit_code": 1,
			"started_at": "2026-08-29T11:00:00Z", "ended_at": "2026-08-29T11:00:05Z",
			"duration_s":   5,
			"input_tokens": 0, "output_tokens": 0,
		},
	)
	return dir
}

func totalTokens(recs []SessionUsage) int64 {
	var n int64
	for _, r := range recs {
		n += r.InputTokens + r.OutputTokens + r.CacheReadTokens + r.CacheWriteTokens
	}
	return n
}

func TestReadSessionUsage_DedupesAndAdapts(t *testing.T) {
	dir := fixtureIndex(t)

	recs, path, err := ReadSessionUsage(dir, Filter{})
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}

	if want := filepath.Join(dir, "sessions", "index.jsonl"); path != want {
		t.Errorf("ledger path = %q, want %q", path, want)
	}
	// 4 lines, 3 distinct sessions: the duplicate s1 must collapse.
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (index has 4 lines, 3 sessions)", len(recs))
	}

	// Guard: the fixture must carry real tokens, or this test would pass
	// against a reader that returns nothing useful.
	const wantTokens = int64(100 + 200 + 300 + 400 + 10 + 20 + 30 + 40)
	if wantTokens == 0 {
		t.Fatal("fixture guard: expected token total must be non-zero")
	}
	if got := totalTokens(recs); got != wantTokens {
		t.Errorf("total tokens = %d, want %d (a 2x overcount means the index was not deduped)", got, wantTokens)
	}

	byID := map[string]SessionUsage{}
	for _, r := range recs {
		byID[r.SessionID] = r
	}

	s1 := byID["s1"]
	if s1.Status != "completed" {
		t.Errorf("s1 status = %q, want completed (last record wins)", s1.Status)
	}
	if s1.InputTokens != 100 || s1.OutputTokens != 200 {
		t.Errorf("s1 tokens = %d/%d, want 100/200", s1.InputTokens, s1.OutputTokens)
	}
	if s1.DurationS != 1800 {
		t.Errorf("s1 duration = %v, want 1800", s1.DurationS)
	}
	if s1.EndedAt.IsZero() {
		t.Error("s1 ended_at should be set")
	}
	if s1.AgentName != "nova" || s1.Backend != "claude" || s1.TaskID != "T-1" || s1.EpicID != "E-1" {
		t.Errorf("s1 identity fields not adapted: %+v", s1)
	}

	s3 := byID["s3"]
	if s3.Status != "failed" {
		t.Errorf("s3 status = %q, want failed", s3.Status)
	}
	if s3.ExitCode != 1 {
		t.Errorf("s3 exit code = %d, want 1", s3.ExitCode)
	}
	if s3.InputTokens != 0 || s3.OutputTokens != 0 {
		t.Errorf("s3 should have zero tokens, got %d/%d", s3.InputTokens, s3.OutputTokens)
	}
}

func TestReadSessionUsage_Filters(t *testing.T) {
	dir := fixtureIndex(t)

	cases := []struct {
		name     string
		filter   Filter
		wantIDs  []string
		wantToks int64
	}{
		{"agent", Filter{AgentName: "nova"}, []string{"s1", "s3"}, 1000},
		{"backend", Filter{Backend: "codex"}, []string{"s2"}, 100},
		{"epic", Filter{EpicID: "E-2"}, []string{"s2"}, 100},
		{"task", Filter{TaskID: "T-1"}, []string{"s1"}, 1000},
		{"status", Filter{Status: "failed"}, []string{"s3"}, 0},
		{"since", Filter{Since: ts(t, "2026-08-29T00:00:00Z")}, []string{"s2", "s3"}, 100},
		{"until", Filter{Until: ts(t, "2026-08-28T23:59:59Z")}, []string{"s1"}, 1000},
		{"since+until", Filter{
			Since: ts(t, "2026-08-29T00:00:00Z"),
			Until: ts(t, "2026-08-29T10:00:00Z"),
		}, []string{"s2"}, 100},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs, _, err := ReadSessionUsage(dir, tc.filter)
			if err != nil {
				t.Fatalf("ReadSessionUsage: %v", err)
			}
			got := map[string]bool{}
			for _, r := range recs {
				got[r.SessionID] = true
			}
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("got %d records (%v), want %v", len(recs), got, tc.wantIDs)
			}
			for _, id := range tc.wantIDs {
				if !got[id] {
					t.Errorf("missing session %s (got %v)", id, got)
				}
			}
			if n := totalTokens(recs); n != tc.wantToks {
				t.Errorf("token total = %d, want %d", n, tc.wantToks)
			}
		})
	}
}

func TestReadSessionUsage_MissingIndex(t *testing.T) {
	dir := t.TempDir()

	recs, path, err := ReadSessionUsage(dir, Filter{})
	if err != nil {
		t.Fatalf("missing index should not be an error, got %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("got %d records, want 0", len(recs))
	}
	if want := filepath.Join(dir, "sessions", "index.jsonl"); path != want {
		t.Errorf("ledger path = %q, want %q even when the file is absent", path, want)
	}
}

func TestReadSessionUsage_CostIsPassedThroughNotDerived(t *testing.T) {
	dir := t.TempDir()
	writeIndex(t, dir,
		indexRecord{
			"session_id": "c1", "agent_name": "nova", "backend": "claude",
			"status": "completed", "started_at": "2026-08-29T09:00:00Z",
			"ended_at": "2026-08-29T09:05:00Z",
			// Non-zero tokens, no cost — the live fleet's shape.
			"input_tokens": 5000, "output_tokens": 700,
		},
		indexRecord{
			"session_id": "c2", "agent_name": "orion", "backend": "codex",
			"status": "completed", "started_at": "2026-08-29T09:00:00Z",
			"ended_at":     "2026-08-29T09:05:00Z",
			"input_tokens": 10, "estimated_cost_usd": 1.25,
		},
	)

	recs, _, err := ReadSessionUsage(dir, Filter{})
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	for _, r := range recs {
		switch r.SessionID {
		case "c1":
			if r.EstimatedCostUSD != 0 {
				t.Errorf("c1 cost = %v, want 0 — cost must never be derived from tokens", r.EstimatedCostUSD)
			}
			if r.InputTokens != 5000 {
				t.Errorf("c1 input tokens = %d, want 5000", r.InputTokens)
			}
		case "c2":
			if r.EstimatedCostUSD != 1.25 {
				t.Errorf("c2 cost = %v, want 1.25 passed through", r.EstimatedCostUSD)
			}
		}
	}
}

func TestReadSessionUsage_RunningSessionHasNoEndTime(t *testing.T) {
	dir := t.TempDir()
	// Started far enough in the past that stale-healing may reclassify it;
	// what matters here is only that no end time is invented.
	writeIndex(t, dir, indexRecord{
		"session_id": "r1", "agent_name": "nova", "backend": "claude",
		"status": "running", "started_at": time.Now().UTC().Format(time.RFC3339),
		"input_tokens": 42,
	})

	recs, _, err := ReadSessionUsage(dir, Filter{})
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if !recs[0].EndedAt.IsZero() {
		t.Errorf("running session got ended_at %v, want zero", recs[0].EndedAt)
	}
	if recs[0].DurationS != 0 {
		t.Errorf("running session got duration %v, want 0", recs[0].DurationS)
	}
	if recs[0].InputTokens != 42 {
		t.Errorf("input tokens = %d, want 42", recs[0].InputTokens)
	}
}

// TestReadSessionUsage_KnownAgentsAllowlist scopes the read to one of the two
// agents in the ledger, and pins that dedup still holds underneath it: s1 is
// written twice and must come back once, with its finalized token counts.
func TestReadSessionUsage_KnownAgentsAllowlist(t *testing.T) {
	dir := fixtureIndex(t)

	recs, _, err := ReadSessionUsage(dir, Filter{KnownAgents: []string{"nova"}})
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (nova's s1 and s3 only)", len(recs))
	}
	for _, r := range recs {
		if r.AgentName != "nova" {
			t.Errorf("record %q has agent %q, want only nova", r.SessionID, r.AgentName)
		}
	}
	// s1's running row carries 1+1 tokens and its finalized row 100+200+300+400;
	// s3 is a zero-token failure. Anything but 1000 means dedup broke.
	if got := totalTokens(recs); got != 1000 {
		t.Errorf("total tokens = %d, want 1000 (the finalized s1 row, counted once)", got)
	}
}

// TestReadSessionUsage_EmptyKnownAgentsReadsEverything is the invariant that
// keeps a workspace without configured agents behaving as it always has.
func TestReadSessionUsage_EmptyKnownAgentsReadsEverything(t *testing.T) {
	dir := fixtureIndex(t)

	for name, f := range map[string]Filter{
		"nil":   {KnownAgents: nil},
		"empty": {KnownAgents: []string{}},
	} {
		recs, _, err := ReadSessionUsage(dir, f)
		if err != nil {
			t.Fatalf("%s: ReadSessionUsage: %v", name, err)
		}
		if len(recs) != 3 {
			t.Errorf("%s: got %d records, want all 3", name, len(recs))
		}
	}
}

// TestReadSessionUsage_AgentNameAndKnownAgentsAreANDed: `loom usage --agent X`
// against an allowlist that does not name X returns nothing. That is correct,
// not a broken ledger — the empty-result message names the ledger path.
func TestReadSessionUsage_AgentNameAndKnownAgentsAreANDed(t *testing.T) {
	dir := fixtureIndex(t)

	recs, _, err := ReadSessionUsage(dir, Filter{AgentName: "orion", KnownAgents: []string{"nova"}})
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("got %d records, want 0 (AgentName and KnownAgents disagree)", len(recs))
	}
}
