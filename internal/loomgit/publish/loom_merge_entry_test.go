package publish_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func TestReconcileJournalLoomMergeProgressAndReviewStop(t *testing.T) {
	ctx := context.Background()
	store := entryMergeFixture(t)
	if err := workspacemgr.ReconcileJournal(ctx, memstore.New()); err != nil {
		t.Fatal(err)
	}
	progress, err := store.LoomMerge(ctx, "W", "stack")
	if err != nil || progress.Phase != "restacking" {
		t.Fatalf("entry did not advance merge: %+v, %v", progress, err)
	}
	offer := journal.RestackOffer{Workspace: "W", Change: "B", Predecessor: "A", Repo: "repo", Revision: 1, TrunkSHA: "landed-a"}
	if err := store.RecordRestackReviewRequired(ctx, offer, "stack", "lead", "B", 2); err != nil {
		t.Fatal(err)
	}
	if err := workspacemgr.ReconcileJournal(ctx, memstore.New()); err == nil {
		t.Fatal("expected review stop")
	}
	stopped, err := store.LoomMerge(ctx, "W", "stack")
	if err != nil || stopped.Phase != "blocked" || stopped.Reason == "" {
		t.Fatalf("review stop was not persisted: %+v, %v", stopped, err)
	}
}

func entryMergeFixture(t *testing.T) *journal.SQLite {
	t.Helper()
	ctx := context.Background()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("GITHUB_TOKEN", "fixture-token")
	t.Setenv("LOOM_CONNECTOR_GITHUB_BASE_URL", entryProvider(t))
	if err := os.MkdirAll(filepath.Join(configDir, "loomgit"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(filepath.Join(configDir, "loomgit", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo := entryGitRepo(t)
	publication := journal.Publication{Workspace: "W", Change: "A", Repo: repo, Branch: "branch-a",
		Trunk: "main", Slug: "owner/repo", Head: "head-a", StackID: "stack", Phase: "done", PRNumber: 1}
	if err := store.BeginPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvancePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordStackBackend(ctx, "W", "stack", "loom"); err != nil {
		t.Fatal(err)
	}
	merge, err := store.BeginLoomMerge(ctx, journal.LoomMerge{Workspace: "W", StackID: "stack", Target: "B",
		RequestID: "merge-entry", Layers: []journal.LoomMergeLayer{{Change: "A", Head: "head-a", Revision: 1},
			{Change: "B", Head: "head-b", Revision: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	landing := merge
	landing.Phase = "landing"
	if err := store.AdvanceLoomMerge(ctx, merge, landing); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkLanded(ctx, "W", "A", "merge_commit"); err != nil {
		t.Fatal(err)
	}
	return store
}

func entryProvider(t *testing.T) string {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/repos/owner/repo/pulls/1" {
			t.Errorf("unexpected provider request: %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = writer.Write([]byte(`{"number":1,"state":"closed","merged_at":"2026-01-01T00:00:00Z",` +
			`"head":{"ref":"branch-a","sha":"head-a"},"base":{"ref":"main"}}`))
	}))
	t.Cleanup(provider.Close)
	return provider.URL
}

func entryGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo, remote := filepath.Join(root, "repo"), filepath.Join(root, "remote.git")
	for _, command := range [][]string{{"init", "-b", "main", repo}, {"init", "--bare", remote}} {
		if output, err := exec.Command("git", command...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", command, output, err)
		}
	}
	for _, command := range [][]string{{"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.com"},
		{"commit", "--allow-empty", "-m", "base"}, {"remote", "add", "origin", remote}, {"push", "origin", "main"}} {
		process := exec.Command("git", command...)
		process.Dir = repo
		if output, err := process.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", command, output, err)
		}
	}
	return repo
}
