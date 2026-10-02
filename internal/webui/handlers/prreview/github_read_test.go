package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

const readRepoPath = "/clones/hello"

// readFixtures is a private repo octocat/hello as GitHub answers it. Every
// object carries node_id and url, which github_read must drop, and the PR
// body and job log echo the host token, which it must redact.
func readFixtures() map[string]any {
	user := map[string]any{"login": "octocat", "type": "User", "node_id": "U1", "url": "u"}
	pr := map[string]any{"number": 8, "state": "open", "title": "Add feature", "body": "see " + prReviewTestToken,
		"draft": false, "merged": false, "mergeable": true, "mergeable_state": "clean", "user": user, "node_id": "PR8",
		"labels": []any{map[string]any{"name": "bug", "node_id": "L1"}}, "requested_reviewers": []any{map[string]any{"login": "rev"}},
		"head": map[string]any{"ref": "feature", "sha": "headsha-8", "repo": map[string]any{"full_name": "octocat/hello"}},
		"base": map[string]any{"ref": "main", "sha": "basesha-8"}, "html_url": "https://github.com/octocat/hello/pull/8",
		"additions": 3, "deletions": 1, "changed_files": 1, "created_at": "2026-10-01T00:00:00Z", "updated_at": "2026-10-02T00:00:00Z"}
	issue := map[string]any{"number": 9, "state": "open", "title": "Bug", "body": "broken", "user": user, "node_id": "I9"}
	file := map[string]any{"filename": "README.md", "status": "modified", "additions": 3, "deletions": 1, "patch": "@@ -1 +1 @@\n-old\n+new", "blob_url": "b"}
	run := map[string]any{"id": 5, "name": "CI", "status": "completed", "conclusion": "failure", "head_sha": "headsha-8", "node_id": "R5"}
	release := map[string]any{"id": 1, "tag_name": "v1", "name": "One", "draft": false, "author": user, "node_id": "RE1"}
	commit := map[string]any{"sha": "abc", "commit": map[string]any{"message": "fix", "author": map[string]any{"name": "Oct", "email": "o@x"}}, "node_id": "C1"}
	const r = "/repos/octocat/hello"
	return map[string]any{
		r:                                   map[string]any{"name": "hello", "full_name": "octocat/hello", "private": true, "visibility": "private", "default_branch": "main", "node_id": "R"},
		r + "/pulls/8":                      pr,
		r + "/pulls/8/files":                []any{file},
		r + "/pulls":                        []any{pr},
		"/search/issues":                    map[string]any{"total_count": 1, "items": []any{issue}},
		r + "/pulls/8/reviews":              []any{map[string]any{"id": 2, "state": "CHANGES_REQUESTED", "body": "fix it", "user": user, "node_id": "RV"}},
		r + "/pulls/8/comments":             []any{map[string]any{"id": 3, "body": "nit", "path": "README.md", "line": 1, "user": user, "node_id": "PC"}},
		r + "/issues/8/comments":            []any{map[string]any{"id": 4, "body": "thanks", "user": user, "node_id": "IC"}},
		r + "/issues/9":                     issue,
		r + "/issues":                       []any{issue},
		r + "/commits/headsha-8/check-runs": map[string]any{"total_count": 1, "check_runs": []any{map[string]any{"id": 7, "name": "test", "status": "completed", "conclusion": "failure", "node_id": "CR"}}},
		r + "/commits/headsha-8/status":     map[string]any{"state": "failure", "statuses": []any{map[string]any{"context": "ci", "state": "failure", "node_id": "S"}}},
		r + "/actions/runs":                 map[string]any{"total_count": 1, "workflow_runs": []any{run}},
		r + "/actions/runs/5":               run,
		r + "/actions/runs/5/jobs":          map[string]any{"jobs": []any{map[string]any{"id": 6, "name": "test", "conclusion": "failure", "node_id": "J"}}},
		r + "/releases":                     []any{release},
		r + "/releases/tags/v1":             release,
		r + "/releases/latest":              release,
		r + "/commits":                      []any{commit},
		r + "/commits/abc":                  commit,
		r + "/compare/main...feature":       map[string]any{"status": "ahead", "ahead_by": 1, "commits": []any{commit}, "files": []any{file}},
		r + "/branches":                     []any{map[string]any{"name": "main", "protected": true, "commit": map[string]any{"sha": "abc", "url": "u"}}},
		r + "/contents/docs/README.md":      map[string]any{"name": "README.md", "path": "docs/README.md", "type": "file", "content": "aGk=", "encoding": "base64", "git_url": "g"},
		r + "/assignees":                    []any{user},
	}
}

