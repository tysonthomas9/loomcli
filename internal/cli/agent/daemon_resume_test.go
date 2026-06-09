package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

func writeLockInfo(t *testing.T, dir string, info cli.LockInfo) {
	t.Helper()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, cli.LockFileName), data, 0600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

func TestMaybeResumeDaemonSession_Guards(t *testing.T) {
	cases := []struct {
		name       string
		assigned   string // LOOM_ASSIGNED_TASK_ID
		lock       cli.LockInfo
		wantResume string // expected armed session id ("" = not armed)
	}{
		{
			name:       "same task, fresh, session present → resume armed",
			assigned:   "loomcli-7",
			lock:       cli.LockInfo{ClaudeSessionID: "sess-1", TaskID: "loomcli-7", TaskStartedAt: time.Now()},
			wantResume: "sess-1",
		},
		{
			name:       "different assigned task → cold start",
			assigned:   "loomcli-OTHER",
			lock:       cli.LockInfo{ClaudeSessionID: "sess-1", TaskID: "loomcli-7", TaskStartedAt: time.Now()},
			wantResume: "",
		},
		{
			name:       "no assigned task → cold start",
			assigned:   "",
			lock:       cli.LockInfo{ClaudeSessionID: "sess-1", TaskID: "loomcli-7", TaskStartedAt: time.Now()},
			wantResume: "",
		},
		{
			name:       "stale session beyond TTL → cold start",
			assigned:   "loomcli-7",
			lock:       cli.LockInfo{ClaudeSessionID: "sess-1", TaskID: "loomcli-7", TaskStartedAt: time.Now().Add(-90 * time.Minute)},
			wantResume: "",
		},
		{
			name:       "no carried session id → cold start",
			assigned:   "loomcli-7",
			lock:       cli.LockInfo{TaskID: "loomcli-7", TaskStartedAt: time.Now()},
			wantResume: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backends.ClearResumeSessionID()
			t.Cleanup(backends.ClearResumeSessionID)
			t.Setenv("LOOM_ASSIGNED_TASK_ID", tc.assigned)
			dir := t.TempDir()
			writeLockInfo(t, dir, tc.lock)

			maybeResumeDaemonSession(dir, tc.assigned)

			if got := backends.GetResumeSessionID(); got != tc.wantResume {
				t.Fatalf("armed resume = %q, want %q", got, tc.wantResume)
			}
		})
	}
}

func TestResumeTTL_EnvOverride(t *testing.T) {
	t.Setenv("LOOM_RESUME_TTL", "45m")
	if got := ResumeTTL(); got != 45*time.Minute {
		t.Fatalf("ResumeTTL = %v, want 45m", got)
	}
	t.Setenv("LOOM_RESUME_TTL", "garbage")
	if got := ResumeTTL(); got != defaultResumeTTL {
		t.Fatalf("ResumeTTL with bad value = %v, want default %v", got, defaultResumeTTL)
	}
}

// TestPersistAssignedTaskToLock: the daemon records the assigned task on the
// WORKTREE lock (so detectRecovery can trigger resume after a crash), skipping
// the write — and thus preserving the TaskStartedAt resume-TTL clock — when the
// lock already carries that task (a resume/checkpoint cycle).
func TestPersistAssignedTaskToLock(t *testing.T) {
	dir := t.TempDir()
	writeLockInfo(t, dir, cli.LockInfo{PID: os.Getpid(), Command: "plan", AgentName: "tester"})

	// empty id → no-op
	persistAssignedTaskToLock(dir, "")
	if info, _ := cli.ReadLockFile(dir); info == nil || info.TaskID != "" {
		t.Fatalf("empty id should not set TaskID, got %+v", info)
	}

	// new task → recorded on the worktree lock (the trigger detectRecovery reads)
	persistAssignedTaskToLock(dir, "loomcli-7")
	info, err := cli.ReadLockFile(dir)
	if err != nil || info == nil || info.TaskID != "loomcli-7" {
		t.Fatalf("after persist: TaskID=%v err=%v", info, err)
	}
	started := info.TaskStartedAt

	// same task again → skipped, so TaskStartedAt (resume-TTL clock) is preserved
	time.Sleep(10 * time.Millisecond)
	persistAssignedTaskToLock(dir, "loomcli-7")
	info2, _ := cli.ReadLockFile(dir)
	if info2 == nil || !info2.TaskStartedAt.Equal(started) {
		t.Errorf("same-task re-persist reset TaskStartedAt: %v → %v", started, info2.TaskStartedAt)
	}
}
