package uniondebt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv keeps every test repo hermetic: no global/system config, fixed
// identity, and a fixed initial branch name.
func gitEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	}
}

type repoFixture struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repoFixture {
	t.Helper()
	r := &repoFixture{t: t, dir: t.TempDir()}
	r.git("init", "-b", "main")
	r.write("shared.txt", "base\n")
	r.git("add", ".")
	r.git("commit", "-m", "base")
	return r
}

func (r *repoFixture) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec
	cmd.Dir = r.dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repoFixture) write(name, body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// commitOn checks out branch (creating it from `from`), writes a file and
// commits.
func (r *repoFixture) commitOn(branch, from, file, body, msg string) {
	r.t.Helper()
	r.git("checkout", "-B", branch, from)
	r.write(file, body)
	r.git("add", ".")
	r.git("commit", "-m", msg)
}

// remoteRef publishes one commit as a REAL remote-tracking ref under
// refs/remotes/origin/. Revision discovery reads that namespace with
// for-each-ref, so a local branch merely named "origin/..." would not be seen.
func (r *repoFixture) remoteRef(shortName, file, body, msg string) string {
	r.t.Helper()
	const staging = "tmp/staging"
	r.commitOn(staging, "main", file, body, msg)
	sha := r.git("rev-parse", staging)
	r.git("update-ref", "refs/remotes/origin/"+shortName, sha)
	r.git("checkout", "main")
	r.git("branch", "-D", staging)
	return sha
}

func TestProbe_Classes(t *testing.T) {
	t.Run("in union", func(t *testing.T) {
		r := newRepo(t)
		r.commitOn("loom/PUPPET-1", "main", "a.txt", "a\n", "work")
		// union contains the branch tip outright.
		r.git("branch", "local/union", "loom/PUPPET-1")

		got := mustProbe(t, r.dir, "PUPPET-1")
		if got.Class != ClassInUnion {
			t.Fatalf("Class = %s, want %s", got.Class, ClassInUnion)
		}
		if got.Ref != "loom/PUPPET-1" || got.TipSHA == "" {
			t.Errorf("Ref/TipSHA = %q/%q, want the branch and a SHA", got.Ref, got.TipSHA)
		}
	})

	t.Run("clean", func(t *testing.T) {
		r := newRepo(t)
		r.commitOn("loom/PUPPET-2", "main", "branch-only.txt", "b\n", "disjoint work")
		r.commitOn("local/union", "main", "union-only.txt", "u\n", "union work")

		got := mustProbe(t, r.dir, "PUPPET-2")
		if got.Class != ClassClean {
			t.Fatalf("Class = %s, want %s", got.Class, ClassClean)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		r := newRepo(t)
		r.commitOn("loom/PUPPET-3", "main", "shared.txt", "branch side\n", "branch edit")
		r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

		got := mustProbe(t, r.dir, "PUPPET-3")
		if got.Class != ClassConflict {
			t.Fatalf("Class = %s, want %s", got.Class, ClassConflict)
		}
		if !strings.Contains(got.Conflict, "shared.txt") {
			t.Errorf("Conflict summary should name the conflicting file, got %q", got.Conflict)
		}
	})

	t.Run("no branch", func(t *testing.T) {
		r := newRepo(t)
		r.git("branch", "local/union", "main")

		got := mustProbe(t, r.dir, "PUPPET-404")
		if got.Class != ClassNoBranch {
			t.Fatalf("Class = %s, want %s", got.Class, ClassNoBranch)
		}
	})

	t.Run("no union branch", func(t *testing.T) {
		r := newRepo(t)
		r.commitOn("loom/PUPPET-5", "main", "a.txt", "a\n", "work")

		got := mustProbe(t, r.dir, "PUPPET-5")
		if got.Class != ClassNoUnion {
			t.Fatalf("Class = %s, want %s", got.Class, ClassNoUnion)
		}
	})

	t.Run("clone path absent", func(t *testing.T) {
		got := mustProbe(t, filepath.Join(t.TempDir(), "nope"), "PUPPET-6")
		if got.Class != ClassNoUnion {
			t.Fatalf("Class = %s, want %s", got.Class, ClassNoUnion)
		}
	})
}

// TestProbe_PrefersOriginRef pins the resolution order: origin/loom/<ID> wins
// when both exist.
func TestProbe_PrefersOriginRef(t *testing.T) {
	r := newRepo(t)
	r.commitOn("loom/PUPPET-7", "main", "a.txt", "local side\n", "local work")
	localTip := r.git("rev-parse", "loom/PUPPET-7")
	r.commitOn("origin/loom/PUPPET-7", "main", "b.txt", "origin side\n", "origin work")
	originTip := r.git("rev-parse", "origin/loom/PUPPET-7")
	r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

	got := mustProbe(t, r.dir, "PUPPET-7")
	if got.Ref != "origin/loom/PUPPET-7" {
		t.Errorf("Ref = %q, want origin/loom/PUPPET-7", got.Ref)
	}
	if got.TipSHA != originTip || got.TipSHA == localTip {
		t.Errorf("TipSHA = %q, want the origin tip %q", got.TipSHA, originTip)
	}
}

// TestProbe_LocalOnlyRefFallback is the PUPPET-308 case: the branch exists only
// as a bare local loom/<ID>. An origin-only lookup would call it NoBranch and
// wrongly retire real debt.
func TestProbe_LocalOnlyRefFallback(t *testing.T) {
	r := newRepo(t)
	r.commitOn("loom/PUPPET-308", "main", "shared.txt", "branch side\n", "branch edit")
	r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

	got := mustProbe(t, r.dir, "PUPPET-308")
	if got.Class != ClassConflict {
		t.Fatalf("Class = %s, want %s", got.Class, ClassConflict)
	}
	if got.Ref != "loom/PUPPET-308" {
		t.Errorf("Ref = %q, want the local loom/PUPPET-308", got.Ref)
	}
}

func TestProbe_RejectsMalformedRefs(t *testing.T) {
	p := NewProber()
	for _, bad := range []string{"", "--upload-pack=evil", "a b", "../etc", "x..y", "-x"} {
		if _, err := p.Probe(t.TempDir(), "local/union", bad, ""); err == nil {
			t.Errorf("task ID %q should be rejected", bad)
		}
		if _, err := p.Probe(t.TempDir(), bad, "PUPPET-1", ""); err == nil {
			t.Errorf("union branch %q should be rejected", bad)
		}
	}
}

func mustProbe(t *testing.T, clone, taskID string) ProbeResult {
	t.Helper()
	return mustProbeTip(t, clone, taskID, "")
}

func mustProbeTip(t *testing.T, clone, taskID, recordedTip string) ProbeResult {
	t.Helper()
	got, err := NewProber().Probe(clone, "local/union", taskID, recordedTip)
	if err != nil {
		t.Fatalf("Probe(%s): %v", taskID, err)
	}
	return got
}

// --- superseded ---

// TestProbe_SupersededWhenRefMoved is the PUPPET-540 shape: the debt records a
// tip, and the ref answering to the task's name today is not its descendant.
func TestProbe_SupersededWhenRefMoved(t *testing.T) {
	r := newRepo(t)
	r.commitOn("loom/PUPPET-540", "main", "shared.txt", "first cut\n", "abandoned work")
	abandoned := r.git("rev-parse", "loom/PUPPET-540")
	// The branch is re-cut from main: the old tip is no longer an ancestor.
	r.commitOn("loom/PUPPET-540", "main", "shared.txt", "rebuilt\n", "rebuilt work")
	rebuilt := r.git("rev-parse", "loom/PUPPET-540")
	r.commitOn("local/union", "main", "union-only.txt", "u\n", "union work")

	got := mustProbeTip(t, r.dir, "PUPPET-540", abandoned)
	if got.Class != ClassSuperseded {
		t.Fatalf("Class = %s, want %s", got.Class, ClassSuperseded)
	}
	if got.TipSHA != rebuilt {
		t.Errorf("TipSHA = %q, want the rebuilt tip %q", got.TipSHA, rebuilt)
	}
	if !strings.Contains(got.Detail, abandoned) {
		t.Errorf("Detail should name the recorded tip, got %q", got.Detail)
	}
}

// TestProbe_SupersededWhenRecordedTipGone covers the replaced-and-collected
// case: the recorded sha does not resolve at all, but a current, different ref
// does.
func TestProbe_SupersededWhenRecordedTipGone(t *testing.T) {
	r := newRepo(t)
	r.commitOn("loom/PUPPET-541", "main", "shared.txt", "branch side\n", "branch edit")
	r.commitOn("local/union", "main", "union-only.txt", "u\n", "union work")

	got := mustProbeTip(t, r.dir, "PUPPET-541", "0123456789abcdef0123456789abcdef01234567")
	if got.Class != ClassSuperseded {
		t.Fatalf("Class = %s, want %s", got.Class, ClassSuperseded)
	}
	if !strings.Contains(got.Detail, "no longer resolves") {
		t.Errorf("Detail should say the recorded tip is gone, got %q", got.Detail)
	}
}

// TestProbe_SupersededWhenContentAlreadyInUnion needs no recorded tip at all:
// every path the branch touches is byte-identical in union already.
func TestProbe_SupersededWhenContentAlreadyInUnion(t *testing.T) {
	r := newRepo(t)
	r.commitOn("loom/PUPPET-542", "main", "feature.txt", "feature\n", "branch work")
	// Union got the same content by another route (a cherry-pick, say), and
	// then moved on, so the branch tip is not an ancestor of it.
	r.commitOn("local/union", "main", "feature.txt", "feature\n", "same content, other route")
	r.write("unrelated.txt", "later\n")
	r.git("add", ".")
	r.git("commit", "-m", "union moves on")

	got := mustProbeTip(t, r.dir, "PUPPET-542", "")
	if got.Class != ClassSuperseded {
		t.Fatalf("Class = %s, want %s", got.Class, ClassSuperseded)
	}
	if !strings.Contains(got.Detail, "byte-identical") {
		t.Errorf("Detail should name the content signal, got %q", got.Detail)
	}
}

// TestProbe_NotSupersededWhenOnlySomePathsMatch: some-but-not-all is ordinary
// debt. Retiring it would drop real work on the floor.
func TestProbe_NotSupersededWhenOnlySomePathsMatch(t *testing.T) {
	r := newRepo(t)
	r.git("checkout", "-B", "loom/PUPPET-543", "main")
	r.write("landed.txt", "landed\n")
	r.write("still-missing.txt", "missing\n")
	r.git("add", ".")
	r.git("commit", "-m", "two files")
	// Union has one of the two.
	r.commitOn("local/union", "main", "landed.txt", "landed\n", "one arrived")

	got := mustProbeTip(t, r.dir, "PUPPET-543", "")
	if got.Class == ClassSuperseded {
		t.Fatalf("Class = %s; only some paths match, this is ordinary debt", got.Class)
	}
}

// TestProbe_NotSupersededWithoutRecordedTip pins that the ref-moved signal is
// simply not evaluated when the tip is unknown.
func TestProbe_NotSupersededWithoutRecordedTip(t *testing.T) {
	r := newRepo(t)
	r.commitOn("loom/PUPPET-544", "main", "shared.txt", "branch side\n", "branch edit")
	r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

	got := mustProbeTip(t, r.dir, "PUPPET-544", "")
	if got.Class != ClassConflict {
		t.Fatalf("Class = %s, want %s with no recorded tip", got.Class, ClassConflict)
	}
}

// TestProbe_NotSupersededOnEmptyPathDiff: a branch whose merge base with union
// already has all of its content touches no path, and an empty path list must
// never be read as "all paths match".
func TestProbe_NotSupersededOnEmptyPathDiff(t *testing.T) {
	r := newRepo(t)
	// The branch adds nothing beyond main; union moved on separately, so the
	// branch tip is not an ancestor of union.
	r.git("branch", "loom/PUPPET-545", "main")
	r.commitOn("local/union", "main", "union-only.txt", "u\n", "union work")

	got := mustProbeTip(t, r.dir, "PUPPET-545", "")
	if got.Class == ClassSuperseded {
		t.Fatalf("Class = %s; an empty path diff is not evidence of anything", got.Class)
	}
}

// --- revision refs ---

// TestProbe_PrefersRevisionRef: a rebuilt branch is republished as -r2, and
// that is the branch a merge should consider, not the unsuffixed original.
func TestProbe_PrefersRevisionRef(t *testing.T) {
	r := newRepo(t)
	r.remoteRef("loom/PUPPET-550", "a.txt", "first cut\n", "first")
	r2Tip := r.remoteRef("loom/PUPPET-550-r2", "a.txt", "second cut\n", "second")
	r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

	got := mustProbe(t, r.dir, "PUPPET-550")
	if got.Ref != "origin/loom/PUPPET-550-r2" {
		t.Fatalf("Ref = %q, want the -r2 revision", got.Ref)
	}
	if got.TipSHA != r2Tip {
		t.Errorf("TipSHA = %q, want the -r2 tip %q", got.TipSHA, r2Tip)
	}
}

// TestProbe_RevisionsSortNumerically pins -r10 over -r2; a lexical sort gets
// this backwards.
func TestProbe_RevisionsSortNumerically(t *testing.T) {
	r := newRepo(t)
	for _, n := range []string{"-r2", "-r3", "-r10"} {
		r.remoteRef("loom/PUPPET-551"+n, "a.txt", n+"\n", "cut "+n)
	}
	r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

	if got := mustProbe(t, r.dir, "PUPPET-551"); got.Ref != "origin/loom/PUPPET-551-r10" {
		t.Fatalf("Ref = %q, want the -r10 revision", got.Ref)
	}
}

// TestProbe_RejectsRevisionLookalikes: -rc1, -r2x and -review all match the
// for-each-ref glob and must be rejected by the anchored regex.
func TestProbe_RejectsRevisionLookalikes(t *testing.T) {
	r := newRepo(t)
	realTip := r.remoteRef("loom/PUPPET-552", "a.txt", "real\n", "real branch")
	for _, suffix := range []string{"-rc1", "-r2x", "-review"} {
		r.remoteRef("loom/PUPPET-552"+suffix, "a.txt", suffix+"\n", "lookalike "+suffix)
	}
	r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

	got := mustProbe(t, r.dir, "PUPPET-552")
	if got.Ref != "origin/loom/PUPPET-552" {
		t.Fatalf("Ref = %q, want the unsuffixed ref: none of the lookalikes is a revision", got.Ref)
	}
	if got.TipSHA != realTip {
		t.Errorf("TipSHA = %q, want %q", got.TipSHA, realTip)
	}
}

// TestProbe_LocalFallbackStaysLast pins the ordering end to end: a revision ref
// and an origin ref both outrank the bare local branch, which remains the last
// resort it has always been.
func TestProbe_LocalFallbackStaysLast(t *testing.T) {
	r := newRepo(t)
	r.commitOn("loom/PUPPET-553", "main", "a.txt", "local\n", "local work")
	r.remoteRef("loom/PUPPET-553", "b.txt", "origin\n", "origin work")
	r.remoteRef("loom/PUPPET-553-r2", "c.txt", "r2\n", "r2 work")
	r.commitOn("local/union", "main", "shared.txt", "union side\n", "union edit")

	if got := mustProbe(t, r.dir, "PUPPET-553"); got.Ref != "origin/loom/PUPPET-553-r2" {
		t.Fatalf("Ref = %q, want the revision ref ahead of both fallbacks", got.Ref)
	}

	// With no revision published, origin still beats local.
	r.git("update-ref", "-d", "refs/remotes/origin/loom/PUPPET-553-r2")
	if got := mustProbe(t, r.dir, "PUPPET-553"); got.Ref != "origin/loom/PUPPET-553" {
		t.Fatalf("Ref = %q, want origin ahead of the local fallback", got.Ref)
	}

	// With neither, the local branch is still found — the PUPPET-308 case.
	r.git("update-ref", "-d", "refs/remotes/origin/loom/PUPPET-553")
	if got := mustProbe(t, r.dir, "PUPPET-553"); got.Ref != "loom/PUPPET-553" {
		t.Fatalf("Ref = %q, want the local fallback", got.Ref)
	}
}
