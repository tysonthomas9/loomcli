package git

import (
	"errors"
	"strings"
	"testing"
)

// verifyPulled is the whole point of the change: every state below is a state
// that used to print ✓ because the previous step returned nil.
func TestVerifyPulled_States(t *testing.T) {
	t.Parallel()

	const before = "aaaaaaaaaaaa"
	const after = "bbbbbbbbbbbb"

	noMerge := []CommandStub{
		{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""},
		{Name: "git", Args: []string{"rev-parse", "--verify", "MERGE_HEAD"}, Err: errNoMergeHead},
	}

	tests := []struct {
		name      string
		remote    string
		source    string
		head      string
		stubs     []CommandStub
		wantState syncState
		wantIn    []string // substrings required in Detail
		wantNoCmd string   // a command that must not be run
	}{
		{
			name:   "behind 0 and HEAD moved is advanced",
			source: "dev",
			head:   before,
			stubs: append(append([]CommandStub{}, noMerge...),
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/dev"}, Stdout: "ccc\n"},
				CommandStub{Name: "git", Args: []string{"rev-list", "--count", "HEAD..origin/dev"}, Stdout: "0\n"},
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Stdout: after + "\n"},
			),
			wantState: syncStateAdvanced,
		},
		{
			name:   "behind 0 and HEAD unchanged is already current",
			source: "dev",
			head:   after,
			stubs: append(append([]CommandStub{}, noMerge...),
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/dev"}, Stdout: "ccc\n"},
				CommandStub{Name: "git", Args: []string{"rev-list", "--count", "HEAD..origin/dev"}, Stdout: "0\n"},
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Stdout: after + "\n"},
			),
			wantState: syncStateAlreadyCurrent,
		},
		{
			// The reported incident: the merge reported success and the
			// worktree is still eight commits behind.
			name:   "still behind after the merge is not a success",
			source: "dev",
			head:   before,
			stubs: append(append([]CommandStub{}, noMerge...),
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/dev"}, Stdout: "ccc\n"},
				CommandStub{Name: "git", Args: []string{"rev-list", "--count", "HEAD..origin/dev"}, Stdout: "8\n"},
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Stdout: after + "\n"},
			),
			wantState: syncStateBehind,
			wantIn:    []string{"8", "origin/dev"},
		},
		{
			name:   "open MERGE_HEAD short-circuits before any distance is measured",
			source: "dev",
			head:   before,
			stubs: []CommandStub{
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: ""},
				{Name: "git", Args: []string{"rev-parse", "--verify", "MERGE_HEAD"}, Stdout: "ddd\n"},
			},
			wantState: syncStateUnresolved,
			wantIn:    []string{"merge unresolved"},
			wantNoCmd: "rev-list",
		},
		{
			name:   "conflicted files are unresolved and counted",
			source: "dev",
			head:   before,
			stubs: []CommandStub{
				{Name: "git", Args: []string{"diff", "--name-only", "--diff-filter=U"}, Stdout: "a.go\nb.go\n"},
			},
			wantState: syncStateUnresolved,
			wantIn:    []string{"2 conflicted files"},
			wantNoCmd: "rev-list",
		},
		{
			name:   "missing remote ref is unverified, never a tick",
			source: "dev",
			head:   before,
			stubs: append(append([]CommandStub{}, noMerge...),
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/dev"}, Err: errors.New("unknown revision")},
			),
			wantState: syncStateUnverified,
			wantIn:    []string{"origin/dev", "fetch may have failed"},
		},
		{
			name:   "non-numeric commit count is unverified, not a crash",
			source: "dev",
			head:   before,
			stubs: append(append([]CommandStub{}, noMerge...),
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/dev"}, Stdout: "ccc\n"},
				CommandStub{Name: "git", Args: []string{"rev-list", "--count", "HEAD..origin/dev"}, Stdout: "garbage\n"},
			),
			wantState: syncStateUnverified,
		},
		{
			name:   "rev-list failure is unverified",
			source: "dev",
			head:   before,
			stubs: append(append([]CommandStub{}, noMerge...),
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/origin/dev"}, Stdout: "ccc\n"},
				CommandStub{Name: "git", Args: []string{"rev-list", "--count", "HEAD..origin/dev"}, Err: errors.New("boom")},
			),
			wantState: syncStateUnverified,
		},
		{
			name:   "a non-default remote is measured against that remote",
			remote: "upstream",
			source: "main",
			head:   before,
			stubs: append(append([]CommandStub{}, noMerge...),
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "refs/remotes/upstream/main"}, Stdout: "ccc\n"},
				CommandStub{Name: "git", Args: []string{"rev-list", "--count", "HEAD..upstream/main"}, Stdout: "3\n"},
				CommandStub{Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}, Stdout: after + "\n"},
			),
			wantState: syncStateBehind,
			wantIn:    []string{"upstream/main"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, _, _, _ := NewTestDeps(t)
			cmdMock := NewCommandMock(t, tc.stubs)
			cmdMock.InstallOn(deps)

			o := pullOutcome{
				Name:       "repo",
				Path:       "/ws/repo",
				Source:     tc.source,
				Remote:     tc.remote,
				HeadBefore: tc.head,
			}
			verifyPulled(deps, &o)

			if o.State != tc.wantState {
				t.Fatalf("state = %v, want %v (detail %q)", o.State, tc.wantState, o.Detail)
			}
			if tc.wantState != syncStateAdvanced && tc.wantState != syncStateAlreadyCurrent && o.InSync() {
				t.Error("a non-success state must never report InSync")
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(o.summaryDetail(), want) {
					t.Errorf("detail %q does not contain %q", o.summaryDetail(), want)
				}
			}
			if tc.wantNoCmd != "" {
				for _, call := range cmdMock.Calls() {
					if len(call.Args) > 0 && call.Args[0] == tc.wantNoCmd {
						t.Errorf("unexpected %q call: %v", tc.wantNoCmd, call.Args)
					}
				}
			}
		})
	}
}

