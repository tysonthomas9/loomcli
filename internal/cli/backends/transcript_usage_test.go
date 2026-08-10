package backends

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/transcript"
	"github.com/olesho/harness-wrapper/pkg/transcript/claudecode"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// fakeUsageReader is a transcript.UsageReader that answers from memory, so the
// wiring can be proven without staging JSONL on disk.
type fakeUsageReader struct {
	usage *transcript.Usage
	err   error

	calls      int
	gotSID     string
	gotWorkDir string
}

func (f *fakeUsageReader) ReadUsage(harnessSessionID, workingDir string) (*transcript.Usage, error) {
	f.calls++
	f.gotSID = harnessSessionID
	f.gotWorkDir = workingDir
	return f.usage, f.err
}

// installUsageReader swaps the per-backend reader factory for one that always
// returns reader (nil means "this backend has no readable transcript").
func installUsageReader(t *testing.T, reader transcript.UsageReader) {
	t.Helper()
	orig := harnessUsageReaderFor
	harnessUsageReaderFor = func(string) transcript.UsageReader { return reader }
	t.Cleanup(func() { harnessUsageReaderFor = orig })
}

// TestAccumulateHarnessUsage_FeedsCollector is the core proof for the finding
// that Claude runs recorded 0 tokens / $0: the numbers a UsageReader returns
// must arrive intact on the Collector and survive Finalize into a SessionUsage.
//
// The two cases also pin the semantics asymmetry documented on
// SessionTokensFromHarnessUsage — Claude's input excludes its cache fields,
// Codex's already includes the cached subset — by asserting each is carried
// VERBATIM rather than reconciled with the other.
func TestAccumulateHarnessUsage_FeedsCollector(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		read    transcript.Usage
		want    usage.SessionUsage
	}{
		{
			name:    "claude keeps cache fields separate from input",
			backend: "claude",
			read: transcript.Usage{
				InputTokens:              1200,
				OutputTokens:             340,
				CacheReadInputTokens:     9000,
				CacheCreationInputTokens: 150,
			},
			want: usage.SessionUsage{
				InputTokens: 1200, OutputTokens: 340,
				CacheReadTokens: 9000, CacheWriteTokens: 150,
			},
		},
		{
			name:    "codex cached input is a subset, never re-added",
			backend: "codex",
			read: transcript.Usage{
				InputTokens:           5000, // already includes the 4096 cached
				OutputTokens:          220,
				CacheReadInputTokens:  4096,
				ReasoningOutputTokens: 700, // loom has no counter for this
			},
			want: usage.SessionUsage{
				InputTokens: 5000, OutputTokens: 220,
				CacheReadTokens: 4096, CacheWriteTokens: 0,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			read := tc.read
			reader := &fakeUsageReader{usage: &read}
			installUsageReader(t, reader)

			c := usage.NewCollector(tc.backend, "falcon")
			if !accumulateHarnessUsage(c, tc.backend, "sess-uuid", "/work/falcon") {
				t.Fatal("accumulateHarnessUsage = false, want true (usage was available)")
			}
			if reader.gotSID != "sess-uuid" || reader.gotWorkDir != "/work/falcon" {
				t.Errorf("ReadUsage(%q, %q), want (\"sess-uuid\", \"/work/falcon\")", reader.gotSID, reader.gotWorkDir)
			}

			in, out, cr, cw := c.Totals()
			if in != tc.want.InputTokens || out != tc.want.OutputTokens ||
				cr != tc.want.CacheReadTokens || cw != tc.want.CacheWriteTokens {
				t.Errorf("totals = (%d, %d, %d, %d), want (%d, %d, %d, %d)",
					in, out, cr, cw,
					tc.want.InputTokens, tc.want.OutputTokens,
					tc.want.CacheReadTokens, tc.want.CacheWriteTokens)
			}
		})
	}
}

