package agentsv1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type fakeWatcher struct{ calls []string }

func (f *fakeWatcher) Watch(_ context.Context, ws, agentID, repoPath string, number int) (loomstore.PRWatch, bool, error) {
	f.calls = append(f.calls, "watch "+ws+" "+agentID+" "+repoPath)
	return loomstore.PRWatch{PRWatchKey: loomstore.PRWatchKey{AgentID: agentID, Owner: "octocat", Repo: "hello", Number: number},
		WorkspaceID: ws, Viewer: "loom-host"}, true, nil
}

func (f *fakeWatcher) Unwatch(_ context.Context, ws, agentID, repoPath string, _ int) (bool, error) {
	f.calls = append(f.calls, "unwatch "+ws+" "+agentID+" "+repoPath)
	return true, nil
}

// TestGitHubWatchRoutes: an agent with github_read registers and removes a
// PR watch on its own repo; the repo and agent come from its token, an agent
// without the tool or a bad number is refused, and a host without the PR
// watcher answers unavailable.
func TestGitHubWatchRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := loomstore.Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	task := testAgent("t1", loomagent.StateIdle)
	task.Preset, task.Mode = "task", "single_task"
	for _, a := range []loomstore.Agent{testAgent("a1", loomagent.StateIdle), task} {
		if err := st.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, WorkspaceID: "ws"})
	tokens := NewTokens([]byte(strings.Repeat("k", 32)))
	ws := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithWorkspace(r.Context(), r.PathValue("ws"))))
		})
	}
	serve := func(w PRWatcher) *httptest.Server {
		mux := http.NewServeMux()
		h := New(func(string) *loomagent.Service { return svc }, nil).WithTokens(tokens)
		if w != nil {
			h.WithPRWatch(w)
		}
		h.Register(mux, ws, nil)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return srv
	}
	fw := &fakeWatcher{}
	srv := serve(fw)
	a1 := "Bearer " + tokens.Agent("ws", "a1")

	status, out := as(t, srv, a1, "POST", "ws/v1/github/watch", `{"number":8}`)
	want(t, "watch", status, out, 200, "")
	if out["owner"] != "octocat" || out["repo"] != "hello" || out["number"] != float64(8) || out["viewer"] != "loom-host" || out["created"] != true {
		t.Fatalf("watch = %v", out)
	}
	status, out = as(t, srv, a1, "POST", "ws/v1/github/unwatch", `{"number":8}`)
	want(t, "unwatch", status, out, 200, "")
	if out["removed"] != true {
		t.Fatalf("unwatch = %v", out)
	}
	if strings.Join(fw.calls, ";") != "watch ws a1 /repo;unwatch ws a1 /repo" {
		t.Fatalf("watcher calls = %v", fw.calls)
	}

	status, out = as(t, srv, a1, "POST", "ws/v1/github/watch", `{"number":0}`)
	want(t, "watch #0", status, out, 400, string(CodeGitHubInvalid))
	status, out = as(t, srv, "Bearer "+tokens.Agent("ws", "t1"), "POST", "ws/v1/github/watch", `{"number":8}`)
	want(t, "watch without github_read", status, out, 403, string(CodeGitHubDenied))
	status, out = as(t, srv, "", "POST", "ws/v1/github/watch", `{"number":8}`)
	want(t, "watch by the user", status, out, 403, string(CodeGitHubDenied))
	status, out = as(t, serve(nil), a1, "POST", "ws/v1/github/watch", `{"number":8}`)
	want(t, "watch with no host watcher", status, out, 503, string(CodeGitHubUnavailable))
	if len(fw.calls) != 2 {
		t.Fatalf("refused calls reached the watcher: %v", fw.calls)
	}
}
