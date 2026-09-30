package replay

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/gitversion"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

func fixture(t *testing.T) (string, *gitexec.Runner, string) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil { //nolint:norawexec // Temporary real-Git fixture.
		t.Fatalf("git init: %s: %v", out, err)
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := gitexec.New(dir, gitexec.Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a", "base\n")
	must(t, r, "add", "a")
	must(t, r, "commit", "-qm", "base")
	return dir, r, must(t, r, "rev-parse", "HEAD")
}

func must(t *testing.T, r *gitexec.Runner, args ...string) string {
	t.Helper()
	out, err := r.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, path, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir string, r *gitexec.Runner, path, contents, message string) string {
	t.Helper()
	write(t, dir, path, contents)
	must(t, r, "add", path)
	must(t, r, "commit", "-qm", message)
	return must(t, r, "rev-parse", "HEAD")
}

func TestTrialMergeCleanAndConflictLeaveCheckoutUnchanged(t *testing.T) {
	dir, r, base := fixture(t)
	source := commit(t, dir, r, "a", "source\n", "source")
	must(t, r, "checkout", "-q", "-b", "work", base)
	target := commit(t, dir, r, "a", "target\n", "target")
	beforeRefs := must(t, r, "for-each-ref", "--format=%(refname) %(objectname)")
	beforeIndex := must(t, r, "ls-files", "--stage")
	got, err := New(r).TrialMerge(context.Background(), base, source, target)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConflictCommit != source || !reflect.DeepEqual(got.ConflictingPaths, []string{"a"}) {
		t.Fatalf("conflict: %+v", got)
	}
	if must(t, r, "rev-parse", "HEAD") != target || must(t, r, "for-each-ref", "--format=%(refname) %(objectname)") != beforeRefs || must(t, r, "ls-files", "--stage") != beforeIndex || must(t, r, "status", "--porcelain") != "" {
		t.Fatal("trial merge changed checkout or refs")
	}
	data, err := os.ReadFile(filepath.Join(dir, "a"))
	if err != nil || string(data) != "target\n" {
		t.Fatalf("worktree changed: %q, %v", data, err)
	}
	must(t, r, "checkout", "-q", "-b", "clean", base)
	cleanTarget := commit(t, dir, r, "b", "other\n", "other")
	got, err = New(r).TrialMerge(context.Background(), base, source, cleanTarget)
	if err != nil || got.ConflictCommit != "" || got.TreeSHA == "" || got.HeadSHA == cleanTarget {
		t.Fatalf("clean: %+v, %v", got, err)
	}
	if must(t, r, "rev-parse", got.HeadSHA+"^{tree}") != got.TreeSHA || must(t, r, "rev-parse", "HEAD") != cleanTarget {
		t.Fatal("clean result tree or HEAD")
	}
}

func TestTrialMergeMultiCommitAndDrop(t *testing.T) {
	dir, r, base := fixture(t)
	first := commit(t, dir, r, "x", "x\n", "first")
	second := commit(t, dir, r, "y", "y\n", "second")
	must(t, r, "checkout", "-q", "-b", "work", base)
	target := commit(t, dir, r, "y", "y\n", "already present")
	got, err := New(r).TrialMerge(context.Background(), base, second, target)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConflictCommit != "" || !reflect.DeepEqual(got.DroppedCommits, []string{second}) || got.TreeSHA == "" {
		t.Fatalf("drop: %+v", got)
	}
	if must(t, r, "show", got.HeadSHA+":x") != "x" || must(t, r, "rev-list", "--count", target+".."+got.HeadSHA) != "1" {
		t.Fatal("first commit was not replayed")
	}
	if must(t, r, "show", "-s", "--format=%s", got.HeadSHA) != "first" || first == got.HeadSHA {
		t.Fatal("replayed commit metadata or parent wrong")
	}
}

func TestTrialMergeStopsAtFirstConflictingCommit(t *testing.T) {
	dir, r, base := fixture(t)
	first := commit(t, dir, r, "x", "x\n", "first")
	second := commit(t, dir, r, "a", "source\n", "second")
	must(t, r, "checkout", "-q", "-b", "work", base)
	target := commit(t, dir, r, "a", "target\n", "target")
	got, err := New(r).TrialMerge(context.Background(), base, second, target)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConflictCommit != second || !reflect.DeepEqual(got.ConflictingPaths, []string{"a"}) || got.HeadSHA == target || got.TreeSHA != "" {
		t.Fatalf("first conflict: %+v", got)
	}
	if must(t, r, "show", "-s", "--format=%s", got.HeadSHA) != "first" || first == got.HeadSHA {
		t.Fatal("first commit was not replayed before conflict")
	}
	if must(t, r, "rev-parse", "HEAD") != target || must(t, r, "status", "--porcelain") != "" {
		t.Fatal("conflict changed checkout")
	}
}

func TestTrialMergeUsesFirstParentOfMergeCommit(t *testing.T) {
	dir, r, base := fixture(t)
	must(t, r, "checkout", "-q", "-b", "side", base)
	commit(t, dir, r, "side-file", "side\n", "side")
	must(t, r, "checkout", "-q", "-b", "source", base)
	commit(t, dir, r, "source-file", "source\n", "source")
	must(t, r, "merge", "--no-ff", "-qm", "merge side", "side")
	head := must(t, r, "rev-parse", "HEAD")
	must(t, r, "checkout", "-q", "-b", "target", base)
	target := commit(t, dir, r, "target-file", "target\n", "target")
	got, err := New(r).TrialMerge(context.Background(), base, head, target)
	if err != nil || got.ConflictCommit != "" || got.TreeSHA == "" {
		t.Fatalf("merge commit: %+v, %v", got, err)
	}
	if must(t, r, "show", got.HeadSHA+":side-file") != "side" || must(t, r, "show", got.HeadSHA+":source-file") != "source" {
		t.Fatal("merged tree was not replayed")
	}
	if must(t, r, "rev-list", "--count", target+".."+got.HeadSHA) != "2" {
		t.Fatal("side parent was replayed separately")
	}
}

func TestTrialMergePreservesAuthorMessageAndTrailers(t *testing.T) {
	dir, r, base := fixture(t)
	write(t, dir, "source-file", "source\n")
	must(t, r, "add", "source-file")
	message := "source message\n\nLoom-Task: P2.3"
	_, err := r.RunWithEnv(context.Background(), map[string]string{"GIT_AUTHOR_NAME": "Original Author", "GIT_AUTHOR_EMAIL": "original@example.test", "GIT_AUTHOR_DATE": "2024-02-03T04:05:06+00:00"}, "commit", "-qm", message)
	if err != nil {
		t.Fatal(err)
	}
	source := must(t, r, "rev-parse", "HEAD")
	must(t, r, "checkout", "-q", "-b", "target", base)
	target := commit(t, dir, r, "target-file", "target\n", "target")
	got, err := New(r).TrialMerge(context.Background(), base, source, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%an", "%ae", "%aI", "%B"} {
		if a, b := must(t, r, "show", "-s", "--format="+format, source), must(t, r, "show", "-s", "--format="+format, got.HeadSHA); a != b {
			t.Errorf("%s differs: %q vs %q", format, a, b)
		}
	}
}

func TestVersionRequirement(t *testing.T) {
	for input, want := range map[string]bool{"git version 2.39.9": false, "git version 2.40.4": true, "git version 2.56.0": true, "unknown": false} {
		if got := gitversion.Check(input) == nil; got != want {
			t.Errorf("%q: %t", input, got)
		}
	}
	_, r, base := fixture(t)
	fake := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nfor arg do if [ \"$arg\" = version ]; then echo 'git version 2.39.9'; exit 0; fi; done\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := New(r).TrialMerge(context.Background(), base, base, base)
	if err == nil || !strings.Contains(err.Error(), "2.40") {
		t.Fatalf("version error: %v", err)
	}
}

func TestTrialMergeProductionDefaults(t *testing.T) {
	dir := os.Getenv("LOOMGIT_REPLAY_REAL_REPO")
	if dir == "" {
		dir, _, _ = fixture(t)
	}
	config := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(config, []byte("[user]\nname = Test\nemail = test@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	r, err := gitexec.New(dir, gitexec.Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := must(t, r, "rev-parse", "HEAD")
	path := "trial-merge-real-repo.txt"
	if os.Getenv("LOOMGIT_REPLAY_REAL_REPO") == "" {
		path = "b"
	}
	source := commit(t, dir, r, path, "one-line edit\n", "source edit")
	must(t, r, "checkout", "-q", "-b", "trial-target", base)
	target := commit(t, dir, r, "trial-merge-target.txt", "other line\n", "target edit")
	got, err := New(r).TrialMerge(context.Background(), base, source, target)
	if err != nil || got.ConflictCommit != "" || got.TreeSHA == "" {
		t.Fatalf("production defaults: %+v, %v", got, err)
	}
	if must(t, r, "show", got.HeadSHA+":"+path) != "one-line edit" {
		t.Fatal("source edit missing from trial tree")
	}
	t.Logf("trial merge base=%s target=%s derived=%s", base, target, got.HeadSHA)
}
