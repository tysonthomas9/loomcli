package retention

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/pool"
)

type fakeAbandonment bool

func (f fakeAbandonment) AbandonedForRetention(context.Context, string, string) (bool, error) {
	return bool(f), nil
}

func TestSweepWithoutAbandonStateKeepsCopy(t *testing.T) {
	ctx := context.Background()
	store, err := journal.OpenSQLite(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	row := journal.RetainedCopy{Workspace: "W", Change: "C", Attempt: "A", Path: "/missing/copy", SourceRepo: "/missing/source", Complete: true}
	if err := store.RecordRetainedCopy(ctx, row); err != nil {
		t.Fatal(err)
	}
	revision, err := store.ReserveRevision(ctx, loomgit.Revision{Workspace: "W", Change: "C", RequestID: "driver:A",
		Kind: "source", Outcome: "completed", BaseSHA: "base", TreeHash: "tree", SourceHeadSHA: "capture"})
	if err != nil {
		t.Fatal(err)
	}
	revision.HeadSHA = "capture"
	if err := store.FinishRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	known, err := store.AbandonedForRetention(ctx, "W", "C")
	if err != nil || known {
		t.Fatalf("missing abandonment table: %t, %v", known, err)
	}
	sweep := Sweep{Store: store, Now: func() time.Time { return time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC) }}
	results, err := sweep.Run(ctx, true)
	if err != nil || len(results) != 1 || results[0].Reason != "change is not landed or abandoned" {
		t.Fatalf("no abandon state: %+v, %v", results, err)
	}
	sweep.Abandoned = fakeAbandonment(true)
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Reason != "retention starts when terminal state is observed" {
		t.Fatalf("fake abandon state: %+v, %v", results, err)
	}
}

