package backends

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// writeDeadlineLock puts a lock this process owns in dir, so
// UpdateLockClaudeSessionID's owner check passes.
func writeDeadlineLock(t *testing.T, dir string) {
	t.Helper()
	data, err := json.MarshalIndent(cli.LockInfo{
		PID: os.Getpid(), Command: "task", AgentName: "tester",
		TaskID: "loomcli-7", TaskStartedAt: time.Now(),
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, cli.LockFileName), data, 0600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

func lockSessionID(t *testing.T, dir string) string {
	t.Helper()
	info, err := cli.ReadLockFile(dir)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	return info.ClaudeSessionID
}

// A turn that ends on the per-turn DEADLINE must still leave its session id on
// the lock. This is the defect the ticket's instrumentation found: the RunTurn
// path persisted the id only after the success arm, so the one exit whose work
// is worth resuming was the one exit that recorded nothing, and the next
// attempt cold-started and redid the task.
func TestDeadlineExitPersistsSessionIDOntoLock(t *testing.T) {
	dir := t.TempDir()
	writeDeadlineLock(t, dir)
	ClearResumeSessionID()
	t.Cleanup(ClearResumeSessionID)

	installClaudeRunTurnMock(t, func(ctx context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		res := claudeRunTurnResult{}
		res.Session.HarnessSessionID = "sess-deadline"
		return res, context.DeadlineExceeded
	})

	if err := (&ClaudeBackend{}).InvokeNonInteractive(dir, "prompt", "tester", nil, nil); err == nil {
		t.Fatal("a deadline exit must surface as an error")
	}
	if got := lockSessionID(t, dir); got != "sess-deadline" {
		t.Fatalf("lock claude_session_id after a deadline exit = %q, want %q", got, "sess-deadline")
	}
}

// The harness reveals Claude's session id on its GRACEFUL quit, which a
// cancelled turn never reaches — so on the deadline path the result usually
// carries no id at all. A run that was itself RESUMING one keeps that id: the
// resumed session is the same session, and dropping it here is what made a
// second ceiling hit unresumable even after the first was carried forward.
func TestDeadlineExitKeepsTheResumedSessionID(t *testing.T) {
	dir := t.TempDir()
	writeDeadlineLock(t, dir)
	SetResumeSessionID("sess-carried")
	t.Cleanup(ClearResumeSessionID)

	installClaudeRunTurnMock(t, func(ctx context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		return claudeRunTurnResult{}, context.DeadlineExceeded // no session id on the result
	})

	if err := (&ClaudeBackend{}).InvokeNonInteractive(dir, "prompt", "tester", nil, nil); err == nil {
		t.Fatal("a deadline exit must surface as an error")
	}
	if got := lockSessionID(t, dir); got != "sess-carried" {
		t.Fatalf("lock claude_session_id after a deadline exit = %q, want the resumed id %q", got, "sess-carried")
	}
}

// A completed turn still persists its id — the behavior the deadline fix must
// not regress.
func TestCompletedTurnPersistsSessionIDOntoLock(t *testing.T) {
	dir := t.TempDir()
	writeDeadlineLock(t, dir)
	ClearResumeSessionID()
	t.Cleanup(ClearResumeSessionID)

	installClaudeRunTurnMock(t, func(ctx context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		res := completedClaudeTurn("done")
		res.Session.HarnessSessionID = "sess-complete"
		return res, nil
	})

	if err := (&ClaudeBackend{}).InvokeNonInteractive(dir, "prompt", "tester", nil, nil); err != nil {
		t.Fatalf("completed turn: %v", err)
	}
	if got := lockSessionID(t, dir); got != "sess-complete" {
		t.Fatalf("lock claude_session_id after a completed turn = %q, want %q", got, "sess-complete")
	}
}
