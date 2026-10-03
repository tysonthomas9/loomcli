package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
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
	calledLead := ""
	previousFollow := followApproved
	previousArea := hasWorkingArea
	hasWorkingArea = func(_ context.Context, _ *review.Local, _, _ string) (bool, error) { return true, nil }
	followApproved = func(_ context.Context, workspace, lead string) (apply.FollowResult, error) {
		called = true
		calledLead = lead
		if workspace != "W" {
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
	if !called || calledLead != "lead" {
		t.Fatalf("human approval did not follow default lead: %t, %s", called, calledLead)
	}
	leadBody, _ := json.Marshal(map[string]any{"head_sha": revision.HeadSHA, "verdict": "approve",
		"actor": map[string]string{"kind": "lead", "id": "L1"}})
	leadPost := httptest.NewRecorder()
	mux.ServeHTTP(leadPost, httptest.NewRequest("POST", path, bytes.NewReader(leadBody)))
	if leadPost.Code != 200 || calledLead != "L1" {
		t.Fatalf("lead approval did not follow its own area: %d %s, target %s", leadPost.Code, leadPost.Body.String(), calledLead)
	}
	get = httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"verdict":"policy"`) {
		t.Fatalf("list after approval: %d %s", get.Code, get.Body.String())
	}
}

// freezeTaskRevision records one completed driver revision of task T in W.
func freezeTaskRevision(t *testing.T) (string, string, int) {
	t.Helper()
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
	return revision.Change, revision.HeadSHA, revision.Number
}

func TestVerdictApproveAndCreatePROpensPRUnlessApproveOnly(t *testing.T) {
	change, head, number := freezeTaskRevision(t)
	previousFollow, previousArea, previousPublish := followApproved, hasWorkingArea, publishApproved
	t.Cleanup(func() { followApproved, hasWorkingArea, publishApproved = previousFollow, previousArea, previousPublish })
	hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return true, nil }
	followApproved = func(context.Context, string, string) (apply.FollowResult, error) {
		return apply.FollowResult{Applied: []string{change}}, nil
	}
	var publishCalls int
	var outcome publish.ApprovalOutcome
	var publishErr error
	publishApproved = func(_ context.Context, workspace, lead string) ([]publish.ApprovalOutcome, error) {
		publishCalls++
		if workspace != "W" || lead != "lead" {
			t.Fatalf("publish target = %s/%s", workspace, lead)
		}
		return []publish.ApprovalOutcome{outcome}, publishErr
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspaces/{ws}/issues/{id}/revisions", handleTaskRevisions)
	mux.HandleFunc("POST /api/workspaces/{ws}/changes/{change}/revisions/{r}/verdict", handleVerdict)
	post := func(extra map[string]any) (int, map[string]any) {
		body := map[string]any{"head_sha": head, "verdict": "approve", "actor": map[string]string{"kind": "human", "id": "user"}}
		for key, value := range extra {
			body[key] = value
		}
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/workspaces/W/changes/"+change+"/revisions/"+strconv.Itoa(number)+"/verdict", bytes.NewReader(data)))
		var decoded map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
		return recorder.Code, decoded
	}

	code, response := post(map[string]any{"approve_only": true})
	if code != 200 || response["status"] != "applied" || publishCalls != 0 {
		t.Fatalf("Approve only = %d %v, publish calls %d", code, response, publishCalls)
	}

	outcome = publish.ApprovalOutcome{Change: change, Status: "published", PRNumber: 4, PRURL: "https://github.com/o/r/pull/4"}
	code, response = post(nil)
	published, _ := response["publish"].(map[string]any)
	if code != 200 || response["status"] != "published" || publishCalls != 1 || published["pr_url"] != outcome.PRURL {
		t.Fatalf("Approve and create PR = %d %v", code, response)
	}
	get := httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequest("GET", "/api/workspaces/W/issues/T/revisions", nil))
	if !strings.Contains(get.Body.String(), `"publish_status":"pending"`) {
		t.Fatalf("revision list does not show the recorded publish intent: %s", get.Body.String())
	}

	outcome = publish.ApprovalOutcome{Change: change, Status: "not_published", Reason: publish.NoProviderReason + " (the repository has no origin remote)"}
	code, response = post(nil)
	notPublished, _ := response["publish"].(map[string]any)
	if code != 200 || response["status"] != "applied" || !strings.HasPrefix(notPublished["reason"].(string), publish.NoProviderReason) {
		t.Fatalf("no provider = %d %v", code, response)
	}

	outcome, publishErr = publish.ApprovalOutcome{Change: change, Status: "pending", Reason: "provider down"}, errors.New("provider down")
	code, response = post(nil)
	if code != http.StatusConflict || response["error"] != "publish_failed" || response["status"] != "applied" {
		t.Fatalf("publish failure = %d %v", code, response)
	}

	code, response = post(map[string]any{"verdict": "reject", "reason": "no"})
	if code != 200 || response["status"] != "recorded" || publishCalls != 3 {
		t.Fatalf("reject = %d %v, publish calls %d", code, response, publishCalls)
	}
}
