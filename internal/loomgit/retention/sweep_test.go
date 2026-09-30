package retention

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

func TestSweepLandedCopyUsesCaptureAndWorkspaceWindow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	copyPath := filepath.Join(root, "attempt-1")
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}}
	rootRunner, err := gitexec.New(root, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rootRunner.Run(ctx, "init", source); err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(source, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "commit", "--allow-empty", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	headBytes, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	treeBytes, err := runner.Run(ctx, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "worktree", "add", "--detach", copyPath, head); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ref, err := refname.AttemptCapture("W", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.UpdateRef(ctx, ref, head, strings.Repeat("0", len(head))); err != nil {
		t.Fatal(err)
	}
	revision, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "driver:attempt-1",
		Kind: "source", Outcome: "completed", BaseSHA: head, TreeHash: strings.TrimSpace(string(treeBytes)), SourceHeadSHA: head})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = head
	if err := store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRetainedCopy(ctx, journal.RetainedCopy{Workspace: "W", Change: "C", Attempt: "attempt-1",
		Path: copyPath, SourceRepo: source, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkLanded(ctx, "W", "C"); err != nil {
		t.Fatal(err)
	}
	policy := journal.DefaultRetentionPolicy()
	policy.Landed = 2 * 24 * time.Hour
	if err := store.SetRetentionPolicy(ctx, "W", policy); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	sweep := Sweep{Store: store, Now: func() time.Time { return start }}
	results, err := sweep.Run(ctx, true)
	if err != nil || len(results) != 1 || results[0].Action != "keep" {
		t.Fatalf("observe: %+v, %v", results, err)
	}
	sweep.Now = func() time.Time { return start.Add(48*time.Hour - time.Second) }
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "keep" {
		t.Fatalf("before expiry: %+v, %v", results, err)
	}
	sweep.Now = func() time.Time { return start.Add(48 * time.Hour) }
	if _, err := runner.Run(ctx, "commit", "--allow-empty", "-m", "other"); err != nil {
		t.Fatal(err)
	}
	otherBytes, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	other := strings.TrimSpace(string(otherBytes))
	if err := runner.UpdateRef(ctx, ref, other, head); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "keep" {
		t.Fatalf("changed capture ref: %+v, %v", results, err)
	}
	if err := runner.UpdateRef(ctx, ref, head, other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "uncaptured.txt"), []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "keep" {
		t.Fatalf("uncaptured work: %+v, %v", results, err)
	}
	if err := os.Remove(filepath.Join(copyPath, "uncaptured.txt")); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, false)
	if err != nil || results[0].Action != "remove" {
		t.Fatalf("dry run: %+v, %v", results, err)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("dry run removed copy: %v", err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "remove" {
		t.Fatalf("apply: %+v, %v", results, err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("copy remains: %v", err)
	}
	sweep.Now = func() time.Time { return start.Add(90 * 24 * time.Hour) }
	results, err = sweep.Run(ctx, true)
	if err != nil || len(results) != 2 || results[1].Action != "delete capture ref" {
		t.Fatalf("capture expiry: %+v, %v", results, err)
	}
	if _, err := runner.Run(ctx, "show-ref", "--verify", ref); err == nil {
		t.Fatal("capture ref remains")
	}
}

func TestSweepDeletesExpiredWorkspaceProviderPrefix(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, remote := filepath.Join(root, "source"), filepath.Join(root, "remote.git")
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}}
	rootRunner, err := gitexec.New(root, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{source, remote} {
		if _, err := rootRunner.Run(ctx, "init", target); err != nil {
			t.Fatal(err)
		}
	}
	runner, err := gitexec.New(source, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "commit", "--allow-empty", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	headBytes, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headBytes))
	ref, err := refname.AttemptCapture("W", "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.UpdateRef(ctx, ref, head, strings.Repeat("0", len(head))); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "push", remote, head+":"+ref); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureMirrorSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMirrorRecord(ctx, journal.MirrorRecord{Repo: source, Ref: ref, Remote: remote, SHA: head, State: "mirrored"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishWorkspaceDeletion(ctx, "W"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(91 * 24 * time.Hour)
	sweep := Sweep{Store: store, Now: func() time.Time { return now }}
	results, err := sweep.Run(ctx, false)
	if err != nil || len(results) != 1 || results[0].Action != "delete provider refs" {
		t.Fatalf("dry run: %+v, %v", results, err)
	}
	if _, err := runner.Run(ctx, "show-ref", "--verify", ref); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "commit", "--allow-empty", "-m", "other"); err != nil {
		t.Fatal(err)
	}
	otherBytes, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	other := strings.TrimSpace(string(otherBytes))
	if _, err := runner.Run(ctx, "push", "--force-with-lease="+ref+":"+head, remote, other+":"+ref); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "keep" {
		t.Fatalf("changed provider ref: %+v, %v", results, err)
	}
	if _, err := runner.Run(ctx, "push", "--force-with-lease="+ref+":"+other, remote, head+":"+ref); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "delete provider refs" {
		t.Fatalf("apply: %+v, %v", results, err)
	}
	listed, err := runner.Run(ctx, "ls-remote", remote, ref)
	if err != nil || len(listed) != 0 {
		t.Fatalf("provider ref remains: %q, %v", listed, err)
	}
	tombstones, err := store.WorkspaceTombstones(ctx)
	if err != nil || len(tombstones) != 0 {
		t.Fatalf("tombstone remains: %+v, %v", tombstones, err)
	}
}
