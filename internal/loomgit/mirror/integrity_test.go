package mirror

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestRemoteIntegrityReportsDeletedMirroredRef(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	remote := filepath.Join(root, "remote.git")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		command := exec.Command("git", args...) //nolint:norawexec // Real local Git provider acceptance test.
		command.Dir = dir
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git(repo, "init", "-q")
	git(repo, "config", "user.name", "Test")
	git(repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "file")
	git(repo, "commit", "-qm", "initial")
	sha := git(repo, "rev-parse", "HEAD")
	git(root, "init", "--bare", "-q", remote)
	ref := "refs/loom/ws/W1/change/C1/1/head"
	git(repo, "push", "-q", remote, "HEAD:"+ref)
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.EnsureMirrorSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMirrorRecord(ctx, journal.MirrorRecord{Repo: repo, Ref: ref, Remote: remote, SHA: sha, State: "mirrored"}); err != nil {
		t.Fatal(err)
	}
	issues, err := RemoteIntegrity(ctx)
	if err != nil || len(issues) != 0 {
		t.Fatalf("healthy remote: %+v %v", issues, err)
	}
	git(remote, "update-ref", "-d", ref)
	issues, err = RemoteIntegrity(ctx)
	if err != nil || len(issues) != 1 || issues[0].State != "integrity_missing" || issues[0].SHA != sha {
		t.Fatalf("deleted remote: %+v %v", issues, err)
	}
}
