package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRunBundledTaskRunner_LiveBackend exercises the bundled TS leaf against a
// REAL backend CLI on PATH (claude / codex / cursor / opencode / gemini), proving the
// Phase-U execution path — backend argv + stream-json -> canonical transcript_entries +
// top-level usage — works per backend, not just for codex.
//
// Gated + skipped by default (real CLIs need auth + network + cost). Run one backend:
//
//	LOOM_LIVE_BACKEND=claude go test ./internal/driver/ -run LiveBackend -v -count=1 -timeout 600s
//
// The daemon->leaf wiring (tsRuntimeInvoker) is backend-agnostic and already verified
// on the codex podman stack; this isolates the remaining per-backend risk in the runner.
func TestRunBundledTaskRunner_LiveBackend(t *testing.T) {
	backend := os.Getenv("LOOM_LIVE_BACKEND")
	if backend == "" {
		t.Skip("set LOOM_LIVE_BACKEND={claude|codex|cursor|opencode|gemini} to run this live test")
	}
	// Map the loom backend name to the CLI binary the runner will look for.
	bin := map[string]string{
		"claude":   "claude",
		"codex":    "codex",
		"cursor":   "cursor-agent",
		"opencode": "opencode",
		"gemini":   "gemini",
	}[backend]
	if bin == "" {
		t.Fatalf("unknown LOOM_LIVE_BACKEND=%q", backend)
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s CLI (%s) not on PATH: %v", backend, bin, err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	serverPath := committedBundleServerPath(t)

	tmp := t.TempDir()
	worktree := filepath.Join(tmp, "wt")
	newGitWorktree(t, worktree)

	// Trivial, cheap, file-touching-free prompt so the run completes fast and the
	// assertion focuses on transcript + usage capture, not delivery.
	prompt := "Reply with exactly this text on one line: HELLO_FROM_" + backend +
		". Do not create, modify, run, or delete any files or commands. Then stop."

	reqJSON, _ := json.Marshal(map[string]any{
		"task_run_id":   "tr-live-" + backend,
		"task_id":       "T-LIVE",
		"backend":       backend,
		"workspace_key": "ws",
		"lease_token":   "",
		"input":         map[string]any{"title": "say hello"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()

	var serr bytes.Buffer
	raw, err := RunBundledTaskRunner(ctx, BundledRunnerOptions{
		ServerPath:   serverPath,
		Worktree:     worktree,
		Backend:      backend,
		Prompt:       prompt,
		RequestJSON:  string(reqJSON),
		LeaseToken:   "",
		StreamStderr: true,
		Stderr:       &serr,
	})
	if err != nil {
		t.Fatalf("RunBundledTaskRunner(%s): %v\n--- stderr (tail) ---\n%s", backend, err, tailStr(serr.String(), 4000))
	}

	var result bundledResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v\nraw: %s", err, raw)
	}

	t.Logf("[%s] status=%q entries=%d usage in=%d out=%d cacheRead=%d cacheWrite=%d cost=$%.4f",
		backend, result.Status, len(result.TranscriptEntries), result.InputTokens, result.OutputTokens,
		result.CacheReadTokens, result.CacheWriteTokens, result.EstimatedCostUSD)

	if result.Status != "completed" {
		t.Fatalf("status = %q (err=%q); raw: %s", result.Status, result.ErrorMessage, tailStr(string(raw), 2000))
	}
	if len(result.TranscriptEntries) == 0 {
		t.Errorf("no transcript_entries captured for %s: %s", backend, tailStr(string(raw), 2000))
	}
	// Usage is the whole point of Phase 0/U telemetry parity — a real model turn must
	// report non-zero input + output tokens.
	if result.InputTokens <= 0 || result.OutputTokens <= 0 {
		t.Errorf("%s usage not captured: in=%d out=%d (raw tail: %s)", backend, result.InputTokens, result.OutputTokens, tailStr(string(raw), 1500))
	}
}
