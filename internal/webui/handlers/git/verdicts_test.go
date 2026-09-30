package git

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func TestTaskRevisionRoutesUseRecordedDriverRevision(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository supplies a product revision.
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.test")
	file := filepath.Join(repo, "work")
	if err := os.WriteFile(file, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "work")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(file, []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patch := git("diff", "--binary", base)
	git("restore", "--worktree", ".")
	revision, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(configDir, "loomgit", "store.db"), driverfreeze.Request{
		Workspace: "W", Task: "T", Repo: "repo", Attempt: "one", Worktree: repo, Base: base, Patch: []byte(patch + "\n"), Outcome: "completed", AuthorKind: "agent", AuthorID: "worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	previousFollow := followApproved
	previousArea := hasWorkingArea
	hasWorkingArea = func(_ context.Context, _ *review.Local, _, _ string) (bool, error) { return true, nil }
	followApproved = func(_ context.Context, workspace, lead string) (apply.FollowResult, error) {
		called = true
		if workspace != "W" || lead != "lead" {
			t.Fatalf("follow target = %s/%s", workspace, lead)
		}
		return apply.FollowResult{Applied: []string{revision.Change}}, nil
	}
	t.Cleanup(func() { followApproved = previousFollow; hasWorkingArea = previousArea })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	mux.HandleFunc("POST /api/workspaces/{ws}/changes/{change}/revisions/{r}/verdict", handleVerdict)
	get := httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), revision.HeadSHA) {
		t.Fatalf("list: %d %s", get.Code, get.Body.String())
	}
	path := "/api/workspaces/W/changes/" + revision.Change + "/revisions/" + strconv.Itoa(revision.Number) + "/verdict"
	body, _ := json.Marshal(map[string]any{"head_sha": revision.HeadSHA, "verdict": "approve", "actor": map[string]string{"kind": "human", "id": "user"}})
	post := httptest.NewRecorder()
	mux.ServeHTTP(post, httptest.NewRequest("POST", path, bytes.NewReader(body)))
	if post.Code != 200 {
		t.Fatalf("verdict: %d %s", post.Code, post.Body.String())
	}
	if !called {
		t.Fatal("approval did not call working-area follow")
	}
	get = httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"verdict":"approve"`) {
		t.Fatalf("list after approval: %d %s", get.Code, get.Body.String())
	}
}
