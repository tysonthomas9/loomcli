package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type nativeAPI struct {
	prs          []map[string]any
	stack        []int
	create       int
	lostResponse bool
	baseUpdates  int
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
		api.createPR(writer, request)
	case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/stacks"):
		api.listStacks(writer)
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/stacks"):
		api.createStack(writer, request)
	case request.Method == http.MethodPatch && strings.Contains(request.URL.Path, "/pulls/"):
		api.baseUpdates++
		writer.WriteHeader(http.StatusOK)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (api *nativeAPI) createPR(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(request.Body).Decode(&body)
	api.prs = append(api.prs, map[string]any{"number": len(api.prs) + 1, "state": "open", "head": map[string]any{"ref": body["head"]}, "base": map[string]any{"ref": body["base"]}, "html_url": fmt.Sprintf("https://example.test/pull/%d", len(api.prs)+1), "body": body["body"]})
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(api.prs[len(api.prs)-1])
}

func (api *nativeAPI) listStacks(writer http.ResponseWriter) {
	if len(api.stack) == 0 {
		_, _ = writer.Write([]byte(`[]`))
		return
	}
	pulls := make([]map[string]int, 0, len(api.stack))
	for _, number := range api.stack {
		pulls = append(pulls, map[string]int{"number": number})
	}
	_ = json.NewEncoder(writer).Encode([]map[string]any{{"number": 1, "pull_requests": pulls}})
}

func (api *nativeAPI) createStack(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		PullRequests []int `json:"pull_requests"`
	}
	_ = json.NewDecoder(request.Body).Decode(&body)
	api.stack = body.PullRequests
	api.create++
	if api.lostResponse {
		api.lostResponse = false
		connection, _, _ := writer.(http.Hijacker).Hijack()
		_ = connection.Close()
		return
	}
	writer.WriteHeader(http.StatusCreated)
}