// readHarness is the prreview harness with readFixtures served and
// readRepoPath registered as octocat/hello's clone.
func readHarness(t *testing.T) *prReviewHarness {
	t.Helper()
	h := newPRReviewHarness(t, true)
	h.rememberLocalPaths(t, "/clones", "hello", readRepoPath)
	g := h.github
	g.extra = map[string]http.HandlerFunc{}
	for path, body := range readFixtures() {
		g.extra[path] = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "1" && r.URL.Query().Get("per_page") == "1" {
				w.Header().Set("Link", `<`+g.server.URL+path+`?page=2>; rel="next"`)
			}
			writeUpstreamJSON(w, http.StatusOK, body)
		}
	}
	g.extra["/repos/octocat/hello/actions/jobs/6/logs"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, g.server.URL+"/blob/job-6.log", http.StatusFound)
	}
	g.extra["/blob/job-6.log"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 300<<10) + "\nError: GITHUB_TOKEN=" + prReviewTestToken + " test failed\n"))
	}
	g.extra["/graphql"] = func(w http.ResponseWriter, _ *http.Request) {
		node := func(n int, head, mergeable, review, checks string) map[string]any {
			return map[string]any{"number": n, "headRefName": head, "mergeable": mergeable, "reviewDecision": review,
				"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": map[string]any{"state": checks}}}}}}
		}
		writeUpstreamJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{
			"nodes": []any{node(11, "loom/stack/s1/t1", "MERGEABLE", "APPROVED", "SUCCESS"), node(12, "loom/stack/s1/t2", "CONFLICTING", "CHANGES_REQUESTED", "FAILURE"),
				node(13, "other", "MERGEABLE", "APPROVED", "SUCCESS")},
			"pageInfo": map[string]any{"hasNextPage": false}}}}})
	}
	return h
}

func (h *prReviewHarness) read(t *testing.T, op string, args map[string]any) (map[string]any, error) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	return h.module.GitHubRead(context.Background(), prReviewTestWorkspace, "a1", readRepoPath, op, args)
}

// dig returns the value at a dotted path; numeric segments index arrays.
func dig(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		switch t := v.(type) {
		case map[string]any:
			v = t[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i >= len(t) {
				return nil
			}
			v = t[i]
		default:
			return nil
		}
	}
	return v
}

