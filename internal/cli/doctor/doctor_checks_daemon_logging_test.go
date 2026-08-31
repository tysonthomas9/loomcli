package doctor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fileInfoAt returns an os.FileInfo for a real temp file whose mtime has been
// backdated to mtime. Real files keep the test honest about what os.Stat
// actually hands the evaluator.
func fileInfoAt(t *testing.T, name string, mtime time.Time) os.FileInfo {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", name, err)
	}
	return info
}

func TestEvaluateDaemonLogging(t *testing.T) {
	now := time.Now()
	const logPath = "/proj/.loom/logs/ws/daemon.log"

	tests := []struct {
		name         string
		logAge       time.Duration
		noLog        bool
		statErr      error
		stateAge     time.Duration // negative → no state file at all
		wantStatus   CheckStatus
		wantSummary  string
		wantDetails  []string
		absentDetail string
	}{
		{
			name:        "fresh log passes",
			logAge:      10 * time.Second,
			stateAge:    2 * time.Second,
			wantStatus:  StatusPass,
			wantSummary: "daemon log is current",
		},
		{
			name:        "three minutes warns",
			logAge:      3 * time.Minute,
			stateAge:    -1,
			wantStatus:  StatusWarn,
			wantSummary: "has not been written for",
			wantDetails: []string{logPath, "check free disk space", "kill -TERM <pid> && loom daemon"},
		},
		{
			name:        "ten minutes fails",
			logAge:      10 * time.Minute,
			stateAge:    -1,
			wantStatus:  StatusFail,
			wantSummary: "daemon alive but daemon.log has not been written for",
			wantDetails: []string{logPath, "check free disk space"},
			// No state file → no discriminator to offer.
			absentDetail: "log path specifically is broken",
		},
		{
			name:        "missing log warns",
			noLog:       true,
			statErr:     fs.ErrNotExist,
			stateAge:    2 * time.Second,
			wantStatus:  StatusWarn,
			wantSummary: "daemon running but has no self-owned log file",
			wantDetails: []string{logPath, "predate"},
		},
		{
			name:        "unreadable log warns",
			noLog:       true,
			statErr:     errors.New("permission denied"),
			stateAge:    2 * time.Second,
			wantStatus:  StatusWarn,
			wantSummary: "could not stat daemon log file",
			wantDetails: []string{logPath, "permission denied"},
		},
		{
			name:        "stale log with fresh state names the log path as the fault",
			logAge:      10 * time.Minute,
			stateAge:    3 * time.Second,
			wantStatus:  StatusFail,
			wantSummary: "daemon alive but daemon.log has not been written for",
			wantDetails: []string{
				"state file is current — the log path specifically is broken, not the daemon",
				"kill -TERM <pid> && loom daemon",
			},
		},
		{
			name:        "stale log and stale state omits the discriminator",
			logAge:      10 * time.Minute,
			stateAge:    5 * time.Minute,
			wantStatus:  StatusFail,
			wantSummary: "daemon alive but daemon.log has not been written for",
			// The state updater is wedged too — checkDaemonStuck owns that story.
			absentDetail: "log path specifically is broken",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logInfo os.FileInfo
			if !tc.noLog {
				logInfo = fileInfoAt(t, "daemon.log", now.Add(-tc.logAge))
			}
			var stateInfo os.FileInfo
			if tc.stateAge >= 0 {
				stateInfo = fileInfoAt(t, "daemon-agents.json", now.Add(-tc.stateAge))
			}

			result := evaluateDaemonLogging(logPath, logInfo, stateInfo, tc.statErr, now)

			if result.Name != "daemon_logging" {
				t.Errorf("unexpected check name: %q", result.Name)
			}
			if result.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s (%s / %s)",
					result.Status, tc.wantStatus, result.Summary, result.Detail)
			}
			if !strings.Contains(result.Summary, tc.wantSummary) {
				t.Errorf("summary = %q, want it to contain %q", result.Summary, tc.wantSummary)
			}
			for _, want := range tc.wantDetails {
				if !strings.Contains(result.Detail, want) {
					t.Errorf("detail missing %q, got:\n%s", want, result.Detail)
				}
			}
			if tc.absentDetail != "" && strings.Contains(result.Detail, tc.absentDetail) {
				t.Errorf("detail should not contain %q, got:\n%s", tc.absentDetail, result.Detail)
			}
		})
	}
}

// TestEvaluateDaemonLogging_ThresholdBoundaries pins the exact WARN/FAIL edges
// so a threshold change has to be deliberate.
func TestEvaluateDaemonLogging_ThresholdBoundaries(t *testing.T) {
	now := time.Now()

	cases := []struct {
		age  time.Duration
		want CheckStatus
	}{
		{daemonLogWarnAge - time.Second, StatusPass},
		{daemonLogWarnAge + time.Second, StatusWarn},
		{daemonLogFailAge - time.Second, StatusWarn},
		{daemonLogFailAge + time.Second, StatusFail},
	}

	for _, tc := range cases {
		t.Run(tc.age.String(), func(t *testing.T) {
			logInfo := fileInfoAt(t, "daemon.log", now.Add(-tc.age))
			result := evaluateDaemonLogging("/tmp/daemon.log", logInfo, nil, nil, now)
			if result.Status != tc.want {
				t.Fatalf("age %s: status = %s, want %s", tc.age, result.Status, tc.want)
			}
		})
	}
}

func TestCheckDaemonLogging_DeadDaemonReturnsEmpty(t *testing.T) {
	// Reuses the daemon_stuck fixture: same pid file, same cwd handling.
	writeDaemonStuckFixture(t, 2147483600, nil, time.Time{})

	result := checkDaemonLogging()
	if result.Name != "" {
		t.Fatalf("expected empty CheckResult when daemon not running, got %+v", result)
	}
}