func TestSummaryFailures_CountsOnlyEvidenceOfFailure(t *testing.T) {
	t.Parallel()

	outcomes := []pullOutcome{
		{State: syncStateAdvanced},
		{State: syncStateAlreadyCurrent},
		{State: syncStateBehind},
		{State: syncStateUnresolved},
		{State: syncStateFailed},
		{State: syncStateUnverified}, // no evidence either way — not a failure
		{State: syncStateSkipped},    // never attempted — not a failure
	}

	if n := summaryFailures(outcomes); n != 3 {
		t.Errorf("summaryFailures = %d, want 3", n)
	}
}

func TestPrintPullSummary_BehindRendersAsFailureNotTick(t *testing.T) {
	// not parallel: swaps os.Stdout
	outcomes := []pullOutcome{
		{Name: "explorer", Source: "main", HeadAfter: "abc1234def", State: syncStateAlreadyCurrent},
		{Name: "tree-clustering", Source: "dev", Behind: 8, State: syncStateBehind,
			Detail: "still 8 commit(s) behind origin/dev after merge"},
		{Name: "tools", State: syncStateSkipped, Detail: "no repo metadata in workspace config"},
	}

	out := captureStdout(t, func() { printPullSummary(outcomes, nil) })

	var behindLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "tree-clustering") {
			behindLine = line
		}
	}
	if behindLine == "" {
		t.Fatalf("no line for the behind repo in:\n%s", out)
	}
	if !strings.Contains(behindLine, "✗") {
		t.Errorf("behind repo must be marked ✗, got %q", behindLine)
	}
	if strings.Contains(behindLine, "✓") {
		t.Errorf("behind repo must never print ✓, got %q", behindLine)
	}
	if !strings.Contains(behindLine, "still 8 commit(s) behind origin/dev") {
		t.Errorf("behind repo must state the measurement, got %q", behindLine)
	}
	if !strings.Contains(out, "1 in sync, 1 failed, 1 skipped") {
		t.Errorf("counts line missing or wrong in:\n%s", out)
	}
	if !strings.Contains(out, "– tools") || !strings.Contains(out, "skipped: no repo metadata") {
		t.Errorf("a skipped repo must still be visible in:\n%s", out)
	}
}

func TestPrintPullSummary_NotCoveredLine(t *testing.T) {
	// not parallel: swaps os.Stdout
	outcomes := []pullOutcome{{Name: "api", Source: "main", State: syncStateAlreadyCurrent}}

	withCoverage := captureStdout(t, func() {
		printPullSummary(outcomes, []string{"api/critic", "api/planner", "api/tester", "api/observer"})
	})
	if !strings.Contains(withCoverage, "Not covered") {
		t.Errorf("expected a Not covered line in:\n%s", withCoverage)
	}
	if !strings.Contains(withCoverage, "api/critic, api/planner, api/tester, and 1 more") {
		t.Errorf("expected the truncated agent list in:\n%s", withCoverage)
	}

	without := captureStdout(t, func() { printPullSummary(outcomes, nil) })
	if strings.Contains(without, "Not covered") {
		t.Errorf("no Not covered line expected in:\n%s", without)
	}
}

func TestGitShortSHA(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                     "",
		"abc":                  "abc",
		"abcdef1":              "abcdef1",
		"abcdef1234567890":     "abcdef1",
		"0000000000000000beef": "0000000",
	}
	for in, want := range cases {
		if got := gitShortSHA(in); got != want {
			t.Errorf("gitShortSHA(%q) = %q, want %q", in, got, want)
		}
	}
}
