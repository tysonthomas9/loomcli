package remotecapture_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	"github.com/tysonthomas9/loomcli/internal/loomgit/remotecapture"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Isolated fixture uses a real Git client and temporary repositories.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Loom", "GIT_AUTHOR_EMAIL=loom@localhost",
		"GIT_COMMITTER_NAME=Loom", "GIT_COMMITTER_EMAIL=loom@localhost")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func setup(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "config"))
	source := filepath.Join(root, "source")
	provider := filepath.Join(root, "provider.git")
	task := filepath.Join(root, "task")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "--bare", "-q", "-b", "main", provider)
	git(t, source, "init", "-q", "-b", "main")
	git(t, source, "remote", "add", "origin", provider)
	if err := os.WriteFile(filepath.Join(source, "readme"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "readme")
	git(t, source, "commit", "-qm", "base")
	base := git(t, source, "rev-parse", "HEAD")
	git(t, source, "push", "-q", "origin", "main")
	git(t, root, "clone", "-q", provider, task)
	if err := bootstrap.MutateWorkspaceLocalState("W", func(local *bootstrap.WorkspaceLocalState) error {
		local.Repos = map[string]string{"app": source}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(root, "store.db")
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	entry, _, err := store.Begin(ctx, "workspace-create:W", "ensure_workspace")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"checkouts_added", "rows_written"} {
		entry, err = store.Advance(ctx, entry, phase, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CommitWorkspace(ctx, entry, []loomgit.WorkspaceRepo{{Workspace: "W", Repo: "app", Trunk: "main", WorkspaceBranch: "lead", BaseSHA: base}}); err != nil {
		t.Fatal(err)
	}
	return source, provider, task, journalPath, base
}

func TestRemoteCaptureProxyAndSnapshot(t *testing.T) {
	source, provider, task, journalPath, base := setup(t)
	ctx := context.Background()
	for _, file := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(task, file), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, task, "add", file)
		git(t, task, "commit", "-qm", file)
	}
	if err := os.WriteFile(filepath.Join(task, "readme"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task, ".env"), []byte("SECRET=fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err := agentcapture.Capture(ctx, task, "W", "run-a1", "task", "task")
	if err != nil {
		t.Fatal(err)
	}
	if capture.Complete || !hasSecret(capture.Entries, ".env") {
		t.Fatalf("secret was not retained in manifest: %+v", capture)
	}
	token, ref, err := remotecapture.Prepare(ctx, journalPath, "W", "run-a1", provider, base, "run:1")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remotecapture.ServeHTTP(w, r, journalPath, "W")
	}))
	defer server.Close()
	push := exec.Command("git", "push", "--force", server.URL+"/capture.git", capture.SHA+":"+ref) //nolint:norawexec // Scoped-token push targets the temporary fake provider.
	push.Dir = task
	push.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Bearer "+token)
	if output, err := push.CombinedOutput(); err != nil {
		t.Fatalf("scoped proxy push: %v: %s", err, output)
	}
	if got := git(t, provider, "rev-parse", ref); got != capture.SHA {
		t.Fatalf("provider capture = %s, want %s", got, capture.SHA)
	}
	tree := git(t, task, "rev-parse", capture.SHA+"^{tree}")
	if _, err := remotecapture.Finalize(ctx, journalPath, remotecapture.FinalizeInput{
		Workspace: "W", Attempt: "run-a1", Task: "task", RepoURL: provider,
		BaseSHA: base, CaptureSHA: base, TreeHash: tree,
		Outcome: "failed", Complete: false, Owner: "run:1",
	}); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("mismatched provider SHA must be rejected: %v", err)
	}
	revision, err := remotecapture.Finalize(ctx, journalPath, remotecapture.FinalizeInput{
		Workspace: "W", Attempt: "run-a1", Task: "task", RepoURL: provider,
		BaseSHA: base, CaptureSHA: capture.SHA, TreeHash: tree,
		Outcome: "failed", Complete: false, Owner: "run:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.BaseSHA != base || revision.TreeHash != tree || !revision.Incomplete || !revision.Ready {
		t.Fatalf("revision = %+v", revision)
	}
	if got := git(t, source, "show", revision.HeadSHA+":readme"); got != "edited" {
		t.Fatalf("revision lost edit: %q", got)
	}
	if got := git(t, source, "ls-tree", "-r", "--name-only", revision.HeadSHA); strings.Contains(got, ".env") {
		t.Fatalf("secret reached revision: %s", got)
	}
	replay, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/capture.git/info/refs?service=git-receive-pack", nil)
	if err != nil {
		t.Fatal(err)
	}
	replay.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("released token replay status = %d", response.StatusCode)
	}
}

func hasSecret(entries []agentcapture.Entry, path string) bool {
	for _, entry := range entries {
		if entry.Path == path && entry.Class == "secret_suspect" {
			return true
		}
	}
	return false
}

func TestPendingCaptureRecoversAfterMirrorRetry(t *testing.T) {
	source, provider, task, journalPath, base := setup(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(task, "change"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, task, "add", "change")
	git(t, task, "commit", "-qm", "remote")
	head := git(t, task, "rev-parse", "HEAD")
	tree := git(t, task, "rev-parse", "HEAD^{tree}")
	_, ref, err := remotecapture.Prepare(ctx, journalPath, "W", "run-a2", provider, base, "run:2")
	if err != nil {
		t.Fatal(err)
	}
	git(t, source, "fetch", "-q", task, head)
	git(t, source, "update-ref", ref, head)
	input := remotecapture.FinalizeInput{
		Workspace: "W", Attempt: "run-a2", Task: "task", RepoURL: provider,
		BaseSHA: base, CaptureSHA: head, TreeHash: tree,
		Outcome: "failed", Complete: false, Owner: "run:2",
	}
	if err := remotecapture.Remember(ctx, journalPath, input, "provider unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := remotecapture.Recover(ctx, journalPath); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := mirror.SyncRepo(ctx, store, source, base); err != nil {
		t.Fatal(err)
	}
	if err := remotecapture.Recover(ctx, journalPath); err != nil {
		t.Fatal(err)
	}
	if got := git(t, provider, "rev-parse", ref); got != head {
		t.Fatalf("mirror head = %s, want %s", got, head)
	}
	pending, err := store.PendingRemoteCaptures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after recovery: %+v", pending)
	}
}