func TestRecordedNativeStackAdoptsAfterLostResponseAndRestart(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	stackRevision(t, fixture, "C", 1, second.HeadSHA)
	ctx := context.Background()
	changes := []string{"A", "B", "C"}
	for _, change := range changes {
		if _, err := fixture.store.DriverChange(ctx, "W", change, "repo", change); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo", Path: fixture.repo, BaseSHA: fixture.base}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.LoomConfig{Workspaces: map[string]config.WorkspaceConfig{"workspace": {ID: "W", Path: fixture.repo, Repos: []config.RepoConfig{{Name: "repo", Path: fixture.repo}}}}}
	api := &nativeAPI{lostResponse: true}
	server := httptest.NewServer(http.HandlerFunc(api.serve))
	defer server.Close()
	forge := stackpublish.NewGitHubForge("fixture-token", server.Client(), server.URL)
	if _, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature-1", "L", changes, forge, "fixture-token", "owner/repo"); err == nil {
		t.Fatal("lost Stack response unexpectedly succeeded")
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := journal.OpenSQLite(fixture.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := publishStackRecorded(ctx, store, cfg, "W", "feature-1", "L", changes, forge, "fixture-token", "owner/repo"); err != nil {
		t.Fatal(err)
	}
	if api.create != 1 || len(api.prs) != 3 || len(api.stack) != 3 || api.stack[0] != 1 || api.stack[1] != 2 || api.stack[2] != 3 {
		t.Fatalf("PRs = %+v, stack = %v, creates = %d", api.prs, api.stack, api.create)
	}
	for index, pr := range api.prs {
		base := "develop"
		if index > 0 {
			base, _ = refname.ChangeBranch("W", changes[index-1])
		}
		if pr["base"].(map[string]any)["ref"] != base {
			t.Fatalf("PR %d base = %v, want %s", index+1, pr["base"], base)
		}
	}
}

func TestRecordedNativeStackStaleLeaseLeavesStackAndBases(t *testing.T) {
	fixture := newFixture(t)
	first := stackRevision(t, fixture, "A", 1, fixture.base)
	second := stackRevision(t, fixture, "B", 1, first.HeadSHA)
	stackRevision(t, fixture, "C", 1, second.HeadSHA)
	ctx := context.Background()
	changes := []string{"A", "B", "C"}
	for _, change := range changes {
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
	if _, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature-1", "L", changes, forge, "fixture-token", "owner/repo"); err != nil {
		t.Fatal(err)
	}
	firstNew := stackRevision(t, fixture, "A", 2, fixture.base)
	secondNew := stackRevision(t, fixture, "B", 2, firstNew.HeadSHA)
	stackRevision(t, fixture, "C", 2, secondNew.HeadSHA)
	branch, _ := refname.ChangeBranch("W", "B")
	git(t, fixture.repo, "push", "--force", fixture.remote, fixture.base+":refs/heads/"+branch)
	_, err := publishStackRecorded(ctx, fixture.store, cfg, "W", "feature-1", "L", changes, forge, "fixture-token", "owner/repo")
	codeIs(t, err, loomgit.Diverged)
	if api.create != 1 || len(api.stack) != 3 || len(api.prs) != 3 || api.baseUpdates != 0 {
		t.Fatalf("stale lease changed Stack or PRs: stack=%v prs=%v creates=%d base updates=%d", api.stack, api.prs, api.create, api.baseUpdates)
	}
	firstBranch, _ := refname.ChangeBranch("W", "A")
	if got := git(t, fixture.remote, "rev-parse", "refs/heads/"+firstBranch); got != first.HeadSHA {
		t.Fatalf("first branch moved to %s after stale lease", got)
	}
}

// nativeFourLayerStack publishes A-D as one native stack, with each PR based on
// its predecessor's branch as GitHub stacks are.
func nativeFourLayerStack(t *testing.T) (fixture, *fakeForge, []loomgit.Revision) {
	t.Helper()
	fixture, ctx := newFixture(t, "main"), context.Background()
	revisions, parent := []loomgit.Revision{}, fixture.base
	for _, change := range []string{"A", "B", "C", "D"} {
		revision := stackRevision(t, fixture, change, 1, parent)
		revisions, parent = append(revisions, revision), revision.HeadSHA
	}
	git(t, fixture.repo, "branch", "-m", "loom/ws/W/interactive/L")
	if err := fixture.store.SaveWorkingAreas(ctx, []journal.WorkingArea{{Workspace: "W", Lead: "L", Repo: "repo",
		Path: fixture.repo, Branch: "loom/ws/W/interactive/L", BaseSHA: fixture.base, Mode: "worktree"}}); err != nil {
		t.Fatal(err)
	}
	forge, request := &fakeForge{}, fixture.request()
	request.forge = forge
	if _, err := publishStack(ctx, fixture.store, StackRequest{Request: request, StackID: "feature-1",
		Changes: []string{"A", "B", "C", "D"}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordStackBackend(ctx, "W", "feature-1", "native"); err != nil {
		t.Fatal(err)
	}
	if forge.prs[2].Base != forge.prs[1].Head {
		t.Fatalf("C PR base = %s, want predecessor branch %s", forge.prs[2].Base, forge.prs[1].Head)
	}
	t.Setenv("LOOM_CONFIG_DIR", filepath.Dir(fixture.repo))
	t.Setenv("GITHUB_TOKEN", "fixture-token")
	return fixture, forge, revisions
}

// squashOnto commits one new squash commit per change onto start in a scratch
// worktree, pushes the result to branch, and returns the new commits.
func squashOnto(t *testing.T, fixture fixture, start, branch string, changes ...string) []string {
	t.Helper()
	trunk := filepath.Join(t.TempDir(), "trunk")
	git(t, fixture.repo, "worktree", "add", "-q", "--detach", trunk, start)
	commits := make([]string, 0, len(changes))
	for _, change := range changes {
		if err := os.WriteFile(filepath.Join(trunk, change), []byte(change+"1"), 0600); err != nil {
			t.Fatal(err)
		}
		git(t, trunk, "add", change)
		git(t, trunk, "commit", "-qm", "squash "+change)
		commits = append(commits, git(t, trunk, "rev-parse", "HEAD"))
	}
	git(t, trunk, "push", "-q", "--force", "origin", "HEAD:refs/heads/"+branch)
	return commits
}

func mergePR(pr *stackpublish.PR, mergeCommit string) {
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", mergeCommit
}

func TestLandingRecordsSquashedStackLayersOnTrunkAndRestacksNative(t *testing.T) {
	fixture, forge, revisions := nativeFourLayerStack(t)
	ctx := context.Background()
	squashes := squashOnto(t, fixture, fixture.base, "main", "A", "B", "C")
	for index := range squashes {
		mergePR(&forge.prs[index], squashes[index])
	}
	provider := squashOnto(t, fixture, squashes[2], forge.prs[3].Head, "D")[0]
	forge.prs[3].Base, forge.prs[3].HeadSHA = "main", provider
	if err := fixture.store.OfferRestack(ctx, journal.RestackOffer{Workspace: "W", Change: "D", Predecessor: "C",
		Repo: "repo", Revision: revisions[3].Number, TrunkSHA: squashes[2]}); err != nil {
		t.Fatal(err)
	}
	if err := landing.ReconcileWithOptions(ctx, fixture.store, forge, landing.Options{Restack: RestackOffer}); err != nil {
		t.Fatalf("native restack after stacked landing: %v", err)
	}
	for _, change := range []string{"A", "B", "C"} {
		if status, err := fixture.store.LandingStatus(ctx, "W", change); err != nil || status.State != "landed" {
			t.Fatalf("%s landing = %+v, %v", change, status, err)
		}
	}
	publication, found, err := fixture.store.Publication(ctx, "W", "D")
	if err != nil || !found || publication.Head != provider || publication.Trunk != "main" {
		t.Fatalf("restacked D publication = %+v, %v", publication, err)
	}
	if revision, err := fixture.store.SourceRevision(ctx, "W", "D"); err != nil || revision <= revisions[3].Number {
		t.Fatalf("D derived revision = %d, %v", revision, err)
	}
}

func TestLandingIgnoresMergeIntoPredecessorBranchOffTrunk(t *testing.T) {
	fixture, forge, revisions := nativeFourLayerStack(t)
	ctx := context.Background()
	squashes := squashOnto(t, fixture, fixture.base, "main", "A", "B")
	mergePR(&forge.prs[0], squashes[0])
	mergePR(&forge.prs[1], squashes[1])
	intoB := squashOnto(t, fixture, revisions[1].HeadSHA, forge.prs[1].Head, "C")[0]
	mergePR(&forge.prs[2], intoB)
	if err := landing.ReconcileWithOptions(ctx, fixture.store, forge, landing.Options{}); err != nil {
		t.Fatal(err)
	}
	for change, want := range map[string]string{"A": "landed", "B": "landed", "C": "merged", "D": "published"} {
		if status, err := fixture.store.LandingStatus(ctx, "W", change); err != nil || status.State != want {
			t.Fatalf("%s landing = %+v, %v; want %s", change, status, err, want)
		}
	}
}
