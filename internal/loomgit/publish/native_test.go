package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type nativeAPI struct {
	prs    []map[string]any
	stack  []int
	create int
}

func (api *nativeAPI) serve(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("X-GitHub-Api-Version") == "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Query().Get("per_page") == "1":
		_, _ = writer.Write([]byte(`[]`))
	case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/pulls"):
		_ = json.NewEncoder(writer).Encode(api.prs)
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/pulls"):
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		api.prs = append(api.prs, map[string]any{"number": len(api.prs) + 1, "state": "open", "head": map[string]any{"ref": body["head"]}, "base": map[string]any{"ref": body["base"]}, "html_url": fmt.Sprintf("https://example.test/pull/%d", len(api.prs)+1), "body": body["body"]})
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(api.prs[len(api.prs)-1])
	case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/stacks"):
		if len(api.stack) == 0 {
			_, _ = writer.Write([]byte(`[]`))
			return
		}
		_, _ = writer.Write([]byte(`[ {"number":1,"pull_requests":[{"number":1},{"number":2}]} ]`))
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/stacks"):
		var body struct {
			PullRequests []int `json:"pull_requests"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		api.stack = body.PullRequests
		api.create++
		writer.WriteHeader(http.StatusCreated)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func TestRecordedNativeStackUsesGitHubAPIWithoutDuplicate(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	stackRevision(t, fixture, "B", 1, first.HeadSHA)
	ctx := context.Background()
	for _, change := range []string{"A", "B"} {
		if _, err := fixture.store.DriverChange(ctx, "W", change, "repo", change); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}}}}
	api := &nativeAPI{}
	server := httptest.NewServer(http.HandlerFunc(api.serve))
	defer server.Close()
	forge := stackpublish.NewGitHubForge("fixture-token", server.Client(), server.URL)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature-1", "L", []string{"A", "B"}, forge, "fixture-token", "owner/repo"); err != nil {
			t.Fatal(err)
		}
	}
	if api.create != 1 || len(api.prs) != 2 || len(api.stack) != 2 || api.stack[0] != 1 || api.stack[1] != 2 {
		t.Fatalf("PRs = %+v, stack = %v, creates = %d", api.prs, api.stack, api.create)
	}
}
