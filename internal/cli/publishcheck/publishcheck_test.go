package publishcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit answers from a map of joined args -> canned result. An unknown
// invocation fails the test loudly: a silently-defaulted answer would let a
// wrong git command look like a right one.
type fakeGit struct {
	t       *testing.T
	replies map[string]reply
	seen    []string
}

type reply struct {
	stdout string
	code   int
}

func (f *fakeGit) Run(_ string, args ...string) (string, int, error) {
	key := strings.Join(args, " ")
	f.seen = append(f.seen, key)
	r, ok := f.replies[key]
	if !ok {
		f.t.Fatalf("unexpected git invocation %q", key)
	}
	return r.stdout, r.code, nil
}

const (
	localSHA     = "1111111111111111111111111111111111111111"
	publishedSHA = "2222222222222222222222222222222222222222"
	baseSHA      = "3333333333333333333333333333333333333333"
)

// baseReplies is the happy path up to the ancestry questions: a local branch
// exists, a remote ref exists, and no revision refs do.
func baseReplies() map[string]reply {
	return map[string]reply{
		"rev-parse -q --verify loom/T-1":                                         {stdout: localSHA + "\n"},
		"rev-parse -q --verify origin/loom/T-1":                                  {stdout: publishedSHA + "\n"},
		"for-each-ref --format=%(refname:short) refs/remotes/origin/loom/T-1-r*": {stdout: ""},
	}
}

func TestCheckVerdicts(t *testing.T) {
	tests := []struct {
		name  string
		tweak func(map[string]reply)
		want  Verdict
	}{
		{
			name: "unpublished when the remote ref is missing",
			tweak: func(r map[string]reply) {
				r["rev-parse -q --verify origin/loom/T-1"] = reply{code: 1}
			},
			want: VerdictUnpublished,
		},
		{
			name: "identical when the shas match",
			tweak: func(r map[string]reply) {
				r["rev-parse -q --verify origin/loom/T-1"] = reply{stdout: localSHA + "\n"}
			},
			want: VerdictIdentical,
		},
		{
			name: "fast-forward when published is an ancestor of local",
			tweak: func(r map[string]reply) {
				r["merge-base --is-ancestor "+publishedSHA+" "+localSHA] = reply{code: 0}
			},
			want: VerdictFastForward,
		},
		{
			name: "behind when local is an ancestor of published",
			tweak: func(r map[string]reply) {
				r["merge-base --is-ancestor "+publishedSHA+" "+localSHA] = reply{code: 1}
				r["merge-base --is-ancestor "+localSHA+" "+publishedSHA] = reply{code: 0}
			},
			want: VerdictBehind,
		},
		{
			name: "diverged when neither is an ancestor of the other",
			tweak: func(r map[string]reply) {
				r["merge-base --is-ancestor "+publishedSHA+" "+localSHA] = reply{code: 1}
				r["merge-base --is-ancestor "+localSHA+" "+publishedSHA] = reply{code: 1}
				r["merge-base "+publishedSHA+" "+localSHA] = reply{stdout: baseSHA + "\n"}
				r["rev-list --count "+baseSHA+".."+publishedSHA] = reply{stdout: "3\n"}
				r["rev-list --count "+baseSHA+".."+localSHA] = reply{stdout: "5\n"}
			},
			want: VerdictDiverged,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			replies := baseReplies()
			tc.tweak(replies)
			got, err := Check(&fakeGit{t: t, replies: replies}, "/repo", "loom/T-1", "T-1")
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got.Verdict != tc.want {
				t.Fatalf("verdict = %q, want %q", got.Verdict, tc.want)
			}
			if got.Detail == "" {
				t.Error("Detail is empty; every verdict must carry one human sentence")
			}
			if got.Fetched {
				t.Error("Fetched must always be false: this command performs no network access")
			}
			if got.Branch != "loom/T-1" || got.TaskID != "T-1" {
				t.Errorf("identity fields wrong: %+v", got)
			}
		})
	}
}