func TestCloneRefsCapturedRejectsExtraLocalCommit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, copyPath := filepath.Join(root, "source"), filepath.Join(root, "copy")
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}}
	rootRunner, err := gitexec.New(root, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rootRunner.Run(ctx, "init", source); err != nil {
		t.Fatal(err)
	}
	sourceRunner, err := gitexec.New(source, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceRunner.Run(ctx, "commit", "--allow-empty", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	baseBytes, err := sourceRunner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(baseBytes))
	if _, err := rootRunner.Run(ctx, "clone", "--local", source, copyPath); err != nil {
		t.Fatal(err)
	}
	copyRunner, err := gitexec.New(copyPath, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copyRunner.Run(ctx, "checkout", "-b", "extra"); err != nil {
		t.Fatal(err)
	}
	if _, err := copyRunner.Run(ctx, "commit", "--allow-empty", "-m", "uncaptured"); err != nil {
		t.Fatal(err)
	}
	if _, err := copyRunner.Run(ctx, "checkout", "--detach", base); err != nil {
		t.Fatal(err)
	}
	if err := checkCopyContent(ctx, copyRunner, base); err != nil {
		t.Fatal(err)
	}
	if err := cloneRefsCaptured(ctx, sourceRunner, copyRunner, base); err == nil {
		t.Fatal("uncaptured local branch was accepted")
	}
}

func TestSweepRemovesFullyFrozenClone(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, copyPath, journalPath := filepath.Join(root, "source"), filepath.Join(root, "A"), filepath.Join(root, "store.db")
	options := gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Test", Email: "test@example.com"}}
	rootRunner, err := gitexec.New(root, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rootRunner.Run(ctx, "init", source); err != nil {
		t.Fatal(err)
	}
	sourceRunner, err := gitexec.New(source, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceRunner.Run(ctx, "commit", "--allow-empty", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	baseBytes, err := sourceRunner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(baseBytes))
	if _, err := rootRunner.Run(ctx, "clone", "--local", source, copyPath); err != nil {
		t.Fatal(err)
	}
	copyRunner, err := gitexec.New(copyPath, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copyRunner.Run(ctx, "commit", "--allow-empty", "-m", "agent work"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "untracked.txt"), []byte("captured"), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, err := agentcapture.Capture(ctx, copyPath, "W", "A", "T", "task")
	if err != nil || !captured.Complete {
		t.Fatalf("capture: %+v, %v", captured, err)
	}
	revision, err := driverfreeze.FreezeCaptureAt(ctx, journalPath, driverfreeze.CaptureRequest{
		Workspace: "W", Task: "T", Repo: "source", Attempt: "A", Worktree: copyPath,
		Base: base, CaptureSHA: captured.SHA, Outcome: "cancelled", Complete: true, SourceRepo: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.HeadSHA == captured.SHA {
		t.Fatal("freeze did not create the rewritten revision head")
	}
	if err := os.Remove(filepath.Join(copyPath, "untracked.txt")); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.MarkLanded(ctx, "W", revision.Change); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	sweep := Sweep{Store: store, Now: func() time.Time { return start }}
	if _, err := sweep.Run(ctx, true); err != nil {
		t.Fatal(err)
	}
	sweep.Now = func() time.Time { return start.Add(7 * 24 * time.Hour) }
	if _, err := copyRunner.Run(ctx, "commit", "--allow-empty", "-m", "uncaptured tag work"); err != nil {
		t.Fatal(err)
	}
	if _, err := copyRunner.Run(ctx, "tag", "uncaptured"); err != nil {
		t.Fatal(err)
	}
	if _, err := copyRunner.Run(ctx, "reset", "--hard", base); err != nil {
		t.Fatal(err)
	}
	results, err := sweep.Run(ctx, true)
	if err != nil || len(results) != 1 || results[0].Action != "keep" {
		t.Fatalf("uncaptured tag must retain clone: %+v, %v", results, err)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("clone with uncaptured tag removed: %v", err)
	}
	if _, err := copyRunner.Run(ctx, "tag", "-d", "uncaptured"); err != nil {
		t.Fatal(err)
	}
	leasePath := filepath.Join(copyPath, ".agent.lock")
	if err := os.WriteFile(leasePath, []byte("{\"pid\":1}"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || len(results) != 1 || results[0].Action != "keep" {
		t.Fatalf("leased clone must remain: %+v, %v", results, err)
	}
	if err := os.Remove(leasePath); err != nil {
		t.Fatal(err)
	}
	copyRepo, err := pool.New(store).Admit(ctx, copyPath)
	if err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- copyRepo.WithLock(ctx, func(context.Context) error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	blockedCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = removeWithLocks(blockedCtx, store, journal.RetainedCopy{
		Workspace: "W", Attempt: "A", Path: copyPath, SourceRepo: source,
	}, sourceRunner, copyRunner, captured.SHA, false)
	cancel()
	if err == nil {
		t.Fatal("repo lease did not block clone removal")
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("repo-leased clone removed: %v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || len(results) != 1 || results[0].Action != "remove" {
		t.Fatalf("expired clone: %+v, %v", results, err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("clone remains: %v", err)
	}
}

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
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, "commit", "-m", "base"); err != nil {
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
	copyRunner, err := gitexec.New(copyPath, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copyRunner.Run(ctx, "commit", "--allow-empty", "-m", "new uncaptured commit"); err != nil {
		t.Fatal(err)
	}
	uncapturedBytes, err := copyRunner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	uncaptured := strings.TrimSpace(string(uncapturedBytes))
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "keep" || !strings.Contains(results[0].Reason, "HEAD is outside") {
		t.Fatalf("clean uncaptured commit: %+v, %v", results, err)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("copy with new commit removed: %v", err)
	}
	if out, err := copyRunner.Run(ctx, "cat-file", "-t", uncaptured); err != nil || strings.TrimSpace(string(out)) != "commit" {
		t.Fatalf("new commit lost: %q, %v", out, err)
	}
	if _, err := copyRunner.Run(ctx, "checkout", "--detach", head); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "tracked.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "keep" {
		t.Fatalf("dirty tracked path: %+v, %v", results, err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "tracked.txt"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "info", "exclude"), []byte("ignored.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err = sweep.Run(ctx, true)
	if err != nil || results[0].Action != "keep" {
		t.Fatalf("ignored path: %+v, %v", results, err)
	}
	if err := os.Remove(filepath.Join(copyPath, "ignored.txt")); err != nil {
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