// TestAccumulateHarnessUsage_MissesAreNoOps pins the "usage is strictly
// optional" contract: none of the ways a read can come up empty may disturb the
// collector, and every one of them reports false rather than erroring, so a
// caller physically cannot fail a turn over missing telemetry.
func TestAccumulateHarnessUsage_MissesAreNoOps(t *testing.T) {
	tests := []struct {
		name      string
		reader    transcript.UsageReader
		sessionID string
	}{
		{
			name:      "backend has no transcript reader",
			reader:    nil,
			sessionID: "sess-uuid",
		},
		{
			name:      "ReadUsage errors",
			reader:    &fakeUsageReader{err: errors.New("transcript: no such file")},
			sessionID: "sess-uuid",
		},
		{
			name:      "transcript carried no usage at all",
			reader:    &fakeUsageReader{usage: nil},
			sessionID: "sess-uuid",
		},
		{
			name:      "empty harness session id",
			reader:    &fakeUsageReader{usage: &transcript.Usage{InputTokens: 999}},
			sessionID: "",
		},
		{
			name:      "blank harness session id",
			reader:    &fakeUsageReader{usage: &transcript.Usage{InputTokens: 999}},
			sessionID: "   ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			installUsageReader(t, tc.reader)

			c := usage.NewCollector("claude", "falcon")
			if accumulateHarnessUsage(c, "claude", tc.sessionID, "/work/falcon") {
				t.Error("accumulateHarnessUsage = true, want false (nothing readable)")
			}
			if in, out, cr, cw := c.Totals(); in != 0 || out != 0 || cr != 0 || cw != 0 {
				t.Errorf("totals = (%d, %d, %d, %d), want all zero", in, out, cr, cw)
			}
			if ReadHarnessUsage("claude", tc.sessionID, "/work/falcon") != nil {
				t.Error("ReadHarnessUsage returned usage, want nil")
			}
		})
	}
}

// TestAccumulateHarnessUsage_NilCollectorIsSafe covers the interactive path,
// which passes no collector at all.
func TestAccumulateHarnessUsage_NilCollectorIsSafe(t *testing.T) {
	reader := &fakeUsageReader{usage: &transcript.Usage{InputTokens: 10}}
	installUsageReader(t, reader)

	if accumulateHarnessUsage(nil, "claude", "sess-uuid", "/work/falcon") {
		t.Error("accumulateHarnessUsage = true with a nil collector, want false")
	}
	if reader.calls != 0 {
		t.Errorf("ReadUsage called %d times with a nil collector, want 0", reader.calls)
	}
}

// TestAccumulateHarnessUsage_DedupsBySessionID guards the shape trap: ReadUsage
// returns a WHOLE-SESSION CUMULATIVE total while Collector.Accumulate is
// additive, so re-reading the same session (a retried or resumed turn) must not
// add the running total a second time. A genuinely different harness session
// still counts separately — those are two billed sessions.
func TestAccumulateHarnessUsage_DedupsBySessionID(t *testing.T) {
	installUsageReader(t, &fakeUsageReader{usage: &transcript.Usage{
		InputTokens: 1000, OutputTokens: 100, CacheReadInputTokens: 50, CacheCreationInputTokens: 25,
	}})

	c := usage.NewCollector("claude", "falcon")
	accumulateHarnessUsage(c, "claude", "sess-A", "/work/falcon")
	accumulateHarnessUsage(c, "claude", "sess-A", "/work/falcon")
	accumulateHarnessUsage(c, "claude", "sess-A", "/work/falcon")

	if in, out, cr, cw := c.Totals(); in != 1000 || out != 100 || cr != 50 || cw != 25 {
		t.Fatalf("after 3 reads of one session totals = (%d, %d, %d, %d), want (1000, 100, 50, 25)", in, out, cr, cw)
	}

	accumulateHarnessUsage(c, "claude", "sess-B", "/work/falcon")
	if in, _, _, _ := c.Totals(); in != 2000 {
		t.Errorf("after a second distinct session input = %d, want 2000", in)
	}
}

