package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// TestPrintProfileDrifts_SilentWhenNoneDrifted: the section exists for an
// abnormal condition, so a healthy fleet must not grow a line about it.
func TestPrintProfileDrifts_SilentWhenNoneDrifted(t *testing.T) {
	if out := captureStdout(t, func() { printProfileDrifts(nil) }); out != "" {
		t.Errorf("no drift must print nothing, got %q", out)
	}
}

// TestPrintProfileDrifts_NamesBothVersions is the point of surfacing this in
// status at all: the operator sees "running unverified" without grepping the
// daemon log for a warning that was printed once, hours ago.
func TestPrintProfileDrifts_NamesBothVersions(t *testing.T) {
	out := captureStdout(t, func() {
		printProfileDrifts([]supervisor.ProfileDrift{{
			Dir:      ".loom/agent-profiles/planner/claude",
			Binary:   "claude",
			Manifest: "2.1.250 (Claude Code)",
			Observed: "2.1.251 (Claude Code)",
			FirstAt:  time.Now(),
			Count:    7,
		}})
	})
	for _, want := range []string{
		"running unverified",
		".loom/agent-profiles/planner/claude",
		"2.1.250 (Claude Code)",
		"2.1.251 (Claude Code)",
		"7 spawn(s)",
		"loom doctor --fix",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestWriteStateFile_CarriesProfileDrifts: `loom daemon status` runs in a
// different process from the daemon, so the state file is the only channel the
// recorded condition can travel over.
func TestWriteStateFile_CarriesProfileDrifts(t *testing.T) {
	supervisor.ResetProfileDrifts()
	t.Cleanup(supervisor.ResetProfileDrifts)

	path := filepath.Join(t.TempDir(), "daemon-agents.json")
	if err := writeStateFile(path, time.Now(), nil, nil, nil, nil, 3); err != nil {
		t.Fatalf("writeStateFile: %v", err)
	}
	state, err := ReadStateFile(path)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if len(state.ProfileDrifts) != 0 {
		t.Fatalf("a clean daemon must record no drift, got %+v", state.ProfileDrifts)
	}
}
