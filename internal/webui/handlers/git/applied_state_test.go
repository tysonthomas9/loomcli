package git

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
)

// The revision list reports applied state from the applied log, so a reload
// shows Applied while the layer is in the lead area and drops it after unapply.
func TestTaskRevisionsReportAppliedStateFromAppliedLog(t *testing.T) {
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
	storePath := filepath.Join(configDir, "loomgit", "store.db")
	revision, err := driverfreeze.FreezeAt(context.Background(), storePath, driverfreeze.Request{
		Workspace: "W", Task: "T", Repo: "repo", Attempt: "one", Worktree: repo, Base: base,
		Patch: []byte(patch + "\n"), Outcome: "completed", AuthorKind: "agent", AuthorID: "worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	applied := func() bool {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
		var body struct {
			Data []map[string]any `json:"data"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || len(body.Data) != 1 {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		value, ok := body.Data[0]["applied"].(bool)
		if !ok {
			t.Fatalf("applied field missing: %s", rec.Body.String())
		}
		return value
	}
	if applied() {
		t.Fatal("fresh revision reported applied")
	}
	db, err := sql.Open("sqlite", storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	run := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	run(`INSERT INTO applied_layers (request_id,workspace,lead,change_id,revision,old_tip,new_tip,commits,dropped,phase)
		VALUES ('req-1','W','lead',?,?,?,?,'[]','[]','done')`, revision.Change, revision.Number, base, revision.HeadSHA)
	if !applied() {
		t.Fatal("revision with a done applied layer not reported applied")
	}
	// loom unapply rebuilds the area and marks the change's layers unapplied.
	run(`UPDATE applied_layers SET phase='unapplied' WHERE request_id='req-1'`)
	if applied() {
		t.Fatal("unapplied revision still reported applied")
	}
}
