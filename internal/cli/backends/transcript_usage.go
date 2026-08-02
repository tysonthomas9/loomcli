package backends

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/transcript"
	"github.com/olesho/harness-wrapper/pkg/transcript/claudecode"
	"github.com/olesho/harness-wrapper/pkg/transcript/codex"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// Token accounting read back from the harness's OWN session log.
//
// The turn-lifecycle API (hwharness.RunTurn) drives Claude Code's interactive
// TUI, which never emits the stream-json usage records the per-line scrapers
// (collectClaudeStreamUsage) were written for — so on that path loom used to
// throw the collector away and every Claude run recorded 0 tokens / $0. The
// harness-wrapper transcript package closes that hole: each per-harness Reader
// also implements transcript.UsageReader, reading the same JSONL the harness
// writes for itself.
//
// Two properties of that API shape everything below:
//
//  1. ReadUsage returns the WHOLE-SESSION CUMULATIVE total, not a per-turn
//     delta, while usage.Collector.Accumulate is additive. Folding a cumulative
//     read into an additive collector is only safe with a stable dedup key —
//     see accumulateHarnessUsage.
//  2. Usage is strictly optional. A ReadUsage error, an empty session id, or a
//     (nil, nil) return must never erase a good reply or change an exit code.
//     Every entry point here returns a zero value instead of an error so a
//     caller physically cannot fail a turn over missing telemetry.

// harnessUsageReaderFor resolves the transcript reader that can answer
// ReadUsage for a backend, or nil when the backend keeps no readable session
// log (echo, external, cursor, ...). It is a package var so tests can inject a
// fake reader without staging JSONL on disk.
var harnessUsageReaderFor = defaultHarnessUsageReaderFor

func defaultHarnessUsageReaderFor(backend string) transcript.UsageReader {
	switch backend {
	case backendnames.Claude:
		// claudecode.Reader defaults ProjectsRoot to ~/.claude/projects, which
		// silently ignores CLAUDE_CONFIG_DIR. That override is load-bearing, not
		// cosmetic: the container and smoke-test paths set it, and a lookup that
		// resolves only from $HOME finds nothing there. sessions.ClaudeConfigDir
		// exists precisely because that mismatch once cost us an infinite
		// demote/reopen loop — pin ProjectsRoot from it rather than letting the
		// library default win.
		root := sessions.ClaudeConfigDir()
		if root == "" {
			return nil
		}
		return &claudecode.Reader{ProjectsRoot: filepath.Join(root, "projects")}
	case backendnames.Codex:
		// Same story one harness over: codex.Reader defaults SessionsRoot to
		// ~/.codex/sessions and ignores CODEX_HOME, which claudeAuthFilePath's
		// sibling (codexAuthFilePath) already honors.
		root := codexSessionsRoot()
		if root == "" {
			return nil
		}
		return &codex.Reader{SessionsRoot: root}
	default:
		return nil
	}
}

// codexSessionsRoot returns <CODEX_HOME|~/.codex>/sessions, or "" when neither
// can be resolved. Mirrors codexAuthFilePath's handling of the override so the
// auth check and the transcript lookup cannot disagree about where Codex lives.
func codexSessionsRoot() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// ReadHarnessUsage asks a backend's own transcript for the cumulative token
// totals of one session. It returns nil — never an error — when the backend has
// no reader, the session id is empty, the read fails, or the transcript carried
// no usage at all. Callers treat a nil result as "no telemetry this run", which
// is the pre-existing behavior, so nothing regresses when a transcript is
// missing.
//
// The returned counts keep each harness's native semantics; see
// SessionTokensFromHarnessUsage for why they must not be normalized.
func ReadHarnessUsage(backend, harnessSessionID, workDir string) *transcript.Usage {
	sid := strings.TrimSpace(harnessSessionID)
	if sid == "" {
		return nil
	}
	reader := harnessUsageReaderFor(backend)
	if reader == nil {
		return nil
	}
	u, err := reader.ReadUsage(sid, workDir)
	if err != nil || u == nil {
		return nil
	}
	return u
}

// SessionTokensFromHarnessUsage projects a harness-native usage record onto
// loom's four token counters.
//
// The mapping is deliberately verbatim, because input_tokens does NOT mean the
// same thing on both harnesses and normalizing it would corrupt the numbers in
// opposite directions:
//
//   - Claude's input_tokens EXCLUDES cache reads and cache creations; those live
//     in the separate cache_* fields (Anthropic API semantics). Its cache
//     figures therefore ADD to input.
//   - Codex's input_tokens already INCLUDES cached_input_tokens as a SUBSET, and
//     Codex has no cache-creation concept at all. Re-adding the cached count
//     would double-charge it.
//
// So cacheRead is reported honestly for both, and cost estimation must not
// re-add it on Codex — usage.DefaultPricing leaves Codex's CacheReadPerMTok at
// zero, which is what keeps EstimateCost from double-charging the subset.
//
// transcript.Usage.ReasoningOutputTokens is dropped: loom has no counter for it,
// and on Codex it is already part of output_tokens.
func SessionTokensFromHarnessUsage(u *transcript.Usage) (input, output, cacheRead, cacheWrite int64) {
	if u == nil {
		return 0, 0, 0, 0
	}
	return int64(u.InputTokens), int64(u.OutputTokens),
		int64(u.CacheReadInputTokens), int64(u.CacheCreationInputTokens)
}

// accumulateHarnessUsage folds a session's cumulative transcript usage into a
// live collector and reports whether anything landed.
//
// The dedup key is the harness session id, which makes the fold idempotent: a
// retried or resumed turn re-reads the same growing transcript, and without a
// stable key each read would add the full running total again. The tradeoff is
// deliberate and one-directional — a retry that resumes the SAME harness
// session keeps the first read's total and under-counts the extra spend, rather
// than reporting a multiple of the real cost. A retry that cold-starts a new
// harness session gets a new key and is counted separately, which is correct:
// those really are two billed sessions.
func accumulateHarnessUsage(collector *usage.Collector, backend, harnessSessionID, workDir string) bool {
	if collector == nil {
		return false
	}
	u := ReadHarnessUsage(backend, harnessSessionID, workDir)
	if u == nil {
		return false
	}
	input, output, cacheRead, cacheWrite := SessionTokensFromHarnessUsage(u)
	collector.Accumulate(harnessUsageDedupKey(backend, harnessSessionID), input, output, cacheRead, cacheWrite)
	return true
}

// harnessUsageDedupKey namespaces the harness session id so it can never
// collide with a Claude message id from the stream-json scraper, which shares
// the collector's seen-set.
func harnessUsageDedupKey(backend, harnessSessionID string) string {
	return "harness-session:" + backend + ":" + strings.TrimSpace(harnessSessionID)
}
