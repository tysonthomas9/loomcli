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
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// freezeTwoRepoTask records one completed driver revision of task T in two
// repos, alpha and beta, and returns them in that order.
func freezeTwoRepoTask(t *testing.T) []loomgit.Revision {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	var out []loomgit.Revision
	for _, name := range []string{"alpha", "beta"} {
		repo := t.TempDir()
		git := func(args ...string) string {
			t.Helper()
			cmd := exec.Command("git", args...) //nolint:norawexec // Real temporary repository supplies a product revision.
			cmd.Dir = repo
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v: %v: %s", args, err, got)
			}
			return strings.TrimSpace(string(got))
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
		if err := os.WriteFile(file, []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		patch := git("diff", "--binary", base)
		git("restore", "--worktree", ".")
		revision, err := driverfreeze.FreezeAt(context.Background(), filepath.Join(configDir, "loomgit", "store.db"), driverfreeze.Request{
			Workspace: "W", Task: "T", Repo: name, Attempt: "one-" + name, Worktree: repo, Base: base, Patch: []byte(patch + "\n"), Outcome: "completed", AuthorKind: "agent", AuthorID: "worker",
		})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, revision)
	}
	return out
}

// TestTaskApprovalPublishesAllReposOrNone pins Tyson's rule for a task with
// code in two repos: if any repo's apply fails, no repo is published and
// the reply names the repo; once every repo applies, all are published.
func TestTaskApprovalPublishesAllReposOrNone(t *testing.T) {
	revisions := freezeTwoRepoTask(t)
	alpha, beta := revisions[0], revisions[1]
	previousFollow, previousArea, previousPublish := followApproved, hasWorkingArea, publishApproved
	t.Cleanup(func() {
		followApproved, hasWorkingArea, publishApproved = previousFollow, previousArea, previousPublish
	})
	hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return true, nil }
	var followed apply.FollowResult
	var followErr error
	followApproved = func(context.Context, string, string) (apply.FollowResult, error) { return followed, followErr }
	var published []string
	publishApproved = func(_ context.Context, _, _ string, _ publish.DeclaredStacks) ([]publish.ApprovalOutcome, error) {
		outcomes := []publish.ApprovalOutcome{}
		for _, change := range followed.Applied {
			published = append(published, change)
			outcomes = append(outcomes, publish.ApprovalOutcome{Change: change, Status: "published", PRNumber: len(published)})
		}
		return outcomes, nil
	}
	var epicPublishes int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/approval", func(w http.ResponseWriter, r *http.Request) {
		handleTaskApprovalWithPublisher(w, r, func(context.Context, string, string) error { epicPublishes++; return nil })
	})
	approve := func() (int, map[string]any) {
		body := map[string]any{"verdict": "approve", "actor": map[string]string{"kind": "human", "id": "user"}, "revisions": []map[string]any{
			{"change_id": alpha.Change, "number": alpha.Number, "head_sha": alpha.HeadSHA},
			{"change_id": beta.Change, "number": beta.Number, "head_sha": beta.HeadSHA},
		}}
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/workspaces/W/issues/T/approval", bytes.NewReader(data)))
		var decoded map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
		return recorder.Code, decoded
	}
	list := func() string {
		get := httptest.NewRecorder()
		mux.ServeHTTP(get, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
		return get.Body.String()
	}

	// alpha applies, beta conflicts: neither is published, and the reply
	// names beta.
	followed = apply.FollowResult{Applied: []string{alpha.Change}, Paths: []string{"work"}}
	followErr = loomgit.NewError(loomgit.Conflict, "conflicts with the stack", nil)
	code, response := approve()
	if code != http.StatusConflict || response["error"] != "not_all_applied" || response["repo"] != "beta" {
		t.Fatalf("one repo conflicts = %d %v", code, response)
	}
	if message, _ := response["message"].(string); !strings.HasPrefix(message, "beta: couldn't apply: it conflicts") ||
		!strings.Contains(message, "No PR is opened for any repo") {
		t.Fatalf("message = %q", response["message"])
	}
	if len(published) != 0 || epicPublishes != 0 {
		t.Fatalf("published %v (epic %d) although beta did not apply", published, epicPublishes)
	}
	if body := list(); strings.Contains(body, `"publish_status"`) || strings.Count(body, `"verdict":"approve"`) != 2 {
		t.Fatalf("both approvals must be recorded with no PR intent: %s", body)
	}

	// Approving again once both apply publishes both.
	followed, followErr = apply.FollowResult{Applied: []string{alpha.Change, beta.Change}}, nil
	code, response = approve()
	if code != http.StatusOK || response["status"] != "published" || len(published) != 2 || epicPublishes != 1 {
		t.Fatalf("both apply = %d %v, published %v", code, response, published)
	}
	if body := list(); strings.Count(body, `"publish_status":"pending"`) != 2 {
		t.Fatalf("both repos must carry the PR intent: %s", body)
	}
}

