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

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// D29 (4): an agent that finished with no changes closes its task. The
// revision is listed as no_changes, and an approve over the API is refused
// with no_changes and never applies; a later attempt with changes is reviewed.
func TestNoChangesRevisionIsListedAndRefusedOverAPI(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	journalPath := filepath.Join(configDir, "loomgit", "store.db")
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
	empty, err := driverfreeze.FreezeCaptureAt(context.Background(), journalPath, driverfreeze.CaptureRequest{
		Workspace: "W", Task: "T", Repo: "repo", Attempt: "empty", Worktree: repo, Base: base,
		CaptureSHA: base, Outcome: "completed", Complete: true, SkipRetention: true})
	if err != nil {
		t.Fatal(err)
	}
	if !empty.NoChanges || empty.Kind != "source" {
		t.Fatalf("empty attempt = %+v", empty)
	}
	followed := false
	previousFollow, previousArea := followApproved, hasWorkingArea
	hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return true, nil }
	followApproved = func(context.Context, string, string) (apply.FollowResult, error) {
		followed = true
		return apply.FollowResult{}, nil
	}
	t.Cleanup(func() { followApproved, hasWorkingArea = previousFollow, previousArea })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	mux.HandleFunc("POST /api/workspaces/{ws}/changes/{change}/revisions/{r}/verdict", handleVerdict)
	list := func() []review.TaskRevision {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
		var out struct{ Data []review.TaskRevision }
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		return out.Data
	}
	approve := func(r loomgit.Revision) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"head_sha": r.HeadSHA, "verdict": "approve",
			"actor": map[string]string{"kind": "human", "id": "user"}})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/workspaces/W/changes/"+r.Change+"/revisions/"+
			strconv.Itoa(r.Number)+"/verdict", bytes.NewReader(body)))
		return rec
	}
	if got := list(); len(got) != 1 || !got[0].NoChanges || got[0].Verdict != "" {
		t.Fatalf("empty revision listing = %+v", got)
	}
	if rec := approve(empty); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error":"no_changes"`) {
		t.Fatalf("approve empty: %d %s", rec.Code, rec.Body.String())
	}
	if followed {
		t.Fatal("a refused approval applied the empty revision")
	}

	if err := os.WriteFile(file, []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	patch := git("diff", "--binary", base)
	git("restore", "--worktree", ".")
	changed, err := driverfreeze.FreezeAt(context.Background(), journalPath, driverfreeze.Request{
		Workspace: "W", Task: "T", Repo: "repo", Attempt: "two", Worktree: repo, Base: base,
		Patch: []byte(patch + "\n"), Outcome: "completed", AuthorKind: "agent", AuthorID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	got := list()
	if len(got) != 2 || got[0].Number != changed.Number || got[0].NoChanges || !got[1].NoChanges {
		t.Fatalf("listing after a changed attempt = %+v", got)
	}
	if rec := approve(changed); rec.Code != http.StatusOK || !followed {
		t.Fatalf("approve changed: %d %s (followed %t)", rec.Code, rec.Body.String(), followed)
	}
}
