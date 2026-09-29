package uniondebt

import (
	"errors"
	"strings"
	"testing"
)

// scriptGit answers declared git invocations and fails every other one. The
// default matters: an undeclared `rev-parse --verify` means "this ref does not
// resolve", which is exactly the state the union enumeration must refuse to
// paper over.
type scriptGit struct {
	out   map[string]string
	codes map[string]int
	calls []string
}

func (g *scriptGit) Run(_ string, args ...string) (string, int, error) {
	key := strings.Join(args, " ")
	g.calls = append(g.calls, key)
	if code, ok := g.codes[key]; ok {
		return g.out[key], code, nil
	}
	if out, ok := g.out[key]; ok {
		return out, 0, nil
	}
	return "", 1, nil
}

func (g *scriptGit) called(key string) bool {
	for _, c := range g.calls {
		if c == key {
			return true
		}
	}
	return false
}

const logFormat = "log --first-parent --merges --format=%H%x00%P%x00%s "

// logLine renders one `git log` record in the NUL-separated format
// UnionMerges asks for.
func logLine(sha, parents, subject string) string {
	return sha + "\x00" + parents + "\x00" + subject
}

// unionLog is the corpus of merge subjects observed on this workspace's union
// branches, verbatim. Every shape that carries a task ref must parse, and the
// three that do not must yield nothing — an integrator's wording is not a
// contract, so the parser may not depend on one.
var unionLog = strings.Join([]string{
	logLine("a1", "p0 c1", "local union: merge loom/PUPPET-432 (fix the thing)"),
	logLine("a2", "p1 c2", "local union: merge loom/PUPPET-233-r2 (rebuilt branch)"),
	logLine("a3", "p2 c3", "union: PR #619 (loom/PUPPET-530)"),
	logLine("a4", "p3 c4", "union: PR #691 (loom/PUPPET-531), re-cut head"),
	logLine("a5", "p4 c5", "Merge branch 'loom/PUPPET-97' into local/union"),
	logLine("a6", "p5 c6", "Merge pull request #12 from tysonthomas9/loom/PUPPET-11"),
	logLine("a7", "p6 c7", "local union: merge origin/v5"),
	logLine("a8", "p7 c8", "local union: merge feat/harness-output-classification"),
	logLine("a9", "p8 c9", "Merge remote-tracking branch 'origin/v5' into local/union"),
}, "\n")

func unionGit(log string) *scriptGit {
	return &scriptGit{out: map[string]string{
		"rev-parse -q --verify origin/v5":    "trunksha\n",
		"rev-parse -q --verify local/union":  "unionsha\n",
		logFormat + "origin/v5..local/union": log,
	}}
}