// TestHarnessUsageDedupKey_NamespacedAwayFromMessageIDs proves the dedup key
// cannot collide with a Claude message id, which shares the collector's
// seen-set via collectClaudeStreamUsage.
func TestHarnessUsageDedupKey_NamespacedAwayFromMessageIDs(t *testing.T) {
	key := harnessUsageDedupKey("claude", "msg_012345")
	if key == "msg_012345" {
		t.Fatal("dedup key equals the raw id; a message id could silently suppress the transcript read")
	}
	if key != harnessUsageDedupKey("claude", "  msg_012345  ") {
		t.Error("dedup key is not stable under surrounding whitespace")
	}
	if key == harnessUsageDedupKey("codex", "msg_012345") {
		t.Error("dedup key is not namespaced per backend")
	}
}

// TestDefaultHarnessUsageReaderFor_HonorsConfigOverrides is the regression guard
// for the override that once cost us an infinite demote/reopen loop: the
// library readers default to ~/.claude and ~/.codex, and loom must pin their
// roots from CLAUDE_CONFIG_DIR / CODEX_HOME instead.
func TestDefaultHarnessUsageReaderFor_HonorsConfigOverrides(t *testing.T) {
	claudeCfg := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeCfg)
	t.Setenv("CODEX_HOME", codexHome)

	claudeReader, ok := defaultHarnessUsageReaderFor("claude").(*claudecode.Reader)
	if !ok {
		t.Fatal("claude reader is not a *claudecode.Reader")
	}
	if want := filepath.Join(claudeCfg, "projects"); claudeReader.ProjectsRoot != want {
		t.Errorf("ProjectsRoot = %q, want %q", claudeReader.ProjectsRoot, want)
	}

	if got, want := codexSessionsRoot(), filepath.Join(codexHome, "sessions"); got != want {
		t.Errorf("codexSessionsRoot() = %q, want %q", got, want)
	}

	if r := defaultHarnessUsageReaderFor("opencode"); r != nil {
		t.Errorf("reader for a backend with no readable transcript = %v, want nil", r)
	}
}

// TestReadHarnessUsage_ReadsClaudeTranscriptFromDisk runs the whole path for
// real — no fake reader — against a Claude Code transcript staged under a
// CLAUDE_CONFIG_DIR override, which is the exact layout the daemon produces.
func TestReadHarnessUsage_ReadsClaudeTranscriptFromDisk(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	workDir := "/tmp/loom-usage-fixture/falcon"
	sessionID := "11111111-2222-3333-4444-555555555555"
	writeClaudeTranscript(t, configDir, workDir, sessionID, claudeTranscriptFixture)

	u := ReadHarnessUsage("claude", sessionID, workDir)
	if u == nil {
		t.Fatal("ReadHarnessUsage = nil, want the staged transcript's totals")
	}
	// Both lines of msg_1 repeat one API call's usage; msg_2 is a second call.
	want := transcript.Usage{
		InputTokens: 110, OutputTokens: 22,
		CacheReadInputTokens: 5, CacheCreationInputTokens: 10,
	}
	if *u != want {
		t.Errorf("usage = %+v, want %+v", *u, want)
	}

	if ReadHarnessUsage("claude", "99999999-9999-9999-9999-999999999999", workDir) != nil {
		t.Error("an unknown session id must read as nil, not as an error or a partial total")
	}
}

// claudeTranscriptFixture is a two-API-call Claude Code transcript. The first
// call is split across two content-block lines that repeat its usage, which is
// what the reader's dedup-by-message-id exists for.
const claudeTranscriptFixture = `{"type":"assistant","uuid":"u1","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}}}
{"type":"assistant","uuid":"u2","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","name":"Bash"}],"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}}}
{"type":"assistant","uuid":"u3","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":7}}}
`