// TestGitHubReadInventoryR1ToR11 maps each of today's agent GitHub reads
// (R1–R11, note 4d917952) to its typed github_read op on a private repo,
// including pagination, job logs and live stack PR health.
func TestGitHubReadInventoryR1ToR11(t *testing.T) {
	h := readHarness(t)
	for _, c := range []struct {
		read, op string
		args     map[string]any
		want     map[string]any // dotted path -> value
	}{
		{"R1 gh pr view", "pr_view", map[string]any{"number": 8}, map[string]any{"item.state": "open", "item.body": "see [redacted]",
			"item.mergeable": true, "item.mergeable_state": "clean", "item.requested_reviewers.0.login": "rev", "item.labels.0.name": "bug", "item.head.sha": "headsha-8"}},
		{"R2/R3 gh pr diff", "pr_files", map[string]any{"number": 8}, map[string]any{"items.0.filename": "README.md", "items.0.patch": "@@ -1 +1 @@\n-old\n+new"}},
		{"R2/R3 diff against a pinned base", "compare", map[string]any{"base": "main", "head": "feature"}, map[string]any{"item.ahead_by": 1.0, "item.files.0.patch": "@@ -1 +1 @@\n-old\n+new"}},
		{"R4 gh pr checks", "check_runs", map[string]any{"ref": "headsha-8"}, map[string]any{"items.0.name": "test", "items.0.conclusion": "failure"}},
		{"R4 commit status", "commit_status", map[string]any{"ref": "headsha-8"}, map[string]any{"item.state": "failure", "item.statuses.0.context": "ci"}},
		{"R5 reviews", "pr_reviews", map[string]any{"number": 8}, map[string]any{"items.0.state": "CHANGES_REQUESTED", "items.0.user.login": "octocat"}},
		{"R5 review comments", "pr_review_comments", map[string]any{"number": 8}, map[string]any{"items.0.body": "nit", "items.0.path": "README.md"}},
		{"R5 conversation", "issue_comments", map[string]any{"number": 8}, map[string]any{"items.0.body": "thanks"}},
		{"R6 gh pr list", "pr_list", map[string]any{"state": "open"}, map[string]any{"items.0.number": 8.0, "items.0.head.ref": "feature"}},
		{"R6 gh search prs", "pr_search", map[string]any{"query": "feature"}, map[string]any{"items.0.number": 9.0}},
		{"R7 gh issue view", "issue_view", map[string]any{"number": 9}, map[string]any{"item.title": "Bug", "item.body": "broken"}},
		{"R7 gh issue list", "issue_list", nil, map[string]any{"items.0.number": 9.0}},
		{"R7 gh search issues", "issue_search", map[string]any{"query": "bug"}, map[string]any{"items.0.title": "Bug"}},
		{"R8 gh run list", "run_list", map[string]any{"branch": "feature"}, map[string]any{"items.0.conclusion": "failure"}},
		{"R8 gh run view", "run_view", map[string]any{"run": 5}, map[string]any{"item.name": "CI"}},
		{"R8 run jobs", "run_jobs", map[string]any{"run": 5}, map[string]any{"items.0.id": 6.0}},
		{"R8 gh run view --log", "job_log", map[string]any{"job": 6}, map[string]any{"truncated": true}},
		{"R9 contents", "contents", map[string]any{"path": "docs/README.md"}, map[string]any{"item.content": "aGk=", "item.git_url": nil}},
		{"R9 commits", "commit_list", nil, map[string]any{"items.0.commit.message": "fix", "items.0.commit.author.email": nil}},
		{"R9 commit", "commit_view", map[string]any{"ref": "abc"}, map[string]any{"item.sha": "abc"}},
		{"R9 branches", "branch_list", nil, map[string]any{"items.0.name": "main", "items.0.commit.sha": "abc"}},
		{"R9 repo users", "assignees", nil, map[string]any{"items.0.login": "octocat"}},
		{"R10 gh repo view (private)", "repo_view", nil, map[string]any{"item.private": true, "item.visibility": "private"}},
		{"R10 gh release list", "release_list", nil, map[string]any{"items.0.tag_name": "v1"}},
		{"R10 gh release view", "release_view", map[string]any{"tag": "v1"}, map[string]any{"item.name": "One"}},
		{"R10 latest release", "release_latest", nil, map[string]any{"item.tag_name": "v1"}},
		{"R11 loom stack status live health", "stack_health", map[string]any{"head": "loom/stack/s1/"}, map[string]any{
			"items.0.number": 11, "items.0.checks": "passing", "items.0.review": "approved", "items.0.mergeable": "mergeable",
			"items.1.checks": "failing", "items.1.mergeable": "conflicting", "items.2": nil}},
	} {
		t.Run(c.read, func(t *testing.T) {
			body, err := h.read(t, c.op, c.args)
			if err != nil {
				t.Fatalf("%s: %v", c.op, err)
			}
			for path, want := range c.want {
				if got := dig(body, path); got != want {
					t.Errorf("%s %s = %#v; want %#v", c.op, path, got, want)
				}
			}
			raw, _ := json.Marshal(body)
			if strings.Contains(string(raw), "node_id") {
				t.Errorf("%s kept a key outside its allowlist: %s", c.op, raw)
			}
		})
	}
	log, _ := h.read(t, "job_log", map[string]any{"job": 6})
	if text, _ := log["text"].(string); len(text) > 256<<10 || len(text) < 255<<10 || !strings.HasSuffix(text, "GITHUB_TOKEN=[redacted] test failed\n") {
		t.Errorf("job_log text: %d bytes, tail %q; want the redacted last 256 KiB", len(text), text[max(0, len(text)-60):])
	}
}

// TestGitHubReadPagination: a list op reads one bounded page; next names the
// following page while GitHub has one.
func TestGitHubReadPagination(t *testing.T) {
	h := readHarness(t)
	first, err := h.read(t, "pr_list", map[string]any{"perPage": 1})
	if err != nil || first["next"] != "2" {
		t.Fatalf("page 1 = %v, %v; want next 2", first, err)
	}
	last, err := h.read(t, "pr_list", map[string]any{"perPage": 1, "page": 2})
	if err != nil || last["next"] != nil {
		t.Fatalf("page 2 = %v, %v; want no next", last, err)
	}
	if _, err := h.read(t, "pr_list", map[string]any{"perPage": 1000}); err != nil {
		t.Fatal(err)
	}
	var queries []string
	for _, c := range h.github.snapshot() {
		if c.path == "/repos/octocat/hello/pulls" {
			queries = append(queries, c.query)
		}
	}
	if strings.Join(queries, " ") != "page=1&per_page=1 page=2&per_page=1 page=1&per_page=100" {
		t.Errorf("queries = %v; want bounded pages", queries)
	}
}

