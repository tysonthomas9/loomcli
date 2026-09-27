package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// authTail is a log tail ClassifyFromLogAt reads as ErrAuth. It stands in for
// the credentials banner an earlier turn had already recovered from — still
// sitting in the log when the supervisor kills the run for its own reasons.
const authTail = "resuming session...\nAPI Error: 401 unauthorized — invalid API key\n"

func newStoppedAgent(t *testing.T, reason StopReason, tail string) *AgentProcess {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(logPath, []byte(tail), 0o600); err != nil {
		t.Fatal(err)
	}
	return &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Backend: "claude"},
		WorktreePath: t.TempDir(),
		LogFilePath:  logPath,
		StopCh:       make(chan struct{}),
		StopReason:   reason,
	}
}

func classOf(ap *AgentProcess) agenterr.Outcome {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.LastError == nil {
		return agenterr.Outcome{}
	}
	return ap.LastError.Class
}

// The PUPPET-179 regression, at the classification level: a healthy agent
// SIGTERMed by the daemon's own shutdown was filed AuthFailure — a class the
// policy treats as StopFatal and human-actionable — because the classifier
// inferred it from a log tail the kill had nothing to do with.
func TestClassifyAgentExit_SupervisorStopIsNotAnAgentFault(t *testing.T) {
	cases := []struct {
		name    string
		reason  StopReason
		arrange func(*Supervisor, *AgentProcess)
	}{
		{"daemon shutdown", StopReasonShutdown, func(*Supervisor, *AgentProcess) {}},
		{"operator stop", StopReasonManualStop, func(*Supervisor, *AgentProcess) {}},
		{"agent removed from config", StopReasonConfigRemoved, func(*Supervisor, *AgentProcess) {}},
		{
			// The reason is recorded on the way OUT of the supervise loop,
			// which is after classification runs, so the shutdown drain
			// reaches the classifier with no reason set. The channel is.
			"shutdown signaled, reason not yet recorded", "",
			func(s *Supervisor, _ *AgentProcess) { close(s.Shutdown) },
		},
		{
			"per-agent stop signaled, reason not yet recorded", "",
			func(_ *Supervisor, ap *AgentProcess) { close(ap.StopCh) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newClassifySupervisor("claude")
			ap := newStoppedAgent(t, tc.reason, authTail)
			tc.arrange(s, ap)

			s.classifyAgentExit(ap, 143)

			if got := classOf(ap); !got.Is(agenterr.SupervisorStopOutcome) {
				t.Fatalf("class = %v, want SupervisorStop", got)
			}
			if classOf(ap).IsClass(wrapper.ErrAuth) {
				t.Fatal("a supervisor-initiated kill was filed as an auth failure")
			}
			ap.Mu.Lock()
			noWork := ap.LastNoWork
			ap.Mu.Unlock()
			if noWork {
				t.Error("a supervisor-initiated stop was recorded as no-work")
			}
		})
	}
}

// The guard on the arm above: only OUR kills bypass log classification. A
// silence-watchdog kill is a verdict ABOUT the agent, and an ordinary crash
// has nothing supervisor-initiated about it — both keep the log verdict.
func TestClassifyAgentExit_AgentFailureIsStillLogClassified(t *testing.T) {
	for _, reason := range []StopReason{"", StopReasonWatchdog, StopReasonRateLimited} {
		s := newClassifySupervisor("claude")
		ap := newStoppedAgent(t, reason, authTail)
		ap.AssignedTaskID = "PUPPET-1" // keep the watchdog case off the NoWork arm

		s.classifyAgentExit(ap, 1)

		if got := classOf(ap); got.Is(agenterr.SupervisorStopOutcome) {
			t.Errorf("stop reason %q was wrongly read as supervisor-initiated", reason)
		}
		if got := classOf(ap); !got.IsClass(wrapper.ErrAuth) {
			t.Errorf("stop reason %q: class = %v, want the log verdict ErrAuth", reason, got)
		}
	}
}
