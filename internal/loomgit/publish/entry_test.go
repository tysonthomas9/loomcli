package publish_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestReconcileJournalNativeRestackWaitsForProviderHead(t *testing.T) {
	repo, remote, head := nativeEntryFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/owner/repo/pulls/2" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"number":2,"state":"open","head":{"ref":"loom/ws/W/change/B"},"base":{"ref":"main"}}`))
	}))
	defer server.Close()
	t.Setenv(connector.GitHubBaseURLEnvVar, server.URL)
	t.Setenv("GITHUB_TOKEN", "fixture-token")
	before := entryGit(t, remote, "rev-parse", "refs/heads/loom/ws/W/change/B")
	for range 2 {
		err := workspacemgr.ReconcileJournal(context.Background(), memstore.New())
		var coded *loomgit.Error
		if !errors.As(err, &coded) || coded.Code() != string(loomgit.AttentionRequired) {
			t.Fatalf("native provider-not-ready result = %v", err)
		}
	}
	if after := entryGit(t, remote, "rev-parse", "refs/heads/loom/ws/W/change/B"); after != before || after != head {
		t.Fatalf("ReconcileJournal pushed native branch: %s -> %s", before, after)
	}
	if got := entryGit(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("ReconcileJournal moved working area: %s", got)
	}
}

func nativeEntryFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	entryGit(t, repo, "init", "-b", "main")
	entryGit(t, repo, "config", "user.name", "Fixture")
	entryGit(t, repo, "config", "user.email", "fixture@example.test")
	if err := os.WriteFile(filepath.Join(repo, "base"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	entryGit(t, repo, "add", "base")
	entryGit(t, repo, "commit", "-m", "base")
	base := entryGit(t, repo, "rev-parse", "HEAD")
	remote := filepath.Join(root, "remote.git")
	entryGit(t, root, "init", "--bare", "-b", "main", remote)
	entryGit(t, repo, "remote", "add", "origin", remote)
	entryGit(t, repo, "push", "origin", "main")
	if err := os.WriteFile(filepath.Join(repo, "B"), []byte("B"), 0600); err != nil {
		t.Fatal(err)
	}
	entryGit(t, repo, "add", "B")
	entryGit(t, repo, "commit", "-m", "B")
	head := entryGit(t, repo, "rev-parse", "HEAD")
	entryGit(t, repo, "push", "origin", "HEAD:refs/heads/loom/ws/W/change/B")
	if err := os.MkdirAll(filepath.Join(configDir, "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(filepath.Join(configDir, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedNativeEntry(t, store, repo, base, head)
	return repo, remote, head
}

func seedNativeEntry(t *testing.T, store *journal.SQLite, repo, base, head string) {
	t.Helper()
	ctx := context.Background()
	entry, _, err := store.Begin(ctx, "workspace-W", "ensure_workspace")
	if err != nil {
		t.Fatal(err)
	}
	entry, err = store.Advance(ctx, entry, "rows_written", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{{Workspace: "W", Repo: "repo", Trunk: "main"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: repo,
		Branch: "main", BaseSHA: base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: "apply-B", Workspace: "W", Lead: "L",
		Change: "B", OldTip: base, NewTip: head, Commits: []string{head}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceApplied(ctx, "apply-B", "prepared", "done"); err != nil {
		t.Fatal(err)
	}
	seedNativeEntryPublication(t, store, repo, base, head)
}

func seedNativeEntryPublication(t *testing.T, store *journal.SQLite, repo, base, head string) {
	t.Helper()
	ctx := context.Background()
	publication := journal.Publication{Workspace: "W", Change: "B", Repo: repo, Branch: "loom/ws/W/change/B",
		Trunk: "main", Slug: "owner/repo", Head: head, StackID: "S"}
	if err := store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.Phase, publication.PRNumber = "done", 2
	if err := store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStackBackend(ctx, "W", "S", "native"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	if err := store.OfferRestack(ctx, journal.RestackOffer{Workspace: "W", Change: "B", Predecessor: "A",
		Repo: "repo", Revision: 1, TrunkSHA: base}); err != nil {
		t.Fatal(err)
	}
}

func entryGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...) //nolint:norawexec // Temporary real-Git entry fixture, no network.
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
