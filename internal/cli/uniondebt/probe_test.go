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
		if _, err := p.Probe(t.TempDir(), "local/union", bad); err == nil {
			t.Errorf("task ID %q should be rejected", bad)
		}
		if _, err := p.Probe(t.TempDir(), bad, "PUPPET-1"); err == nil {
			t.Errorf("union branch %q should be rejected", bad)
		}
	}
}

func mustProbe(t *testing.T, clone, taskID string) ProbeResult {
	t.Helper()
	got, err := NewProber().Probe(clone, "local/union", taskID)
	if err != nil {
		t.Fatalf("Probe(%s): %v", taskID, err)
	}
	return got
}
