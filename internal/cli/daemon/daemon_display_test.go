package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

func TestPrintAgentStatusIncludesDiagnosticBranches(t *testing.T) {
	start := time.Now().Add(-2 * time.Hour)
	exit := start.Add(90 * time.Second)
	agent := DaemonAgentStatus{
		Worktree:              "nova",
		Role:                  "task",
		Status:                "stopped",
		EpicID:                "epic-1",
		TaskID:                "task-1",
		LastStart:             start,
		LastExit:              exit,
		LastExitCode:          7,
		LastErrorClass:        "RateLimited",
		NoWorkCount:           3,
		BackoffUntil:          time.Now().Add(time.Minute),
		RestartCount:          2,
		StopReason:            string(supervisor.StopReasonFatalError),
		OwnershipLeaseID:      "lease-1",
		OwnershipFencingToken: 12,
	}

	out := captureDaemonStdout(t, func() {
		printAgentStatus(agent)
	})
	for _, want := range []string{
		"nova (task)",
		"Epic: epic-1",
		"Task: task-1",
		"Ownership: fence 12",
		"Last run:",
		"Last error: RateLimited",
		"NoWork: 3",
		"Backoff:",
		"Restarts: 2",
		"Stopped: fatal_error (exit 7)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatDaemonDurationBranches(t *testing.T) {
	tests := map[time.Duration]string{
		0:                             "<1s",
		500 * time.Millisecond:        "<1s",
		42 * time.Second:              "42s",
		2*time.Minute + 5*time.Second: "2m 5s",
		3*time.Hour + 7*time.Minute + time.Second: "3h 7m",
	}
	for d, want := range tests {
		if got := formatDaemonDuration(d); got != want {
			t.Fatalf("formatDaemonDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