// TestGitHubReadStackHealthPages: stack_health answers one bounded page of
// the stack's PRs, at most 100, with next while more remain.
func TestGitHubReadStackHealthPages(t *testing.T) {
	h := readHarness(t)
	nodes := []any{}
	for i := range 150 {
		nodes = append(nodes, map[string]any{"number": i + 1, "headRefName": fmt.Sprintf("loom/stack/s1/t%03d", i), "mergeable": "MERGEABLE"})
	}
	h.github.extra["/graphql"] = func(w http.ResponseWriter, _ *http.Request) {
		writeUpstreamJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequests": map[string]any{
			"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": false}}}}})
	}
	for _, c := range []struct {
		page, perPage, n int
		next             any
	}{{0, 1000, 100, "2"}, {2, 1000, 50, nil}, {1, 0, 30, "2"}, {1 << 40, 100, 0, nil}} {
		got, err := h.read(t, "stack_health", map[string]any{"head": "loom/stack/s1/", "page": c.page, "perPage": c.perPage})
		items, _ := got["items"].([]any)
		if err != nil || len(items) != c.n || got["next"] != c.next {
			t.Errorf("page %d perPage %d: %d items, next %v, err %v; want %d, %v", c.page, c.perPage, len(items), got["next"], err, c.n, c.next)
		}
	}
}