// writeClaudeTranscript stages body at
// <configDir>/projects/<encoded-workdir>/<sessionID>.jsonl, the path Claude Code
// itself writes to.
func writeClaudeTranscript(t *testing.T, configDir, workDir, sessionID, body string) {
	t.Helper()
	dir := filepath.Join(configDir, "projects", claudecode.EncodedCWD(workDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// TestInvokeClaudeRunTurn_FeedsCollectorFromTranscript is the end-to-end proof
// for the defect itself: the turn-lifecycle path used to end in `_ = collector`,
// so every Claude run booked zero. It must now read the session's transcript
// usage, keyed by the session id RunTurn reported, and land it on the collector.
func TestInvokeClaudeRunTurn_FeedsCollectorFromTranscript(t *testing.T) {
	reader := &fakeUsageReader{usage: &transcript.Usage{
		InputTokens: 4200, OutputTokens: 310, CacheReadInputTokens: 77, CacheCreationInputTokens: 12,
	}}
	installUsageReader(t, reader)
	installClaudeRunTurnMock(t, func(_ context.Context, _ claudeRunTurnConfig) (claudeRunTurnResult, error) {
		res := completedClaudeTurn("all done")
		res.Session.HarnessSessionID = "claude-sess-1"
		return res, nil
	})

	c := usage.NewCollector("claude", "falcon")
	res, err := invokeClaudeRunTurn(context.Background(), "/work/falcon", "do it", "falcon", "", nil, c)
	if err != nil {
		t.Fatalf("invokeClaudeRunTurn: %v", err)
	}
	if got := claudeTurnText(res); got != "all done" {
		t.Errorf("turn text = %q, want %q", got, "all done")
	}
	if reader.gotSID != "claude-sess-1" || reader.gotWorkDir != "/work/falcon" {
		t.Errorf("ReadUsage(%q, %q), want (\"claude-sess-1\", \"/work/falcon\")", reader.gotSID, reader.gotWorkDir)
	}
	if in, out, cr, cw := c.Totals(); in != 4200 || out != 310 || cr != 77 || cw != 12 {
		t.Errorf("totals = (%d, %d, %d, %d), want (4200, 310, 77, 12)", in, out, cr, cw)
	}
}

// TestInvokeClaudeRunTurn_UsageMissLeavesTheTurnIntact is the other half of the
// contract: telemetry is optional, so an unreadable transcript — or a turn that
// never reported a session id — must leave the reply and the error exactly as
// the harness produced them.
func TestInvokeClaudeRunTurn_UsageMissLeavesTheTurnIntact(t *testing.T) {
	tests := []struct {
		name      string
		reader    transcript.UsageReader
		sessionID string
	}{
		{name: "transcript unreadable", reader: &fakeUsageReader{err: errors.New("boom")}, sessionID: "claude-sess-1"},
		{name: "turn reported no session id", reader: &fakeUsageReader{usage: &transcript.Usage{InputTokens: 5}}, sessionID: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			installUsageReader(t, tc.reader)
			installClaudeRunTurnMock(t, func(_ context.Context, _ claudeRunTurnConfig) (claudeRunTurnResult, error) {
				res := completedClaudeTurn("still a good reply")
				res.Session.HarnessSessionID = tc.sessionID
				return res, nil
			})

			c := usage.NewCollector("claude", "falcon")
			res, err := invokeClaudeRunTurn(context.Background(), "/work/falcon", "do it", "falcon", "", nil, c)
			if err != nil {
				t.Fatalf("a usage miss changed the error: %v", err)
			}
			if got := claudeTurnText(res); got != "still a good reply" {
				t.Errorf("a usage miss changed the reply: %q", got)
			}
			if in, out, cr, cw := c.Totals(); in != 0 || out != 0 || cr != 0 || cw != 0 {
				t.Errorf("totals = (%d, %d, %d, %d), want all zero", in, out, cr, cw)
			}
		})
	}
}
