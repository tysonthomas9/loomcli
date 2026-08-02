package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/transcript/claudecode"
)

// TestExtractLeafUsage_ParsesResultEntryUsage proves the supervisor recovers the
// TS leaf's token/cost usage from the terminal `result` entry's `output` field —
// the channel that fixes the daemon session-metadata tokens=0 finding (the reaped
// worker's collector-aware finalize never runs, so the supervisor sources usage here).
func TestExtractLeafUsage_ParsesResultEntryUsage(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"role":"system","type":"session_meta","text":"local-cli-codex session"}`,
		`{"role":"assistant","type":"text","text":"done"}`,
		`{"role":"system","type":"result","text":"completed","output":"{\"input_tokens\":8000,\"output_tokens\":300,\"cache_read_tokens\":12,\"cache_write_tokens\":7,\"cost_usd\":0.42}"}`,
	}, "\n") + "\n")

	u := extractLeafUsage(data)
	if u.InputTokens != 8000 || u.OutputTokens != 300 || u.CacheReadTokens != 12 || u.CacheWriteTokens != 7 {
		t.Fatalf("token mismatch: %+v", u)
	}
	if u.cost() != 0.42 {
		t.Errorf("cost = %v, want 0.42", u.cost())
	}
}

// TestExtractLeafUsage_RawStreamHasNoUsage proves the Go leaf's raw backend stream
// (no canonical `result` entry) yields zero usage from THIS source. That is why
// the Go leaf needs the second source — see harnessLeafUsage below.
func TestExtractLeafUsage_RawStreamHasNoUsage(t *testing.T) {
	data := []byte(`{"type":"response_item","payload":{"role":"assistant"}}` + "\n")
	if u := extractLeafUsage(data); u != (leafUsage{}) {
		t.Errorf("raw stream must yield zero usage, got %+v", u)
	}
}

// TestLeafUsageCost_PrefersCostOverEstimate pins the cost() precedence: the
// backend-reported cost_usd wins; estimated_cost_usd is the fallback.
func TestLeafUsageCost_PrefersCostOverEstimate(t *testing.T) {
	if got := (leafUsage{CostUSD: 1.5, EstimatedCostUSD: 9.9}).cost(); got != 1.5 {
		t.Errorf("cost() = %v, want 1.5 (cost_usd wins)", got)
	}
	if got := (leafUsage{EstimatedCostUSD: 2.5}).cost(); got != 2.5 {
		t.Errorf("cost() = %v, want 2.5 (estimated fallback)", got)
	}
}

// TestHarnessLeafUsage_GoLeafTranscriptYieldsNonZero is the regression test for
// the finding this file's back-fill exists to close: a Go-leaf daemon run used
// to finalize with tokens=0 because the only source was the TS leaf's `result`
// entry, which the Go leaf never writes. Reading Claude Code's own transcript
// instead yields the real numbers — and a priced estimate to go with them.
func TestHarnessLeafUsage_GoLeafTranscriptYieldsNonZero(t *testing.T) {
	// Pricing overrides would make the cost assertion depend on the environment.
	t.Setenv("LOOM_COST_PER_MTOK_INPUT", "")
	t.Setenv("LOOM_COST_PER_MTOK_OUTPUT", "")

	workDir := "/tmp/loom-supervisor-usage/falcon"
	sessionID := "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	stageClaudeTranscript(t, workDir, sessionID, `{"type":"assistant","uuid":"u1","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":4000,"output_tokens":900,"cache_read_input_tokens":120000,"cache_creation_input_tokens":3000}}}
{"type":"assistant","uuid":"u2","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","name":"Bash"}],"usage":{"input_tokens":4000,"output_tokens":900,"cache_read_input_tokens":120000,"cache_creation_input_tokens":3000}}}
`)

	// No hint: the success path clears the lock's carried claude_session_id, so
	// this is the shape the supervisor actually sees on a clean run.
	got := harnessLeafUsage("claude", workDir, "", time.Now().Add(-10*time.Minute))

	if got == (leafUsage{}) {
		t.Fatal("harnessLeafUsage = zero; the Go leaf is still recording tokens=0")
	}
	want := leafUsage{InputTokens: 4000, OutputTokens: 900, CacheReadTokens: 120000, CacheWriteTokens: 3000}
	if got.InputTokens != want.InputTokens || got.OutputTokens != want.OutputTokens ||
		got.CacheReadTokens != want.CacheReadTokens || got.CacheWriteTokens != want.CacheWriteTokens {
		t.Errorf("tokens = %+v, want %+v (one API call counted once)", got, want)
	}
	if got.EstimatedCostUSD <= 0 {
		t.Errorf("EstimatedCostUSD = %v, want > 0", got.EstimatedCostUSD)
	}
	if got.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 — a locally priced figure is an estimate, not a reported cost", got.CostUSD)
	}
	if got.cost() != got.EstimatedCostUSD {
		t.Errorf("cost() = %v, want the estimate %v", got.cost(), got.EstimatedCostUSD)
	}
}

// TestHarnessLeafUsage_MissesAreZero pins the best-effort contract: every way
// the back-fill can come up empty degrades to the zero value that used to be
// recorded unconditionally, never to an error that could fail the finalize.
func TestHarnessLeafUsage_MissesAreZero(t *testing.T) {
	workDir := "/tmp/loom-supervisor-usage/falcon"
	sessionID := "77777777-8888-9999-aaaa-cccccccccccc"
	stageClaudeTranscript(t, workDir, sessionID,
		`{"type":"assistant","uuid":"u1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":10,"output_tokens":1}}}`+"\n")

	since := time.Now().Add(-10 * time.Minute)
	tests := []struct {
		name             string
		backend, workDir string
		since            time.Time
	}{
		{name: "backend keeps no readable transcript", backend: "opencode", workDir: workDir, since: since},
		{name: "unset backend", backend: "", workDir: workDir, since: since},
		{name: "working directory never ran an agent", backend: "claude", workDir: "/tmp/loom-supervisor-usage/ghost", since: since},
		{name: "nothing written since the session started", backend: "claude", workDir: workDir, since: time.Now().Add(time.Hour)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := harnessLeafUsage(tc.backend, tc.workDir, "", tc.since); got != (leafUsage{}) {
				t.Errorf("harnessLeafUsage = %+v, want the zero value", got)
			}
		})
	}
}

// TestResolveLeafTokens_PrecedenceBetweenTheTwoSources pins the ordering: the TS
// leaf's own `result` accounting wins outright and the harness read is not even
// attempted, so this change cannot perturb the leaf that already worked.
func TestResolveLeafTokens_PrecedenceBetweenTheTwoSources(t *testing.T) {
	fromResult := leafUsage{InputTokens: 8000, OutputTokens: 300, CostUSD: 0.42}
	backfill := leafUsage{InputTokens: 1, OutputTokens: 1}

	called := 0
	thunk := func() leafUsage { called++; return backfill }

	if got := resolveLeafTokens(fromResult, thunk); got != fromResult {
		t.Errorf("with a TS result entry got %+v, want %+v", got, fromResult)
	}
	if called != 0 {
		t.Errorf("harness back-fill ran %d times on the TS path, want 0", called)
	}

	if got := resolveLeafTokens(leafUsage{}, thunk); got != backfill {
		t.Errorf("with no TS result entry got %+v, want the back-fill %+v", got, backfill)
	}
	if called != 1 {
		t.Errorf("harness back-fill ran %d times on the Go path, want 1", called)
	}
}

// stageClaudeTranscript writes body where Claude Code would write it for
// workDir, under a CLAUDE_CONFIG_DIR scoped to this test.
func stageClaudeTranscript(t *testing.T, workDir, sessionID, body string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	dir := filepath.Join(configDir, "projects", claudecode.EncodedCWD(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}