func TestUnionMerges_SubjectShapes(t *testing.T) {
	git := unionGit(unionLog)
	merges, err := (&Prober{git: git}).UnionMerges("/clones/loomcli", "local/union", "v5")
	if err != nil {
		t.Fatalf("UnionMerges: %v", err)
	}

	want := []string{"PUPPET-11", "PUPPET-233", "PUPPET-432", "PUPPET-530", "PUPPET-531", "PUPPET-97"}
	var got []string
	for _, m := range merges {
		got = append(got, m.TaskID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("task IDs = %v, want %v (sorted, revisions collapsed, non-task subjects dropped)", got, want)
	}

	// The inherited v5 merge parses; it is the RANGE that keeps it out of a
	// real enumeration, not the parser. Assert the range was asked for.
	if !git.called(logFormat + "origin/v5..local/union") {
		t.Errorf("git log was not ranged origin/v5..local/union; calls: %v", git.calls)
	}
	for _, m := range merges {
		if m.TaskID == "PUPPET-233" {
			if m.MergeSHA != "a2" || m.ParentSHA != "c2" {
				t.Errorf("PUPPET-233 = %+v, want merge a2 / second parent c2", m)
			}
		}
	}
}

func TestUnionMerges_TrunkFallbackAndFailures(t *testing.T) {
	t.Run("falls back to the bare trunk", func(t *testing.T) {
		git := &scriptGit{out: map[string]string{
			"rev-parse -q --verify v5":          "trunksha\n",
			"rev-parse -q --verify local/union": "unionsha\n",
			logFormat + "v5..local/union":       logLine("a1", "p0 c1", "union: PR #1 (loom/PUPPET-7)"),
		}}
		merges, err := (&Prober{git: git}).UnionMerges("/clones/loomcli", "local/union", "v5")
		if err != nil {
			t.Fatalf("UnionMerges: %v", err)
		}
		if len(merges) != 1 || merges[0].TaskID != "PUPPET-7" {
			t.Errorf("merges = %+v, want the single PUPPET-7 entry", merges)
		}
	})

	t.Run("no trunk resolves", func(t *testing.T) {
		git := &scriptGit{out: map[string]string{"rev-parse -q --verify local/union": "unionsha\n"}}
		_, err := (&Prober{git: git}).UnionMerges("/clones/loomcli", "local/union", "v5")
		if !errors.Is(err, errUnionRange) {
			t.Errorf("err = %v, want errUnionRange: an unresolvable trunk must never read as an empty union", err)
		}
	})

	t.Run("no union branch", func(t *testing.T) {
		git := &scriptGit{out: map[string]string{"rev-parse -q --verify origin/v5": "trunksha\n"}}
		merges, err := (&Prober{git: git}).UnionMerges("/clones/loomcli", "local/union", "v5")
		if !errors.Is(err, errUnionRange) {
			t.Errorf("err = %v, want errUnionRange", err)
		}
		if merges != nil {
			t.Errorf("merges = %+v, want nil: an empty list would report this repo as owing nothing", merges)
		}
	})
}

func TestUnionMerges_DedupesKeepingNewest(t *testing.T) {
	log := strings.Join([]string{
		logLine("new", "p0 cnew", "union: PR #691 (loom/PUPPET-530), re-cut head"),
		logLine("old", "p1 cold", "local union: merge loom/PUPPET-530 (first cut)"),
	}, "\n")
	merges, err := (&Prober{git: unionGit(log)}).UnionMerges("/clones/loomcli", "local/union", "v5")
	if err != nil {
		t.Fatalf("UnionMerges: %v", err)
	}
	if len(merges) != 1 {
		t.Fatalf("merges = %+v, want one entry for the one task", merges)
	}
	if merges[0].MergeSHA != "new" {
		t.Errorf("merge SHA = %q, want the newest merge %q", merges[0].MergeSHA, "new")
	}
}

func TestUnionTip(t *testing.T) {
	git := &scriptGit{out: map[string]string{"rev-parse local/union": "c96d21676\n"}}
	tip, err := (&Prober{git: git}).UnionTip("/clones/loomcli", "local/union")
	if err != nil || tip != "c96d21676" {
		t.Fatalf("UnionTip = %q, %v; want c96d21676", tip, err)
	}
	if _, err := (&Prober{git: &scriptGit{}}).UnionTip("/clones/loomcli", "local/union"); !errors.Is(err, errUnionRange) {
		t.Errorf("missing branch err = %v, want errUnionRange", err)
	}
}

func TestLanded(t *testing.T) {
	const ref = "origin/loom/PUPPET-97"
	base := map[string]string{
		"rev-parse -q --verify origin/v5": "trunksha\n",
		"rev-parse -q --verify " + ref:    "tipsha00\n",
	}
	cases := []struct {
		name       string
		codes      map[string]int
		out        map[string]string
		wantLanded bool
		wantIn     string
	}{
		{
			name:       "ancestor of the trunk",
			codes:      map[string]int{"merge-base --is-ancestor " + ref + " origin/v5": 0},
			wantLanded: true,
			wantIn:     "origin/v5 contains " + ref,
		},
		{
			name:  "every commit patch-equivalent upstream",
			codes: map[string]int{"merge-base --is-ancestor " + ref + " origin/v5": 1},
			out: map[string]string{
				"cherry origin/v5 " + ref: "- aaa\n- bbb\n- ccc\n- ddd\n",
			},
			wantLanded: true,
			wantIn:     "all 4 commit(s)",
		},
		{
			name:  "some commits are not upstream",
			codes: map[string]int{"merge-base --is-ancestor " + ref + " origin/v5": 1},
			out: map[string]string{
				"cherry origin/v5 " + ref: "- aaa\n+ bbb\n",
			},
			wantLanded: false,
			wantIn:     "1 of 2 commit(s) are not upstream",
		},
		{
			// An empty diff proves nothing, and this answer decides whether a
			// marker gets written.
			name:       "empty cherry output is not landed",
			codes:      map[string]int{"merge-base --is-ancestor " + ref + " origin/v5": 1},
			out:        map[string]string{"cherry origin/v5 " + ref: "\n"},
			wantLanded: false,
			wantIn:     "0 of 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			git := &scriptGit{out: map[string]string{}, codes: tc.codes}
			for k, v := range base {
				git.out[k] = v
			}
			for k, v := range tc.out {
				git.out[k] = v
			}
			landed, why, err := (&Prober{git: git}).Landed("/clones/loomcli", "v5", "PUPPET-97")
			if err != nil {
				t.Fatalf("Landed: %v", err)
			}
			if landed != tc.wantLanded {
				t.Errorf("landed = %v, want %v (%s)", landed, tc.wantLanded, why)
			}
			if !strings.Contains(why, tc.wantIn) {
				t.Errorf("reason = %q, want it to contain %q", why, tc.wantIn)
			}
		})
	}
}

func TestLanded_NoRefResolves(t *testing.T) {
	git := &scriptGit{out: map[string]string{"rev-parse -q --verify origin/v5": "trunksha\n"}}
	_, _, err := (&Prober{git: git}).Landed("/clones/loomcli", "v5", "PUPPET-97")
	if !errors.Is(err, errNoTaskRef) {
		t.Errorf("err = %v, want errNoTaskRef so the caller can report pr-no-branch instead of labeling", err)
	}
}