func TestTaskApprovalRefusesAStaleRevision(t *testing.T) {
	revisions := freezeTwoRepoTask(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/approval", handleTaskApproval)
	data, _ := json.Marshal(map[string]any{"verdict": "approve", "revisions": []map[string]any{
		{"change_id": revisions[0].Change, "number": revisions[0].Number, "head_sha": strings.Repeat("0", 40)},
	}})
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/workspaces/W/issues/T/approval", bytes.NewReader(data)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "stale_revision") {
		t.Fatalf("stale head = %d %s", recorder.Code, recorder.Body.String())
	}
}

// TestTaskApprovalRefusesBeforeRecording pins the two whole-task refusals:
// a request that leaves out a repo still without a PR, and several repos
// while the lead has no working area. Neither records a verdict or a PR
// intent, and both name the repo.
func TestTaskApprovalRefusesBeforeRecording(t *testing.T) {
	revisions := freezeTwoRepoTask(t)
	alpha, beta := revisions[0], revisions[1]
	previousArea := hasWorkingArea
	t.Cleanup(func() { hasWorkingArea = previousArea })
	available := true
	hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return available, nil }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	mux.HandleFunc("POST /api/workspaces/{ws}/issues/{id}/approval", func(w http.ResponseWriter, r *http.Request) {
		handleTaskApprovalWithPublisher(w, r, func(context.Context, string, string) error {
			t.Fatal("a refused approval published")
			return nil
		})
	})
	approve := func(revs ...loomgit.Revision) (int, map[string]any) {
		items := []map[string]any{}
		for _, r := range revs {
			items = append(items, map[string]any{"change_id": r.Change, "number": r.Number, "head_sha": r.HeadSHA})
		}
		data, _ := json.Marshal(map[string]any{"verdict": "approve", "actor": map[string]string{"kind": "human", "id": "user"}, "revisions": items})
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/workspaces/W/issues/T/approval", bytes.NewReader(data)))
		var decoded map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
		return recorder.Code, decoded
	}
	nothingRecorded := func() {
		t.Helper()
		get := httptest.NewRecorder()
		mux.ServeHTTP(get, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
		if body := get.Body.String(); strings.Contains(body, `"verdict"`) || strings.Contains(body, `"publish_status"`) {
			t.Fatalf("a refused approval recorded something: %s", body)
		}
	}

	if code, response := approve(alpha); code != http.StatusConflict || response["error"] != "missing_repo" || response["repo"] != "beta" {
		t.Fatalf("approving alpha alone = %d %v", code, response)
	}
	nothingRecorded()

	available = false
	code, response := approve(alpha, beta)
	if code != http.StatusConflict || response["error"] != "no_working_area" || response["repo"] != "alpha" {
		t.Fatalf("no working area = %d %v", code, response)
	}
	if message, _ := response["message"].(string); !strings.Contains(message, "No repo is approved") {
		t.Fatalf("message = %q", message)
	}
	nothingRecorded()
}
