package git

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
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
	mux.HandleFunc("POST /api/workspaces/{ws}/changes/{change}/revisions/{r}/verdict", handleVerdict)
	state := func() (bool, bool) {
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
		needs, needsOK := body.Data[0]["needs_working_area"].(bool)
		if !ok || !needsOK {
			t.Fatalf("applied or needs_working_area missing: %s", rec.Body.String())
		}
		return value, needs
	}
	applied := func() bool { t.Helper(); a, _ := state(); return a }
	needsArea := func() bool { t.Helper(); _, n := state(); return n }
	if applied() || needsArea() {
		t.Fatal("fresh revision reported applied or waiting for a working area")
	}
	// Approve with no lead working area: the verdict waits for one, and the
	// list says so on every reload.
	previousArea := hasWorkingArea
	hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return false, nil }
	t.Cleanup(func() { hasWorkingArea = previousArea })
	verdictBody, _ := json.Marshal(map[string]any{"head_sha": revision.HeadSHA, "verdict": "approve",
		"actor": map[string]string{"kind": "human", "id": "user"}})
	post := httptest.NewRecorder()
	mux.ServeHTTP(post, httptest.NewRequest("POST", "/api/workspaces/W/changes/"+revision.Change+"/revisions/"+
		strconv.Itoa(revision.Number)+"/verdict", bytes.NewReader(verdictBody)))
	if post.Code != 200 || !strings.Contains(post.Body.String(), "approved_waiting_for_working_area") {
		t.Fatalf("verdict: %d %s", post.Code, post.Body.String())
	}
	if !needsArea() {
		t.Fatal("approved revision without a working area not reported as needing one")
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
	if a, n := state(); !a || n {
		t.Fatalf("revision with a done applied layer: applied=%t needs_working_area=%t", a, n)
	}
	// loom unapply rebuilds the area and marks the change's layers unapplied.
	run(`UPDATE applied_layers SET phase='unapplied' WHERE request_id='req-1'`)
	if applied() {
		t.Fatal("unapplied revision still reported applied")
	}
	run(`INSERT INTO working_areas (workspace,lead,repo,path,branch,base_sha,mode) VALUES ('W','lead','repo','/x','b',?,'interactive')`, base)
	if needsArea() {
		t.Fatal("revision reported as needing a working area after the lead has one")
	}
}