func TestDivergedDetailNamesBaseAndCounts(t *testing.T) {
	replies := baseReplies()
	replies["merge-base --is-ancestor "+publishedSHA+" "+localSHA] = reply{code: 1}
	replies["merge-base --is-ancestor "+localSHA+" "+publishedSHA] = reply{code: 1}
	replies["merge-base "+publishedSHA+" "+localSHA] = reply{stdout: baseSHA + "\n"}
	replies["rev-list --count "+baseSHA+".."+publishedSHA] = reply{stdout: "3\n"}
	replies["rev-list --count "+baseSHA+".."+localSHA] = reply{stdout: "5\n"}

	got, err := Check(&fakeGit{t: t, replies: replies}, "/repo", "loom/T-1", "T-1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, want := range []string{baseSHA[:12], "3 commit(s) published", "5 local", "loom/T-1-r2"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("Detail %q does not mention %q", got.Detail, want)
		}
	}
}

func TestCheckRejectsUnparseableBranch(t *testing.T) {
	for _, branch := range []string{"loom/../evil", "--upload-pack=x", ""} {
		if _, err := Check(&fakeGit{t: t, replies: map[string]reply{}}, "/repo", branch, "T-1"); err == nil {
			t.Errorf("branch %q was accepted; want a validation error", branch)
		}
	}
}

func TestCheckMissingLocalBranchIsAnError(t *testing.T) {
	replies := map[string]reply{
		"rev-parse -q --verify loom/T-1": {code: 1},
	}
	_, err := Check(&fakeGit{t: t, replies: replies}, "/repo", "loom/T-1", "T-1")
	if err == nil {
		t.Fatal("missing local branch was reported as a verdict; want an error")
	}
	if !strings.Contains(err.Error(), "no local branch") {
		t.Errorf("error %q does not name the missing local branch", err)
	}
}

func TestRevisionScanSortsNumerically(t *testing.T) {
	replies := baseReplies()
	replies["rev-parse -q --verify origin/loom/T-1"] = reply{stdout: localSHA + "\n"} // identical; the scan runs anyway
	// Deliberately out of order, and lexically misleading: "-r10" sorts before
	// "-r2" as a string.
	replies["for-each-ref --format=%(refname:short) refs/remotes/origin/loom/T-1-r*"] = reply{
		stdout: "origin/loom/T-1-r10\norigin/loom/T-1-r2\n",
	}

	got, err := Check(&fakeGit{t: t, replies: replies}, "/repo", "loom/T-1", "T-1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := []string{"loom/T-1-r2", "loom/T-1-r10"}; !equal(got.Revisions, want) {
		t.Errorf("Revisions = %v, want %v (ascending, numeric)", got.Revisions, want)
	}
	if got.NextRevision != "loom/T-1-r11" {
		t.Errorf("NextRevision = %q, want loom/T-1-r11", got.NextRevision)
	}
}

