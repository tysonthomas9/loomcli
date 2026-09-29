package mirror

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:norawexec // Real temporary Git repositories are this test's subject.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func fixture(t *testing.T) (string, string, string, *journal.SQLite) {
	t.Helper()
	root := t.TempDir()
	repo, remote := filepath.Join(root, "repo"), filepath.Join(root, "provider.git")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.name", "Mirror Test")
	git(t, repo, "config", "user.email", "mirror@example.test")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "readme"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".gitignore", "readme")
	git(t, repo, "commit", "-qm", "base")
	base := git(t, repo, "rev-parse", "HEAD")
	git(t, root, "init", "-q", "--bare", remote)
	git(t, repo, "remote", "add", "origin", remote)
	store, err := journal.OpenSQLite(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureMirrorSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return repo, remote, base, store
}

func TestCaptureMirrorAndLeasedDeletion(t *testing.T) {
	ctx := context.Background()
	repo, remote, base, store := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("LOCAL_SECRET=fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "readme"), []byte("edited\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := capture.Capture(ctx, runner, repo, capture.Params{Workspace: "W", Attempt: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if result.CaptureSHA == "" {
		t.Fatal("capture has no commit")
	}
	revision, err := refname.RevisionHead("W", "C", "1")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := refname.InteractiveBackup("W", "lead")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{revision, backup} {
		git(t, repo, "update-ref", ref, base)
	}
	if err := SyncRepo(ctx, store, repo, base); err != nil {
		t.Fatal(err)
	}
	if got := git(t, remote, "rev-parse", result.CaptureRef); got != result.CaptureSHA {
		t.Fatalf("remote %s != capture %s", got, result.CaptureSHA)
	}
	for _, ref := range []string{revision, backup} {
		if got := git(t, remote, "rev-parse", ref); got != base {
			t.Fatalf("remote %s = %s", ref, got)
		}
	}
	if got := git(t, remote, "ls-tree", "-r", "--name-only", result.CaptureRef); strings.Contains(got, ".env") {
		t.Fatalf("ignored secret entered provider tree: %s", got)
	}
	row, found, err := store.MirrorState(ctx, repo, result.CaptureRef)
	if err != nil || !found || row.State != "mirrored" {
		t.Fatalf("mirror state: %+v %v %v", row, found, err)
	}
	if out := git(t, remote, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/tags"); out != "" {
		t.Fatalf("unexpected public refs: %s", out)
	}
	git(t, repo, "update-ref", "-d", result.CaptureRef)
	if err := SyncRepo(ctx, store, repo, base); err != nil {
		t.Fatal(err)
	}
	if out := git(t, remote, "for-each-ref", "--format=%(refname)", result.CaptureRef); out != "" {
		t.Fatalf("remote ref retained: %s", out)
	}
}

func TestSecretPathAndRemoteMoveRefused(t *testing.T) {
	ctx := context.Background()
	repo, remote, base, store := fixture(t)
	ref, err := refname.WIP("W", "lead", "A")
	if err != nil {
		t.Fatal(err)
	}
	git(t, repo, "update-ref", ref, base)
	if err := SyncRepo(ctx, store, repo, base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "id_rsa"), []byte("fixture key"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "id_rsa")
	git(t, repo, "commit", "-qm", "new secret")
	secretSHA := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "update-ref", ref, secretSHA)
	if err := SyncRepo(ctx, store, repo, base); err == nil {
		t.Fatal("secret ref mirrored")
	}
	row, _, _ := store.MirrorState(ctx, repo, ref)
	if row.State != "not_mirrored" || !strings.Contains(row.Reason, "id_rsa") {
		t.Fatalf("secret state: %+v", row)
	}
	if got := git(t, remote, "rev-parse", ref); got != base {
		t.Fatal("remote moved after secret refusal")
	}
	git(t, repo, "push", "origin", secretSHA+":"+ref)
	git(t, repo, "update-ref", ref, base)
	if err := SyncRepo(ctx, store, repo, base); err == nil {
		t.Fatal("remote drift was overwritten")
	}
	if got := git(t, remote, "rev-parse", ref); got != secretSHA {
		t.Fatal("remote drift was overwritten")
	}
}

func TestProviderRejectionRetainsLocalRefAndRetries(t *testing.T) {
	ctx := context.Background()
	repo, remote, base, store := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "readme"), []byte("new capture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := capture.Capture(ctx, runner, repo, capture.Params{Workspace: "W", Attempt: "A"})
	if err != nil {
		t.Fatal(err)
	}
	ref := result.CaptureRef
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho provider rejected >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := SyncRepo(ctx, store, repo, base); err == nil {
		t.Fatal("provider rejection was ignored")
	}
	row, found, err := store.MirrorState(ctx, repo, ref)
	if err != nil || !found || row.State != "not_mirrored" || !strings.Contains(row.Reason, "provider rejected") {
		t.Fatalf("rejected state: %+v %v %v", row, found, err)
	}
	if got := git(t, repo, "rev-parse", ref); got != result.CaptureSHA {
		t.Fatal("local ref lost after rejection")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if err := SyncRepo(ctx, store, repo, base); err != nil {
		t.Fatal(err)
	}
	row, _, _ = store.MirrorState(ctx, repo, ref)
	if row.State != "mirrored" || row.SHA != result.CaptureSHA {
		t.Fatalf("retry state: %+v", row)
	}
}

func TestTrackedSecretPatternInBaseIsAllowed(t *testing.T) {
	ctx := context.Background()
	repo, remote, workspaceBase, store := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, ".env.example"), []byte("EXAMPLE=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-f", ".env.example")
	git(t, repo, "commit", "-qm", "tracked example")
	if err := os.WriteFile(filepath.Join(repo, "readme"), []byte("attempt edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := capture.Capture(ctx, runner, repo, capture.Params{Workspace: "W", Attempt: "A"})
	if err != nil {
		t.Fatal(err)
	}
	revisionBase, err := refname.RevisionBase("W", "C", "1")
	if err != nil {
		t.Fatal(err)
	}
	revisionHead, err := refname.RevisionHead("W", "C", "1")
	if err != nil {
		t.Fatal(err)
	}
	git(t, repo, "update-ref", revisionBase, git(t, repo, "rev-parse", "HEAD"))
	git(t, repo, "update-ref", revisionHead, result.CaptureSHA)
	if err := SyncRepo(ctx, store, repo, workspaceBase); err != nil {
		t.Fatal(err)
	}
	if got := git(t, remote, "rev-parse", result.CaptureRef); got != result.CaptureSHA {
		t.Fatal("tracked base path was refused")
	}
	if got := git(t, remote, "rev-parse", revisionHead); got != result.CaptureSHA {
		t.Fatal("revision baseline path was refused")
	}
}

func TestUnavailableProviderDoesNotBlockLocalCaptureAndRetries(t *testing.T) {
	ctx := context.Background()
	repo, remote, base, store := fixture(t)
	if err := os.WriteFile(filepath.Join(repo, "readme"), []byte("offline edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := capture.Capture(ctx, runner, repo, capture.Params{Workspace: "W", Attempt: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	if result.CaptureSHA == "" {
		t.Fatal("local capture did not finish")
	}
	git(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "unavailable.git"))
	if err := SyncRepo(ctx, store, repo, base); err == nil {
		t.Fatal("unavailable provider did not fail")
	}
	row, _, err := store.MirrorState(ctx, repo, result.CaptureRef)
	if err != nil || row.State != "not_mirrored" || row.SHA != "" {
		t.Fatalf("offline state: %+v %v", row, err)
	}
	if got := git(t, repo, "rev-parse", result.CaptureRef); got != result.CaptureSHA {
		t.Fatal("local capture lost while provider was unavailable")
	}
	git(t, repo, "remote", "set-url", "origin", remote)
	if err := SyncRepo(ctx, store, repo, base); err != nil {
		t.Fatal(err)
	}
	if got := git(t, remote, "rev-parse", result.CaptureRef); got != result.CaptureSHA {
		t.Fatal("retry did not mirror capture")
	}
}
