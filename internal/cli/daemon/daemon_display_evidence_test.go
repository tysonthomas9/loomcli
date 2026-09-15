package daemon

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPrintAgentEvidence_OnlyForFailedOrBlocked(t *testing.T) {
	const summary = "harness_marker rule=AuthRequiredMarker screen=banner:claude.loggedout.run_login,composer=false"

	cases := []struct {
		status string
		want   bool
	}{
		{"failed", true},
		{"blocked", true},
		// A parked idle agent carries supervisor.no_work evidence on every
		// cycle; printing it would bury the lines an operator must act on.
		{"stopped", false},
		{"running", false},
	}

	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			out := captureStdout(t, func() {
				printAgentEvidence(DaemonAgentStatus{Status: tc.status, LastErrorEvidence: summary})
			})
			if got := strings.Contains(out, "Evidence:"); got != tc.want {
				t.Errorf("status %q printed evidence = %v, want %v (out=%q)", tc.status, got, tc.want, out)
			}
		})
	}
}

func TestPrintAgentEvidence_EmptyPrintsNothing(t *testing.T) {
	out := captureStdout(t, func() {
		printAgentEvidence(DaemonAgentStatus{Status: "failed"})
	})
	if out != "" {
		t.Errorf("printed %q for empty evidence, want nothing", out)
	}
}

func TestTruncateDisplay_CutsOnRuneBoundary(t *testing.T) {
	// Box-drawing glyphs are three bytes each; a byte cut lands mid-rune.
	long := strings.Repeat("─", 200)
	got := truncateDisplay(long, evidenceDisplayCap)
	if len(got) > evidenceDisplayCap {
		t.Errorf("len = %d, want <= %d", len(got), evidenceDisplayCap)
	}
	if !utf8.ValidString(got) {
		t.Error("truncated output is not valid UTF-8")
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("got %q, want a trailing ellipsis", got)
	}
	if short := truncateDisplay("abc", evidenceDisplayCap); short != "abc" {
		t.Errorf("short input = %q, want %q unchanged", short, "abc")
	}
}
