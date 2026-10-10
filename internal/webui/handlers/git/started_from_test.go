package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
)

// P2.24: the agent's Changes tab reads where a task started. With no journal
// it is trunk; a dependent pinned to a blocker's revision names the blocker.
func TestStartedFromRoute(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/started-from", handleStartedFrom)
	get := func(task string) map[string]string {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/W/issues/"+task+"/started-from?lead=L", nil))
		var out struct {
			Success bool
			Data    map[string]string
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.Success {
			t.Fatalf("started-from %s: %d %s", task, rec.Code, rec.Body.String())
		}
		return out.Data
	}
	if got := get("B"); got["kind"] != "trunk" || got["task"] != "" {
		t.Fatalf("no journal: %v", got)
	}

	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository supplies the blocker's revision.
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
	blocker, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(configDir, "loomgit", "store.db"), driverfreeze.Request{
		Workspace: "W", Task: "A", Repo: "repo", Attempt: "one", Worktree: repo, Base: base, Patch: []byte(patch + "\n"), Outcome: "completed", AuthorKind: "agent", AuthorID: "worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := get("B"); got["kind"] != "trunk" {
		t.Fatalf("lead without a working area: %v", got)
	}
	if err := taskcopy.RecordLineageBase(context.Background(), "W", "B", "repo", taskcopy.LineageBase{
		SHA: blocker.HeadSHA, Change: blocker.Change, Revision: blocker.Number}); err != nil {
		t.Fatal(err)
	}
	if got := get("B"); got["kind"] != "blocker" || got["task"] != "A" {
		t.Fatalf("dependent: %v", got)
	}
}
