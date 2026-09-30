package applyrecovery_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/epic"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	webgit "github.com/tysonthomas9/loomcli/internal/webui/handlers/git"
)

type epicForge struct{ prs []stackpublish.PR }

func (forge *epicForge) ListStackPRs(_ context.Context, _, _, prefix string) ([]stackpublish.PR, error) {
	var found []stackpublish.PR
	for _, pr := range forge.prs {
		if strings.HasPrefix(pr.Head, prefix) {
			found = append(found, pr)
		}
	}
	return found, nil
}

func (forge *epicForge) CreatePR(_ context.Context, _, _, head, base, _, body string) (stackpublish.PR, error) {
	pr := stackpublish.PR{Number: len(forge.prs) + 1, Head: head, Base: base, Body: body,
		State: "open", URL: "https://github.com/owner/repo/pull/" + strconv.Itoa(len(forge.prs)+1)}
	forge.prs = append(forge.prs, pr)
	return pr, nil
}

func (forge *epicForge) UpdatePRBase(_ context.Context, _, _ string, number int, base string) error {
	forge.prs[number-1].Base = base
	return nil
}

func (forge *epicForge) UpdatePRBody(_ context.Context, _, _ string, number int, body string) error {
	forge.prs[number-1].Body = body
	return nil
}

func freezeSecondEpicTask(t *testing.T, area journal.WorkingArea, base string) loomgit.Revision {
	t.Helper()
	path := filepath.Join(area.Path, "second")
	if err := os.WriteFile(path, []byte("second task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recoveryGit(t, area.Path, "add", "-N", "second")
	patch := recoveryGit(t, area.Path, "diff", "--binary")
	recoveryGit(t, area.Path, "reset", "--", "second")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	revision, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db"),
		driverfreeze.Request{Workspace: "W", Task: "T2", Repo: "repo", Attempt: "epic-task-2",
			Worktree: area.Path, Base: base, Patch: []byte(patch + "\n"), Outcome: "completed",
			AuthorKind: "agent", AuthorID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func approveEpicRevision(t *testing.T, mux *http.ServeMux, revision loomgit.Revision) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"head_sha": revision.HeadSHA, "verdict": "approve", "lead": "L",
		"actor": map[string]string{"kind": "human", "id": "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/workspaces/W/changes/" + revision.Change + "/revisions/" + strconv.Itoa(revision.Number) + "/verdict"
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"applied"`) {
		t.Fatalf("HTTP approval = %d %s", response.Code, response.Body.String())
	}
}

func prepareEpicPublication(t *testing.T, store *journal.SQLite, area journal.WorkingArea, base string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	recoveryGit(t, filepath.Dir(remote), "init", "-q", "--bare", "-b", "main", remote)
	recoveryGit(t, area.Path, "remote", "add", "origin", remote)
	recoveryGit(t, area.Path, "push", "origin", base+":refs/heads/main")
	ctx := context.Background()
	entry, _, err := store.Begin(ctx, "epic-workspace", "ensure_workspace")
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
}

func TestEpicFrozenTasksHTTPApprovalPublishesLinearStack(t *testing.T) {
	ctx, store, area, base, first := bridgeApprovalFixture(t)
	second := freezeSecondEpicTask(t, area, base)
	prepareEpicPublication(t, store, area, base)
	forge := &epicForge{}
	publisher := func(ctx context.Context, workspace, stackID, lead string, changes []string) ([]publish.Result, error) {
		return publish.PublishStackWithProvider(ctx, workspace, stackID, lead, changes, forge, "fixture-token", "owner/repo")
	}
	if err := epic.ReconcileEpicStack(ctx, "W", "L", publisher); err == nil || len(forge.prs) != 0 {
		t.Fatalf("unapproved epic published: %v, %+v", err, forge.prs)
	}
	mux := http.NewServeMux()
	webgit.NewModule(nil, nil).Register(mux)
	approveEpicRevision(t, mux, first)
	approveEpicRevision(t, mux, second)
	layers, err := store.AppliedLog(ctx, "W", "L")
	if err != nil || len(layers) != 2 {
		t.Fatalf("applied layers = %+v, %v", layers, err)
	}
	if err := store.SetDeliveryMode(ctx, "W", "trunk"); err != nil {
		t.Fatal(err)
	}
	err = epic.ReconcileEpicStack(ctx, "W", "L", publisher)
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Kind != loomgit.ModeMismatch || len(forge.prs) != 0 {
		t.Fatalf("trunk epic publish = %v; PRs=%+v", err, forge.prs)
	}
	if err := store.SetDeliveryMode(ctx, "W", "stack"); err != nil {
		t.Fatal(err)
	}
	if err := epic.ReconcileEpicStack(ctx, "W", "L", publisher); err != nil {
		t.Fatal(err)
	}
	if len(forge.prs) != 2 || forge.prs[0].Base != "main" || forge.prs[1].Base != forge.prs[0].Head {
		t.Fatalf("published PR chain = %+v", forge.prs)
	}
}
