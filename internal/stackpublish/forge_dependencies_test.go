package stackpublish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type dependencyServer struct {
	checkRunCode int
	rateLimited  bool
	checkRuns    []map[string]any
	statuses     []map[string]string
	branch       string
	rules        string
}

func (server *dependencyServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/check-runs":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if server.rateLimited {
				w.Header().Set("X-RateLimit-Remaining", "0")
			}
			if server.checkRunCode == http.StatusCreated {
				server.checkRuns = append(server.checkRuns, body)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1,"app":{"id":42}}`))
				return
			}
			w.WriteHeader(server.checkRunCode)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/statuses/abc123":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			server.statuses = append(server.statuses, body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/branches/main":
			_, _ = w.Write([]byte(server.branch))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/rules/branches/main":
			_, _ = w.Write([]byte(server.rules))
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"headCommit":{"oid":"merge-group-sha"}}}}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newDependencyForge(t *testing.T, server *dependencyServer) *GitHubForge {
	t.Helper()
	test := httptest.NewServer(server.handler(t))
	t.Cleanup(test.Close)
	return NewGitHubForge("test", test.Client(), test.URL)
}

func TestPostDependencyStatusUsesAppCheckRun(t *testing.T) {
	server := &dependencyServer{checkRunCode: http.StatusCreated}
	forge := newDependencyForge(t, server)
	ctx := context.Background()
	if app, err := forge.PostDependencyStatus(ctx, "owner", "repo", "abc123", DependencyStatus{State: "pending", Description: "Waiting for owner/api#7 to land"}); err != nil || app != 42 {
		t.Fatalf("check run app = %d, %v", app, err)
	}
	if _, err := forge.PostDependencyStatus(ctx, "owner", "repo", "abc123", DependencyStatus{State: "success", Description: "All cross-repo predecessors landed"}); err != nil {
		t.Fatal(err)
	}
	if len(server.checkRuns) != 2 || len(server.statuses) != 0 {
		t.Fatalf("check runs = %+v, statuses = %+v", server.checkRuns, server.statuses)
	}
	pending, success := server.checkRuns[0], server.checkRuns[1]
	if pending["name"] != DependencyCheckName || pending["head_sha"] != "abc123" || pending["status"] != "in_progress" || pending["conclusion"] != nil {
		t.Fatalf("pending check run = %+v", pending)
	}
	if success["status"] != "completed" || success["conclusion"] != "success" {
		t.Fatalf("success check run = %+v", success)
	}
}

func TestPostDependencyStatusFallsBackToCommitStatusWithoutApp(t *testing.T) {
	server := &dependencyServer{checkRunCode: http.StatusForbidden}
	forge := newDependencyForge(t, server)
	long := "Waiting for " + strings.Repeat("owner/api#7 to land; ", 10)
	if app, err := forge.PostDependencyStatus(context.Background(), "owner", "repo", "abc123", DependencyStatus{State: "pending", Description: long}); err != nil || app != 0 {
		t.Fatalf("commit status app = %d, %v", app, err)
	}
	if len(server.statuses) != 1 || server.statuses[0]["context"] != DependencyCheckName || server.statuses[0]["state"] != "pending" ||
		len(server.statuses[0]["description"]) > 140 {
		t.Fatalf("statuses = %+v", server.statuses)
	}
}

func TestPostDependencyStatusRateLimitIsAnError(t *testing.T) {
	server := &dependencyServer{checkRunCode: http.StatusForbidden, rateLimited: true}
	forge := newDependencyForge(t, server)
	_, err := forge.PostDependencyStatus(context.Background(), "owner", "repo", "abc123", DependencyStatus{State: "success", Description: "ok"})
	if err == nil || len(server.statuses) != 0 {
		t.Fatalf("rate-limited post = %v, statuses = %+v", err, server.statuses)
	}
}

func TestMergeQueueHead(t *testing.T) {
	forge := newDependencyForge(t, &dependencyServer{})
	sha, err := forge.MergeQueueHead(context.Background(), "owner", "repo", 2)
	if err != nil || sha != "merge-group-sha" {
		t.Fatalf("merge queue head = %q, %v", sha, err)
	}
}

func TestDependencyEnforcement(t *testing.T) {
	for _, test := range []struct {
		name, branch, rules, want string
	}{
		{"no protection", `{"protected":false}`, `[]`, "not_enforced"},
		{"other checks only", `{"protection":{"required_status_checks":{"contexts":["ci"],"checks":[{"context":"ci","app_id":15368}]}}}`, `[]`, "not_enforced"},
		{"any source", `{"protection":{"required_status_checks":{"contexts":["loom/dependencies"],"checks":[{"context":"loom/dependencies","app_id":null}]}}}`, `[]`, "not_pinned"},
		{"classic pinned", `{"protection":{"required_status_checks":{"contexts":["loom/dependencies"],"checks":[{"context":"loom/dependencies","app_id":42}]}}}`, `[]`, "enforced"},
		{"classic other app", `{"protection":{"required_status_checks":{"contexts":["loom/dependencies"],"checks":[{"context":"loom/dependencies","app_id":7}]}}}`, `[]`, "wrong_app"},
		{"ruleset any source", `{}`, `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"loom/dependencies"}]}}]`, "not_pinned"},
		{"ruleset pinned", `{}`, `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"loom/dependencies","integration_id":42}]}}]`, "enforced"},
		{"ruleset other app", `{}`, `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"loom/dependencies","integration_id":7}]}}]`, "wrong_app"},
		{"classic Loom, ruleset other app", `{"protection":{"required_status_checks":{"checks":[{"context":"loom/dependencies","app_id":42}]}}}`, `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"loom/dependencies","integration_id":7}]}}]`, "wrong_app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			forge := newDependencyForge(t, &dependencyServer{branch: test.branch, rules: test.rules})
			state, err := forge.DependencyEnforcement(context.Background(), "owner", "repo", "main", 42)
			if err != nil || state != test.want {
				t.Fatalf("enforcement = %q, %v; want %q", state, err, test.want)
			}
		})
	}
}

func TestDependencyEnforcementPinnedWithoutLoomApp(t *testing.T) {
	forge := newDependencyForge(t, &dependencyServer{branch: `{"protection":{"required_status_checks":{"checks":[{"context":"loom/dependencies","app_id":42}]}}}`, rules: `[]`})
	state, err := forge.DependencyEnforcement(context.Background(), "owner", "repo", "main", 0)
	if err != nil || state != "wrong_app" {
		t.Fatalf("pinned check with Loom posting commit statuses = %q, %v", state, err)
	}
}
