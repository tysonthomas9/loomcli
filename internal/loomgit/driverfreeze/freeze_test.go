package driverfreeze_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestEpicPRRequestedHonorsDryRun(t *testing.T) {
	for _, tc := range []struct {
		payload string
		want    bool
	}{
		{`{"leadName":"L","openPullRequest":true}`, true},
		{`{"leadName":"L","stackedPullRequests":true}`, true},
		{`{"leadName":"L","openPullRequest":true,"dryRun":true}`, false},
		{`{"leadName":"L"}`, false},
	} {
		lead, requested := driverfreeze.EpicPRRequested(json.RawMessage(tc.payload))
		if lead != "L" || requested != tc.want {
			t.Fatalf("payload %s: lead=%q requested=%t", tc.payload, lead, requested)
		}
	}
}

func TestFreezeFlatDiffAndNextAttempt(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary Git repository validates revision objects and refs.
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.test")
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("base\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "--all")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	journalPath := filepath.Join(t.TempDir(), "journal.db")
	ctx := context.Background()
	var firstChange string
	for number, value := range []string{"first\n", "second\n"} {
		for _, name := range []string{"a", "b", "c"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		patch := git("diff", "--binary", base)
		git("restore", "--worktree", ".")
		outcome := []string{"completed", "timeout"}[number]
		in := driverfreeze.Request{Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: []string{"attempt-1", "attempt-2"}[number], Worktree: dir, Base: base, Patch: []byte(patch + "\n"), Outcome: outcome, AuthorKind: "agent", AuthorID: "worker"}
		rev, err := driverfreeze.FreezeAt(ctx, journalPath, in)
		if err != nil {
			t.Fatal(err)
		}
		again, err := driverfreeze.FreezeAt(ctx, journalPath, in)
		if err != nil || again.HeadSHA != rev.HeadSHA || again.Number != rev.Number {
			t.Fatalf("retry changed revision: %+v, %v", again, err)
		}
		if rev.Number != number+1 || !rev.Ready || rev.Change == "" {
			t.Fatalf("revision = %+v", rev)
		}
		if number == 0 {
			firstChange = rev.Change
		} else if rev.Change != firstChange {
			t.Fatalf("change changed: %s -> %s", firstChange, rev.Change)
		}
		if got := git("rev-list", "--count", base+".."+rev.HeadSHA); got != "1" {
			t.Fatalf("flat diff made %s commits", got)
		}
		message := git("show", "-s", "--format=%B", rev.HeadSHA)
		for _, trailer := range []string{"Loom-Task: TASK", "Loom-Attempt: " + in.Attempt, "Loom-Change-Id: " + rev.Change, "Loom-Revision: " + string(rune('1'+number))} {
			if !strings.Contains(message, trailer) {
				t.Fatalf("missing %q in %q", trailer, message)
			}
		}
		if got := git("status", "--porcelain"); got != "" {
			t.Fatalf("worktree changed: %s", got)
		}
		if got := git("rev-parse", "HEAD"); got != base {
			t.Fatalf("HEAD changed: %s", got)
		}
		store, err := journal.OpenSQLite(journalPath)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := store.GetRevision(ctx, "WS", rev.Change, rev.Number)
		authorKind, authorID, authorErr := store.RevisionAuthor(ctx, rev)
		_ = store.Close()
		if err != nil || stored.Outcome != outcome {
			t.Fatalf("stored revision = %+v, %v", stored, err)
		}
		if authorErr != nil || authorKind != "agent" || authorID != "worker" {
			t.Fatalf("revision author = %q/%q, %v", authorKind, authorID, authorErr)
		}
	}
}

func TestFreezeCommittedAndUncommittedWork(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository proves mixed commit and working-tree capture.
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.test")
	for _, name := range []string{"committed", "edited"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("base\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "--all")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "committed"), []byte("commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "committed")
	git("commit", "-qm", "agent commit")
	if err := os.WriteFile(filepath.Join(dir, "edited"), []byte("edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeHead, beforeStatus := git("rev-parse", "HEAD"), git("status", "--porcelain")
	patch := git("diff", "--binary", base) + "\n"
	journalPath := filepath.Join(t.TempDir(), "journal.db")
	rev, err := driverfreeze.FreezeAt(context.Background(), journalPath, driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "mixed", Worktree: dir,
		Base: base, Patch: []byte(patch), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if git("rev-parse", "HEAD") != beforeHead || git("status", "--porcelain") != beforeStatus {
		t.Fatal("runner worktree changed")
	}
	if got := git("rev-list", "--count", base+".."+rev.HeadSHA); got != "1" {
		t.Fatalf("flat diff made %s revision commits", got)
	}
	if got := git("diff-tree", "--no-commit-id", "--name-only", "-r", rev.HeadSHA); !strings.Contains(got, "committed") || !strings.Contains(got, "edited") {
		t.Fatalf("revision lost work: %s", got)
	}
	explicit, err := driverfreeze.FreezeAt(context.Background(), journalPath, driverfreeze.Request{
		Workspace: "WS", Task: "TASK", Repo: "repo", Attempt: "mixed-explicit", Worktree: dir,
		Base: base, CommitHeadSHA: beforeHead, Patch: []byte(patch), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Number != 2 || explicit.Change != rev.Change {
		t.Fatalf("explicit revision = %+v", explicit)
	}
	if got := git("rev-list", "--count", base+".."+explicit.HeadSHA); got != "2" {
		t.Fatalf("explicit commit chain has %s commits", got)
	}
	if messages := git("log", "--format=%B", base+".."+explicit.HeadSHA); !strings.Contains(messages, "agent commit") || strings.Count(messages, "Loom-Revision: 2") != 2 {
		t.Fatalf("explicit commit metadata lost: %s", messages)
	}
	if got := git("status", "--porcelain"); got != beforeStatus {
		t.Fatalf("worktree changed: %s", got)
	}
}