// TestGitHubReadToolsReadOnly: only allowlisted ops run, every request is a
// GET (stack_health's fixed host-side GraphQL query is a read), and write
// verbs, arbitrary REST or GraphQL, and shell commands are refused before
// anything reaches GitHub.
func TestGitHubReadToolsReadOnly(t *testing.T) {
	h := readHarness(t)
	for _, op := range []string{"", "pr_merge", "pr_create", "review_post", "issue_comment_post", "merge", "api", "graphql", "rest",
		"gh pr view 8", "sh -c gh", "GET /repos/evil/x", "../pr_view", "user_view"} {
		if _, err := h.read(t, op, map[string]any{"number": 8, "method": "POST", "path": "/repos/octocat/hello/pulls/8/merge"}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("op %q: err = %v; want invalid", op, err)
		}
	}
	for _, c := range h.github.snapshot() {
		t.Errorf("a refused op reached GitHub: %s %s", c.method, c.path)
	}
	// Extra args name no method or path: pr_view stays one GET of the PR.
	if _, err := h.read(t, "pr_view", map[string]any{"number": 8, "method": "PUT", "path": "x", "url": "https://evil"}); err != nil {
		t.Fatal(err)
	}
	for op := range readFixtureOps {
		if _, err := h.read(t, op, readFixtureOps[op]); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	for _, c := range h.github.snapshot() {
		if c.method != http.MethodGet && (c.path != "/graphql" || !strings.HasPrefix(c.body["query"].(string), "query(")) {
			t.Errorf("github_read sent %s %s", c.method, c.path)
		}
	}
}

var readFixtureOps = map[string]map[string]any{"pr_view": {"number": 8}, "pr_files": {"number": 8}, "pr_list": {}, "repo_view": {},
	"run_jobs": {"run": 5}, "job_log": {"job": 6}, "contents": {"path": "docs/README.md"}, "stack_health": {"head": "loom/stack/"}}

// TestGitHubReadRepoScope: the repo is the one registered at the agent's
// clone path, never one the agent names; another path, a search widened
// past the repo or a path leaving the repo is refused.
func TestGitHubReadRepoScope(t *testing.T) {
	h := readHarness(t)
	h.addRepo(t, "other", "https://github.com/evil/other")
	h.rememberLocalPaths(t, "/clones", "hello", readRepoPath)
	for _, path := range []string{"/elsewhere/other", "/elsewhere", "", "/clones"} {
		_, err := h.module.GitHubRead(context.Background(), prReviewTestWorkspace, "a1", path, "repo_view", map[string]any{})
		if !errors.Is(err, domain.ErrNotOwner) {
			t.Errorf("repo path %q: err = %v; want refused", path, err)
		}
	}
	if _, err := h.module.GitHubRead(context.Background(), "OTHER", "a1", readRepoPath, "repo_view", map[string]any{}); err == nil {
		t.Error("another workspace's read succeeded")
	}
	if _, err := h.read(t, "repo_view", map[string]any{"owner": "evil", "repo": "other"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{{"query": "x repo:evil/other"}, {"query": "x OR org:evil"}, {"query": "-user:a x"}, {"query": "(owner:evil)"}, {}} {
		if _, err := h.read(t, "issue_search", args); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("search %v: err = %v; want invalid", args, err)
		}
	}
	for _, p := range []string{"../../evil/other/contents/x", "docs/../../x", "a b"} {
		if _, err := h.read(t, "contents", map[string]any{"path": p}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("contents %q: err = %v; want invalid", p, err)
		}
	}
	if _, err := h.read(t, "pr_search", map[string]any{"query": "fix"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.github.snapshot() {
		if !strings.HasPrefix(c.path, "/repos/octocat/hello") && c.path != "/search/issues" {
			t.Errorf("read left the bound repo: %s", c.path)
		}
		if c.path == "/search/issues" && !strings.Contains(c.query, "repo%3Aoctocat%2Fhello+is%3Apr") {
			t.Errorf("search query %q is not bound to the repo", c.query)
		}
	}
}

// TestGitHubReadCredentialIsolation: the host token authenticates the
// upstream request only; no result carries it, even when GitHub echoes it.
func TestGitHubReadCredentialIsolation(t *testing.T) {
	h := readHarness(t)
	for op, args := range readFixtureOps {
		body, err := h.read(t, op, args)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if raw, _ := json.Marshal(body); strings.Contains(string(raw), prReviewTestToken) {
			t.Errorf("%s result carries the host token", op)
		}
	}
	for _, c := range h.github.snapshot() {
		if c.path != "/blob/job-6.log" && c.authorization != "Bearer "+prReviewTestToken {
			t.Errorf("%s was not sent with the host credential", c.path)
		}
	}
}

// TestGitHubReadAgentCommandParity: `loom pr list` and `loom stack status`
// live health, which agents run today with their own gh or token, have
// typed github_read replacements carrying the same data.
func TestGitHubReadAgentCommandParity(t *testing.T) {
	h := readHarness(t)
	list, err := h.read(t, "pr_list", map[string]any{"state": "all"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := h.read(t, "pr_view", map[string]any{"number": 8})
	if err != nil {
		t.Fatal(err)
	}
	// prListJSONFields (internal/cli/git/pr_list.go) -> github_read.
	for field, from := range map[string]any{"number": dig(list, "items.0.number"), "title": dig(list, "items.0.title"),
		"url": dig(list, "items.0.html_url"), "state": dig(list, "items.0.state"), "isDraft": dig(list, "items.0.draft"),
		"headRefName": dig(list, "items.0.head.ref"), "baseRefName": dig(list, "items.0.base.ref"), "author": dig(list, "items.0.user.login"),
		"createdAt": dig(list, "items.0.created_at"), "updatedAt": dig(list, "items.0.updated_at"),
		"additions": dig(view, "item.additions"), "deletions": dig(view, "item.deletions"), "changedFiles": dig(view, "item.changed_files")} {
		if from == nil {
			t.Errorf("loom pr list %s has no github_read source", field)
		}
	}
	// `loom stack status` reads its live health from the forge; stack_health
	// returns the same per-PR health (and reviewDecision for pr list).
	want, err := stackpublish.NewGitHubForge(prReviewTestToken, nil, os.Getenv(connector.GitHubBaseURLEnvVar)).
		PRStatuses(context.Background(), "octocat", "hello", "loom/stack/s1/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.read(t, "stack_health", map[string]any{"head": "loom/stack/s1/"})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := got["items"].([]any)
	if len(items) != len(want) {
		t.Fatalf("stack_health = %v; want %v", items, want)
	}
	for _, it := range items {
		m := it.(map[string]any)
		w := want[m["head"].(string)]
		if m["number"] != w.Number || m["checks"] != w.Checks || m["review"] != w.Review || m["mergeable"] != w.Mergeable {
			t.Errorf("stack_health %v; want %+v", m, w)
		}
	}
}

// TestGitHubReadNoCredential: with the reader wired but no GitHub token on
// the host, github_read fails at once with a clear error, before any GitHub
// call.
func TestGitHubReadNoCredential(t *testing.T) {
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialNone, "")
	h.rememberLocalPaths(t, "/clones", "hello", readRepoPath)
	for _, op := range []string{"pr_view", "stack_health"} {
		start := time.Now()
		_, err := h.read(t, op, map[string]any{"number": 8})
		if !errors.Is(err, errNoGitHubToken) || time.Since(start) > 5*time.Second {
			t.Errorf("%s with no GitHub token: %v after %v; want errNoGitHubToken at once", op, err, time.Since(start))
		}
	}
}
