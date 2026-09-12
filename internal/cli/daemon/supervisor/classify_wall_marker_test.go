package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// newMarkerAgent builds an agent whose log tail is `tail` and whose kill is
// attributed to `reason`.
func newMarkerAgent(t *testing.T, reason StopReason, tail string) *AgentProcess {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(logPath, []byte(tail), 0o600); err != nil {
		t.Fatal(err)
	}
	return &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "worker", Role: "task", Backend: "claude"},
		WorktreePath: t.TempDir(),
		LogFilePath:  logPath,
		StopReason:   reason,
	}
}

// The bug this closes: a leaf walled by billing outlives the run cap, is killed
// for LENGTH, and is filed as a timeout — a counted retry on a condition no
// retry can clear. The marker is a statement about the ACCOUNT, so it outranks
// the stop reason.
func TestClassifyAgentExit_BillingMarkerOutranksDurationKill(t *testing.T) {
	s := newClassifySupervisor("claude")
	ap := newMarkerAgent(t, StopReasonRunDurationExceeded,
		"running turn...\n"+agenterr.BillingWallMarker+": Your credit balance is too low.\n")

	s.classifyAgentExit(ap, 143)

	ap.Mu.Lock()
	lastErr, noWork := ap.LastError, ap.LastNoWork
	ap.Mu.Unlock()

	if lastErr == nil || lastErr.Class != agenterr.OutcomeFromHarness(wrapper.ErrBilling) {
		t.Fatalf("LastError = %#v, want ErrBilling rather than the duration-kill timeout", lastErr)
	}
	if noWork {
		t.Fatal("a marked exit is never the idle verdict")
	}
}

// Same override on the other arm: with no task claimed, a watchdog stop would
// otherwise read a walled agent as "nothing to do".
func TestClassifyAgentExit_BillingMarkerOutranksNoWork(t *testing.T) {
	s := newClassifySupervisor("claude")
	ap := newMarkerAgent(t, StopReasonWatchdog,
		agenterr.BillingWallMarker+": Your credit balance is too low.\n")

	s.classifyAgentExit(ap, 137)

	ap.Mu.Lock()
	lastErr, noWork := ap.LastError, ap.LastNoWork
	ap.Mu.Unlock()

	if noWork {
		t.Fatal("a walled agent was filed as idle")
	}
	if lastErr == nil || lastErr.Class != agenterr.OutcomeFromHarness(wrapper.ErrBilling) {
		t.Fatalf("LastError = %#v, want ErrBilling", lastErr)
	}
}

// The guard on the override: only the explicit markers outrank the stop reason.
// An ordinary duration kill keeps its timeout verdict, whatever the tail says.
func TestClassifyAgentExit_UnmarkedDurationKillStaysTimeout(t *testing.T) {
	s := newClassifySupervisor("claude")
	ap := newMarkerAgent(t, StopReasonRunDurationExceeded,
		"the agent was discussing a billing problem and credits when it was killed\n")

	s.classifyAgentExit(ap, 143)

	ap.Mu.Lock()
	lastErr := ap.LastError
	ap.Mu.Unlock()

	if lastErr == nil || lastErr.Class != agenterr.OutcomeFromHarness(wrapper.ErrTimeout) {
		t.Fatalf("LastError = %#v, want the duration-kill timeout — a pattern match must never override", lastErr)
	}
}