func TestRevisionScanIgnoresLookalikes(t *testing.T) {
	replies := baseReplies()
	replies["rev-parse -q --verify origin/loom/T-1"] = reply{code: 1} // unpublished; the scan still runs
	replies["for-each-ref --format=%(refname:short) refs/remotes/origin/loom/T-1-r*"] = reply{
		stdout: "origin/loom/T-1-rc1\norigin/loom/T-1-r2x\norigin/loom/T-1-review\n",
	}

	got, err := Check(&fakeGit{t: t, replies: replies}, "/repo", "loom/T-1", "T-1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(got.Revisions) != 0 {
		t.Errorf("Revisions = %v, want none: -rc1, -r2x and -review are not revisions", got.Revisions)
	}
	// r1 is the unsuffixed ref, so the first republish is r2.
	if got.NextRevision != "loom/T-1-r2" {
		t.Errorf("NextRevision = %q, want loom/T-1-r2", got.NextRevision)
	}
	if got.Verdict != VerdictUnpublished {
		t.Errorf("verdict = %q, want unpublished", got.Verdict)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── real-git end-to-end ─────────────────────────────────────────────────────
//
// A fake-only suite proves the switch statement, not that the git invocations
// are right. This reproduces the shape of the real occurrence: publish a
// branch, re-cut it from a different base, and check what the integrator sees.

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

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, name, body, msg string) string {
	t.Helper()
	writeFile(t, dir, name, body)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", msg)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// newUpstreamAndClone builds an "origin" repo and a clone of it, by path. No
// network is involved at any point.
func newUpstreamAndClone(t *testing.T) (upstream, clone string) {
	t.Helper()
	root := t.TempDir()
	upstream = filepath.Join(root, "upstream")
	if err := os.Mkdir(upstream, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, upstream, "init", "-b", "main")
	commit(t, upstream, "shared.txt", "base\n", "base")
	// A non-bare upstream refuses a push to its checked-out branch; the tests
	// below only ever push to other branches, but make that explicit.
	runGit(t, upstream, "config", "receive.denyCurrentBranch", "ignore")

	clone = filepath.Join(root, "clone")
	runGit(t, root, "clone", upstream, clone)
	return upstream, clone
}

func TestEndToEndDivergedAgainstRealGit(t *testing.T) {
	_, clone := newUpstreamAndClone(t)

	// Publish loom/T-9 with two commits of its own.
	runGit(t, clone, "checkout", "-b", "loom/T-9")
	commit(t, clone, "a.txt", "a\n", "published one")
	published := commit(t, clone, "b.txt", "b\n", "published two")
	runGit(t, clone, "push", "origin", "loom/T-9")

	// The upstream trunk moves on, and the coder re-cuts the branch from the
	// new trunk tip — exactly the real failure. The old published head is now
	// unreachable from the new local branch.
	runGit(t, clone, "checkout", "main")
	newBase := commit(t, clone, "trunk.txt", "moved\n", "trunk moves")
	runGit(t, clone, "checkout", "-B", "loom/T-9", newBase)
	commit(t, clone, "a.txt", "a re-cut\n", "recut one")
	commit(t, clone, "c.txt", "c\n", "recut two")
	local := commit(t, clone, "d.txt", "d\n", "recut three")

	got, err := Check(NewGitRunner(), clone, "loom/T-9", "T-9")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Verdict != VerdictDiverged {
		t.Fatalf("verdict = %q, want diverged (detail: %s)", got.Verdict, got.Detail)
	}
	if got.LocalSHA != local {
		t.Errorf("LocalSHA = %s, want %s", got.LocalSHA, local)
	}
	if got.PublishedSHA != published {
		t.Errorf("PublishedSHA = %s, want %s", got.PublishedSHA, published)
	}
	if got.RemoteRef != "origin/loom/T-9" {
		t.Errorf("RemoteRef = %q, want origin/loom/T-9", got.RemoteRef)
	}
	// The merge base is the trunk commit both sides share: the original base
	// commit, since the published side branched before the trunk moved.
	mergeBase := runGit(t, clone, "merge-base", published, local)
	if !strings.Contains(got.Detail, mergeBase[:12]) {
		t.Errorf("Detail %q does not name merge base %s", got.Detail, mergeBase)
	}
	// 2 commits published past the base; 4 local — the trunk commit the branch
	// was re-cut onto counts as one of them.
	if !strings.Contains(got.Detail, "2 commit(s) published") || !strings.Contains(got.Detail, "4 local") {
		t.Errorf("Detail %q does not carry the right ahead/behind counts", got.Detail)
	}
	if got.NextRevision != "loom/T-9-r2" {
		t.Errorf("NextRevision = %q, want loom/T-9-r2", got.NextRevision)
	}
	if got.Fetched {
		t.Error("Fetched must be false")
	}
}

func TestEndToEndFastForwardAndUnpublished(t *testing.T) {
	_, clone := newUpstreamAndClone(t)

	// Never published: the branch exists only locally.
	runGit(t, clone, "checkout", "-b", "loom/T-10")
	commit(t, clone, "a.txt", "a\n", "one")
	got, err := Check(NewGitRunner(), clone, "loom/T-10", "T-10")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Verdict != VerdictUnpublished {
		t.Fatalf("verdict = %q, want unpublished", got.Verdict)
	}

	// Publish it: identical.
	runGit(t, clone, "push", "origin", "loom/T-10")
	if got, err = Check(NewGitRunner(), clone, "loom/T-10", "T-10"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Verdict != VerdictIdentical {
		t.Fatalf("verdict = %q, want identical", got.Verdict)
	}

	// One more local commit on top: fast-forward.
	commit(t, clone, "b.txt", "b\n", "two")
	if got, err = Check(NewGitRunner(), clone, "loom/T-10", "T-10"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Verdict != VerdictFastForward {
		t.Fatalf("verdict = %q, want fast-forward", got.Verdict)
	}

	// Publish that, then rewind the local branch behind the published head.
	runGit(t, clone, "push", "origin", "loom/T-10")
	runGit(t, clone, "reset", "--hard", "HEAD~1")
	if got, err = Check(NewGitRunner(), clone, "loom/T-10", "T-10"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Verdict != VerdictBehind {
		t.Fatalf("verdict = %q, want behind", got.Verdict)
	}
}
