package daemon

import (
	"testing"
	"time"
)

func TestDecideDaemonRestart(t *testing.T) {
	const maxR = 10
	const healthy = 5 * time.Minute

	cases := []struct {
		name        string
		exitCode    int
		autoRestart bool
		prev        int
		uptime      time.Duration
		wantRestart bool
		wantAttempt int
		wantExit    int
	}{
		{"clean shutdown never restarts", 0, true, 0, time.Second, false, 0, 0},
		{"non-fatal code not retried", 1, true, 0, time.Second, false, 0, 1},
		{"auto-restart disabled", fatalSupervisorExitCode, false, 0, time.Second, false, 0, fatalSupervisorExitCode},
		{"fatal restarts on first attempt", fatalSupervisorExitCode, true, 0, time.Second, true, 1, 0},
		{"fatal increments attempt", fatalSupervisorExitCode, true, 3, time.Second, true, 4, 0},
		{"healthy uptime resets budget", fatalSupervisorExitCode, true, 7, healthy + time.Minute, true, 1, 0},
		{"budget exhausted stops", fatalSupervisorExitCode, true, maxR, time.Second, false, 0, fatalSupervisorExitCode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decideDaemonRestart(tc.exitCode, tc.autoRestart, tc.prev, tc.uptime, healthy, maxR)
			if d.Restart != tc.wantRestart {
				t.Fatalf("Restart=%v want %v", d.Restart, tc.wantRestart)
			}
			if tc.wantRestart {
				if d.Attempt != tc.wantAttempt {
					t.Errorf("Attempt=%d want %d", d.Attempt, tc.wantAttempt)
				}
			} else if d.ExitCode != tc.wantExit {
				t.Errorf("ExitCode=%d want %d", d.ExitCode, tc.wantExit)
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	t.Setenv("LOOM_TEST_ENVINT", "")
	if got := envInt("LOOM_TEST_ENVINT", 7); got != 7 {
		t.Errorf("unset/empty: got %d want 7", got)
	}
	t.Setenv("LOOM_TEST_ENVINT", "4")
	if got := envInt("LOOM_TEST_ENVINT", 7); got != 4 {
		t.Errorf("parse: got %d want 4", got)
	}
	t.Setenv("LOOM_TEST_ENVINT", "not-a-number")
	if got := envInt("LOOM_TEST_ENVINT", 7); got != 7 {
		t.Errorf("unparseable falls back: got %d want 7", got)
	}
}
