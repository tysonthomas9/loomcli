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
