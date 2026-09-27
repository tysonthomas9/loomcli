package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// TestForcedTurnTimeoutResumeGate drives the REAL Claude backend to a REAL
// per-turn deadline (LOOM_RUN_TURN_TIMEOUT_SECONDS) and then asks
// maybeResumeDaemonSession what the next attempt would do. It is the
// instrument for "which early return actually fires on a ceiling hit" — a
// question the unit tests cannot answer, because the interesting half is
// whether the harness ever gets far enough to hand loom a session id.
//
// Opt-in: it spends real Claude tokens and needs an authenticated `claude` on
// PATH, so it is skipped unless LOOM_FORCED_TIMEOUT_GATE=1.
func TestForcedTurnTimeoutResumeGate(t *testing.T) {
	if os.Getenv("LOOM_FORCED_TIMEOUT_GATE") != "1" {
		t.Skip("set LOOM_FORCED_TIMEOUT_GATE=1 to run the real forced-timeout gate")
	}
	seconds := os.Getenv("LOOM_RUN_TURN_TIMEOUT_SECONDS")
	if seconds == "" {
		t.Fatal("set LOOM_RUN_TURN_TIMEOUT_SECONDS to the forced deadline")
	}

	// An UNTRUSTED directory raises Claude's folder-trust dialog, and the run
	// then ends on the dialog instead of the deadline — measuring the wrong
	// thing. The caller names a directory Claude already trusts.
	workDir := os.Getenv("LOOM_FORCED_TIMEOUT_GATE_DIR")
	if workDir == "" {
		t.Fatal("set LOOM_FORCED_TIMEOUT_GATE_DIR to a directory Claude Code already trusts")
	}
	t.Setenv("LOOM_ROLE_INPUT_POLICY", `{"default":"allow"}`)
	if dir := os.Getenv("LOOM_FORCED_TIMEOUT_GATE_CLAUDE_CONFIG_DIR"); dir != "" {
		t.Setenv("CLAUDE_CONFIG_DIR", dir)
	}

	const taskID = "GATE-1"
	if err := cli.AcquireLock(workDir, "task", "gate-agent"); err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	if err := cli.UpdateLockTask(workDir, taskID, "forced timeout gate"); err != nil {
		t.Fatalf("update lock task: %v", err)
	}

	prompt := "Count out loud from 1 to 400. Print each number on its own line, " +
		"with a one-sentence remark about it. Do not stop early."
	err := (&backends.ClaudeBackend{}).InvokeNonInteractive(workDir, prompt, "gate-agent", nil, nil)
	t.Logf("GATE invoke error: %v", err)

	raw, readErr := os.ReadFile(filepath.Join(workDir, cli.LockFileName))
	t.Logf("GATE lock read err: %v", readErr)
	t.Logf("GATE lock after deadline: %s", raw)
	var info cli.LockInfo
	_ = json.Unmarshal(raw, &info)
	t.Logf("GATE claude_session_id after deadline: %q", info.ClaudeSessionID)

	// Whatever it prints is the answer this ticket was opened to get.
	maybeResumeDaemonSession(workDir, taskID)
}
