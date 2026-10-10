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
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskreview"
)

type frozenTask struct {
	change, head string
	number       int
}

// dependentTasks freezes task A, then task B built on A's revision 1 before
// A was reviewed, in one journal. freeze adds another attempt of a task.
func dependentTasks(t *testing.T) (frozenTask, frozenTask, func(task, attempt, body string) frozenTask) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository supplies product revisions.
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
	freeze := func(task, attempt, body string) frozenTask {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		patch := git("diff", "--binary", base)
		git("restore", "--worktree", ".")
		revision, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(configDir, "loomgit", "store.db"), driverfreeze.Request{
			Workspace: "W", Task: task, Repo: "repo", Attempt: attempt, Worktree: repo, Base: base,
			Patch: []byte(patch + "\n"), Outcome: "completed", AuthorKind: "agent", AuthorID: "worker",
		})
		if err != nil {
			t.Fatal(err)
		}
		return frozenTask{revision.Change, revision.HeadSHA, revision.Number}
	}
	a := freeze("A", "a1", "a\n")
	b := freeze("B", "b1", "b\n")
	if err := taskcopy.RecordLineageBase(context.Background(), "W", "B", "repo",
		taskcopy.LineageBase{Change: a.change, Revision: a.number, SHA: a.head}); err != nil {
		t.Fatal(err)
	}
	return a, b, freeze
}

func dependencyMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/rebuild", handleRebuild)
	mux.HandleFunc("POST /api/workspaces/{ws}/changes/{change}/revisions/{r}/verdict", handleVerdict)
	return mux
}

func serve(t *testing.T, mux *http.ServeMux, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, path, reader))
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("%s %s: %d %s", method, path, recorder.Code, recorder.Body.String())
	}
	return recorder.Code, decoded
}

func verdict(t *testing.T, mux *http.ServeMux, task frozenTask, kind string) (int, map[string]any) {
	t.Helper()
	return serve(t, mux, "POST", "/api/workspaces/W/changes/"+task.change+"/revisions/"+strconv.Itoa(task.number)+"/verdict",
		map[string]any{"head_sha": task.head, "verdict": kind, "reason": "r", "approve_only": true,
			"actor": map[string]string{"kind": "human", "id": "user"}})
}

func newestRevision(t *testing.T, mux *http.ServeMux, task string) map[string]any {
	t.Helper()
	code, body := serve(t, mux, "GET", "/api/workspaces/W/issues/"+task+"/revisions", nil)
	data, _ := body["data"].([]any)
	if code != 200 || len(data) == 0 {
		t.Fatalf("revisions of %s: %d %v", task, code, body)
	}
	return data[0].(map[string]any)
}

// Tyson, 2026-10-09: B starts on A's unreviewed revision. Approving B first
// waits for A and says so; rejecting A makes B stale, Approve on B is refused
// with the reason, and Rebuild sets B aside once A has a new revision.
func TestDependentWaitsThenGoesStaleAndRebuilds(t *testing.T) {
	a, b, freeze := dependentTasks(t)
	previousFollow, previousArea, previousSettle := followApproved, hasWorkingArea, settleTask
	t.Cleanup(func() { followApproved, hasWorkingArea, settleTask = previousFollow, previousArea, previousSettle })
	hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return true, nil }
	followApproved = func(context.Context, string, string) (apply.FollowResult, error) {
		return apply.FollowResult{Pending: []string{b.change}}, nil
	}
	var settled []string
	settleTask = func(_ context.Context, _ string, change string) (taskreview.Decision, error) {
		settled = append(settled, change)
		return taskreview.Wait, nil
	}
	mux := dependencyMux()

	code, body := verdict(t, mux, b, "approve")
	if code != 200 || body["status"] != "approved_waiting_for_dependency" || body["reason"] != "waiting for A to be approved" {
		t.Fatalf("approve B before A = %d %v", code, body)
	}
	if got := newestRevision(t, mux, "B"); got["depends_on"] != "A" || got["lineage_state"] != nil {
		t.Fatalf("B before A's review = %v", got)
	}

	if code, body := verdict(t, mux, a, "reject"); code != 200 {
		t.Fatalf("reject A = %d %v", code, body)
	}
	const stale = "built on A's code, which was rejected: rebuild it once A has new code"
	if got := newestRevision(t, mux, "B"); got["lineage_state"] != "stale" || got["lineage_reason"] != stale {
		t.Fatalf("B after A was rejected = %v", got)
	}
	code, body = verdict(t, mux, b, "approve")
	if code != http.StatusConflict || body["error"] != "stale" || body["message"] != "approve is refused: "+stale {
		t.Fatalf("approve stale B = %d %v", code, body)
	}
	rebuild := map[string]any{"actor": map[string]string{"kind": "human", "id": "user"}}
	code, body = serve(t, mux, "POST", "/api/workspaces/W/issues/B/rebuild", rebuild)
	if code != http.StatusConflict || body["message"] != stale {
		t.Fatalf("rebuild with nothing newer = %d %v", code, body)
	}

	next := freeze("A", "a2", "a again\n")
	if got := newestRevision(t, mux, "B"); got["rebuild_on"] != float64(next.number) {
		t.Fatalf("B once A has revision %d = %v", next.number, got)
	}
	byAgent := map[string]any{"actor": map[string]string{"kind": "agent", "id": "worker"}}
	if code, body := serve(t, mux, "POST", "/api/workspaces/W/issues/B/rebuild", byAgent); code != http.StatusConflict || body["error"] != "review_required" {
		t.Fatalf("rebuild by an agent = %d %v, want refused", code, body)
	}
	settled = nil
	code, body = serve(t, mux, "POST", "/api/workspaces/W/issues/B/rebuild", rebuild)
	data, _ := body["data"].(map[string]any)
	if code != 200 || data["rebuild_on"] != float64(next.number) || data["verdict"] != "reject" ||
		len(settled) != 1 || settled[0] != b.change {
		t.Fatalf("rebuild = %d %v, settled %v", code, body, settled)
	}
	if got := newestRevision(t, mux, "B"); got["verdict"] != "reject" {
		t.Fatalf("B after rebuild = %v", got)
	}
	if code, body := serve(t, mux, "POST", "/api/workspaces/W/issues/B/rebuild", map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("rebuild without an actor = %d %v", code, body)
	}
}
