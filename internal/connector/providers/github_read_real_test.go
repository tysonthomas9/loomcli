package providers

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestGitHubReadRealContract runs github_read against a real private repo
// the tester owns: LOOM_REAL_GITHUB_REPO=owner/name with its read token in
// LOOM_REAL_GITHUB_TOKEN. It is skipped otherwise; the token is never
// printed.
func TestGitHubReadRealContract(t *testing.T) {
	full, token := os.Getenv("LOOM_REAL_GITHUB_REPO"), os.Getenv("LOOM_REAL_GITHUB_TOKEN")
	owner, repo, ok := strings.Cut(full, "/")
	if !ok || token == "" {
		t.Skip("set LOOM_REAL_GITHUB_REPO=owner/name (an owned private repo) and LOOM_REAL_GITHUB_TOKEN")
	}
	g := NewGitHub(nil, os.Getenv("LOOM_CONNECTOR_GITHUB_BASE_URL"))
	read := func(op string, args map[string]any) map[string]any {
		t.Helper()
		a := map[string]any{"op": op, "owner": owner, "repo": repo}
		for k, v := range args {
			a[k] = v
		}
		res, err := g.Call(context.Background(), CallSpec{Action: ActionGitHubRead, Args: a, Credential: token})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return res.Body
	}
	if item, _ := read("repo_view", nil)["item"].(map[string]any); item["private"] != true {
		t.Fatalf("repo_view private = %v; the contract needs a private repo", item["private"])
	}
	for _, op := range []string{"pr_list", "issue_list", "run_list", "branch_list", "commit_list", "release_list", "assignees"} {
		read(op, map[string]any{"perPage": 1})
	}
	read("contents", nil)
	if prs, _ := read("pr_list", map[string]any{"state": "all", "perPage": 1})["items"].([]any); len(prs) > 0 {
		pr := prs[0].(map[string]any)
		n := pr["number"].(float64)
		for _, op := range []string{"pr_view", "pr_files", "pr_reviews", "pr_review_comments", "issue_comments"} {
			read(op, map[string]any{"number": n})
		}
		read("check_runs", map[string]any{"ref": pr["head"].(map[string]any)["sha"]})
	}
	if runs, _ := read("run_list", map[string]any{"perPage": 1})["items"].([]any); len(runs) > 0 {
		jobs, _ := read("run_jobs", map[string]any{"run": runs[0].(map[string]any)["id"]})["items"].([]any)
		if len(jobs) > 0 {
			if log := read("job_log", map[string]any{"job": jobs[0].(map[string]any)["id"]}); strings.Contains(log["text"].(string), token) {
				t.Fatal("job_log carries the token")
			}
		}
	}
}
