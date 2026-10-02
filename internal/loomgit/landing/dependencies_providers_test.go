package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

var (
	_ DependencyForge = (*stackpublish.GitLabForge)(nil)
	_ DependencyForge = (*stackpublish.BitbucketForge)(nil)
)

type providerPost struct {
	repo, sha string
	body      map[string]any
}

// gitlabFake serves the GitLab REST v4 shapes Loom reads and writes for
// owner/repo1 and owner/repo2.
type gitlabFake struct {
	t          *testing.T
	mrs        map[string]map[string]any // "repo1" -> merge request
	trains     map[string]map[string]any // "repo1" -> merge train car
	required   bool
	statuses   []providerPost
	responses  []providerPost
	rateLimits int
}

var gitlabRoute = regexp.MustCompile(`^/api/v4/projects/(?:owner|group%2Fsubgroup)%2F(repo[12])(/.*)?$`)

func (fake *gitlabFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	match := gitlabRoute.FindStringSubmatch(r.URL.EscapedPath())
	if match == nil {
		http.NotFound(w, r)
		return
	}
	repo, rest := match[1], match[2]
	mr := fake.mrs[repo]
	var body map[string]any
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fake.t.Error(err)
		}
	}
	switch {
	case r.Method == http.MethodGet && rest == "":
		writeJSON(w, http.StatusOK, map[string]any{"id": 2, "only_allow_merge_if_all_status_checks_passed": fake.required})
	case r.Method == http.MethodGet && rest == fmt.Sprintf("/merge_requests/%v", mr["iid"]):
		writeJSON(w, http.StatusOK, mr)
	case r.Method == http.MethodGet && rest == fmt.Sprintf("/merge_trains/merge_requests/%v", mr["iid"]):
		if fake.trains[repo] == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "404 Not found"})
			return
		}
		writeJSON(w, http.StatusOK, fake.trains[repo])
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/repository/commits/") && strings.HasSuffix(rest, "/merge_requests"):
		found := []map[string]any{}
		if strings.Contains(rest, "/"+mr["sha"].(string)+"/") {
			found = append(found, mr)
		}
		writeJSON(w, http.StatusOK, found)
	case r.Method == http.MethodPost && strings.HasPrefix(rest, "/statuses/"):
		if fake.rateLimits > 0 {
			fake.rateLimits--
			w.Header().Set("RateLimit-Remaining", "0")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"message": "429 Too Many Requests"})
			return
		}
		fake.statuses = append(fake.statuses, providerPost{repo: repo, sha: strings.TrimPrefix(rest, "/statuses/"), body: body})
		writeJSON(w, http.StatusCreated, map[string]any{"id": len(fake.statuses), "status": body["state"], "name": body["name"]})
	case r.Method == http.MethodGet && rest == "/external_status_checks":
		writeJSON(w, http.StatusOK, []map[string]any{{"id": 7, "name": "loom/dependencies", "project_id": 2,
			"external_url": "https://loom.example/status", "protected_branches": []any{}, "hmac": false}})
	case r.Method == http.MethodPost && rest == fmt.Sprintf("/merge_requests/%v/status_check_responses", mr["iid"]):
		fake.responses = append(fake.responses, providerPost{repo: repo, sha: body["sha"].(string), body: body})
		writeJSON(w, http.StatusCreated, map[string]any{"id": len(fake.responses), "merge_request": mr,
			"external_status_check": map[string]any{"id": body["external_status_check_id"]}})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "404 Not found"})
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func lastPost(t *testing.T, posts []providerPost, repo, sha string) map[string]any {
	t.Helper()
	for index := len(posts) - 1; index >= 0; index-- {
		if posts[index].repo == repo && posts[index].sha == sha {
			return posts[index].body
		}
	}
	t.Fatalf("no post on %s@%s: %+v", repo, sha, posts)
	return nil
}

func newGitLabDependencyFixture(t *testing.T) (*crossRepoFixture, *gitlabFake, Options) {
	t.Helper()
	return newGitLabDependencyFixtureAt(t, "owner")
}

func newGitLabDependencyFixtureAt(t *testing.T, owner string) (*crossRepoFixture, *gitlabFake, Options) {
	t.Helper()
	fixture := newCrossRepoFixtureAt(t, owner)
	fake := &gitlabFake{t: t, mrs: map[string]map[string]any{}, trains: map[string]map[string]any{}}
	for number, head := range []string{fixture.head1, fixture.head2} {
		fake.mrs[fmt.Sprintf("repo%d", number+1)] = map[string]any{"id": 100 + number, "iid": number + 1, "state": "opened",
			"source_branch": fmt.Sprintf("loom/ws/W/change/C%d", number+1), "target_branch": "main", "sha": head,
			"merge_commit_sha": nil, "squash_commit_sha": nil, "title": "change", "description": "",
			"web_url": fmt.Sprintf("https://gitlab.example/owner/repo%d/-/merge_requests/%d", number+1, number+1)}
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	forge := stackpublish.NewGitLabForge("token", server.Client(), server.URL+"/api/v4")
	return fixture, fake, Options{Predecessors: fixture.predecessors, Forge: forge}
}

func reconcileWith(t *testing.T, fixture *crossRepoFixture, options Options) error {
	t.Helper()
	return ReconcileWithOptions(context.Background(), fixture.store, options.Forge, options)
}

func landOnTrunk(t *testing.T, fixture *crossRepoFixture) string {
	t.Helper()
	merged := commitFile(t, fixture.repo1, "MR1 merged")
	git(t, fixture.repo1, "push", "-q", "origin", "main")
	return merged
}

func TestGitLabDependencyCheckPendingThenPassedAfterLanding(t *testing.T) {
	fixture, fake, options := newGitLabDependencyFixture(t)
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	status := lastPost(t, fake.statuses, "repo2", fixture.head2)
	if status["state"] != "pending" || status["name"] != "loom/dependencies" || !strings.Contains(status["description"].(string), "owner/repo1#1") {
		t.Fatalf("MR2 commit status = %+v", status)
	}
	if response := lastPost(t, fake.responses, "repo2", fixture.head2); response["status"] != "pending" || response["external_status_check_id"] != float64(7) {
		t.Fatalf("MR2 external status check = %+v", response)
	}
	for _, post := range append(fake.statuses, fake.responses...) {
		if post.repo != "repo2" {
			t.Fatalf("posted on MR1, which has no cross-repo predecessor: %+v", post)
		}
	}
	merged := landOnTrunk(t, fixture)
	fake.mrs["repo1"]["state"], fake.mrs["repo1"]["merge_commit_sha"] = "merged", merged
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	if status := lastPost(t, fake.statuses, "repo2", fixture.head2); status["state"] != "success" {
		t.Fatalf("MR2 commit status after MR1 landed = %+v", status)
	}
	if response := lastPost(t, fake.responses, "repo2", fixture.head2); response["status"] != "passed" {
		t.Fatalf("MR2 external status check after MR1 landed = %+v", response)
	}
	_, enforcement, err := fixture.store.DependencyChecks(context.Background())
	if err != nil || len(enforcement) != 1 || enforcement[0].State != "not_enforced" || !strings.Contains(enforcement[0].Reason, "not enforced") {
		t.Fatalf("enforcement = %+v, %v", enforcement, err)
	}
}

// A GitLab project in a nested group (group/subgroup/project) goes through
// landing detection and loom/dependencies like a top-level one.
func TestGitLabNestedGroupProjectPendingThenPassed(t *testing.T) {
	fixture, fake, options := newGitLabDependencyFixtureAt(t, "group/subgroup")
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	status := lastPost(t, fake.statuses, "repo2", fixture.head2)
	if status["state"] != "pending" || !strings.Contains(status["description"].(string), "group/subgroup/repo1#1") {
		t.Fatalf("MR2 commit status = %+v", status)
	}
	if response := lastPost(t, fake.responses, "repo2", fixture.head2); response["status"] != "pending" {
		t.Fatalf("MR2 external status check = %+v", response)
	}
	merged := landOnTrunk(t, fixture)
	fake.mrs["repo1"]["state"], fake.mrs["repo1"]["merge_commit_sha"] = "merged", merged
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	if status, err := fixture.store.LandingStatus(context.Background(), "W", "C1"); err != nil || status.State != "landed" {
		t.Fatalf("nested-group MR1 landing = %+v, %v", status, err)
	}
	if status := lastPost(t, fake.statuses, "repo2", fixture.head2); status["state"] != "success" {
		t.Fatalf("MR2 commit status after MR1 landed = %+v", status)
	}
	if response := lastPost(t, fake.responses, "repo2", fixture.head2); response["status"] != "passed" {
		t.Fatalf("MR2 external status check after MR1 landed = %+v", response)
	}
}

func TestGitLabMergeTrainIsQueuedNotLanded(t *testing.T) {
	fixture, fake, options := newGitLabDependencyFixture(t)
	fake.required = true
	train := func(sha string) map[string]any {
		return map[string]any{"id": 1, "status": "fresh", "target_branch": "main", "pipeline": map[string]any{"id": 9, "sha": sha, "ref": "refs/merge-requests/1/train"}}
	}
	fake.trains["repo1"] = train(strings.Repeat("1", 40))
	fake.trains["repo2"] = train(strings.Repeat("2", 40))
	for pass := 0; pass < 2; pass++ {
		if err := reconcileWith(t, fixture, options); err != nil {
			t.Fatal(err)
		}
	}
	if status, err := fixture.store.LandingStatus(context.Background(), "W", "C1"); err != nil || status.State == "landed" {
		t.Fatalf("MR1 in a merge train counted as landed: %+v, %v", status, err)
	}
	head, group := lastPost(t, fake.statuses, "repo2", fixture.head2), lastPost(t, fake.statuses, "repo2", strings.Repeat("2", 40))
	if head["state"] != "pending" || group["state"] != "pending" {
		t.Fatalf("MR2 while MR1 is only in a merge train: head %+v, train %+v", head, group)
	}
	_, enforcement, err := fixture.store.DependencyChecks(context.Background())
	if err != nil || len(enforcement) != 1 || enforcement[0].State != "not_pinned" {
		t.Fatalf("enforcement = %+v, %v", enforcement, err)
	}
}

func TestGitLabRateLimitedPostRetriesAndNeverShowsFalseSuccess(t *testing.T) {
	fixture, fake, options := newGitLabDependencyFixture(t)
	fake.rateLimits = 1
	if err := reconcileWith(t, fixture, options); err == nil || len(fake.statuses) != 0 {
		t.Fatalf("rate-limited post = %v, %+v", err, fake.statuses)
	}
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	merged := landOnTrunk(t, fixture)
	fake.mrs["repo1"]["state"], fake.mrs["repo1"]["merge_commit_sha"] = "merged", merged
	fake.rateLimits = 1
	if err := reconcileWith(t, fixture, options); err == nil {
		t.Fatal("rate-limited success post was not reported")
	}
	if status := lastPost(t, fake.statuses, "repo2", fixture.head2); status["state"] != "pending" {
		t.Fatalf("MR2 after a failed post = %+v", status)
	}
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	if status := lastPost(t, fake.statuses, "repo2", fixture.head2); status["state"] != "success" {
		t.Fatalf("retried success post = %+v", status)
	}
}

// bitbucketFake serves the Bitbucket Cloud 2.0 shapes Loom reads and writes.
// Pull requests carry 12-character hashes, as Bitbucket returns them.
type bitbucketFake struct {
	t            *testing.T
	pulls        map[string]map[string]any // "repo1" -> pull request
	commits      map[string]string         // short hash -> full hash
	restrictions []map[string]any
	statuses     []providerPost
	rateLimits   int
}

var bitbucketRoute = regexp.MustCompile(`^/2\.0/repositories/owner/(repo[12])(/.*)$`)

func (fake *bitbucketFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	match := bitbucketRoute.FindStringSubmatch(r.URL.Path)
	if match == nil {
		http.NotFound(w, r)
		return
	}
	repo, rest := match[1], match[2]
	pull := fake.pulls[repo]
	switch {
	case r.Method == http.MethodGet && rest == fmt.Sprintf("/pullrequests/%v", pull["id"]):
		writeJSON(w, http.StatusOK, pull)
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/commit/") && strings.Count(rest, "/") == 2:
		short := strings.TrimPrefix(rest, "/commit/")
		if full := fake.commits[short]; full != "" {
			writeJSON(w, http.StatusOK, map[string]any{"type": "commit", "hash": full})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"type": "error", "error": map[string]string{"message": "Commit not found"}})
	case r.Method == http.MethodPost && strings.HasPrefix(rest, "/commit/") && strings.HasSuffix(rest, "/statuses/build"):
		if fake.rateLimits > 0 {
			fake.rateLimits--
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"type": "error", "error": map[string]string{"message": "Rate limit for this resource has been exceeded"}})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fake.t.Error(err)
		}
		sha := strings.TrimSuffix(strings.TrimPrefix(rest, "/commit/"), "/statuses/build")
		fake.statuses = append(fake.statuses, providerPost{repo: repo, sha: sha, body: body})
		writeJSON(w, http.StatusCreated, map[string]any{"type": "build", "key": body["key"], "state": body["state"]})
	case r.Method == http.MethodGet && rest == "/branch-restrictions" && r.URL.Query().Get("kind") == "require_passing_builds_to_merge":
		writeJSON(w, http.StatusOK, map[string]any{"pagelen": 10, "page": 1, "size": len(fake.restrictions), "values": fake.restrictions})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"type": "error", "error": map[string]string{"message": "Resource not found"}})
	}
}

func bitbucketPull(number int, state, head string) map[string]any {
	return map[string]any{"type": "pullrequest", "id": number, "state": state, "title": "change", "description": "",
		"source":       map[string]any{"branch": map[string]string{"name": fmt.Sprintf("loom/ws/W/change/C%d", number)}, "commit": map[string]string{"hash": head[:12]}},
		"destination":  map[string]any{"branch": map[string]string{"name": "main"}, "commit": map[string]string{"hash": "0123456789ab"}},
		"merge_commit": nil,
		"links":        map[string]any{"html": map[string]string{"href": fmt.Sprintf("https://bitbucket.org/owner/repo%d/pull-requests/%d", number, number)}}}
}

func newBitbucketDependencyFixture(t *testing.T) (*crossRepoFixture, *bitbucketFake, Options) {
	t.Helper()
	fixture := newCrossRepoFixture(t)
	fake := &bitbucketFake{t: t, pulls: map[string]map[string]any{
		"repo1": bitbucketPull(1, "OPEN", fixture.head1), "repo2": bitbucketPull(2, "OPEN", fixture.head2)},
		commits: map[string]string{fixture.head1[:12]: fixture.head1, fixture.head2[:12]: fixture.head2}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	forge := stackpublish.NewBitbucketForge("token", server.Client(), server.URL+"/2.0")
	return fixture, fake, Options{Predecessors: fixture.predecessors, Forge: forge}
}

func TestBitbucketBuildStatusInProgressThenSuccessfulAfterLanding(t *testing.T) {
	fixture, fake, options := newBitbucketDependencyFixture(t)
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	status := lastPost(t, fake.statuses, "repo2", fixture.head2)
	if status["state"] != "INPROGRESS" || status["key"] != "loom/dependencies" || !strings.Contains(status["description"].(string), "owner/repo1#1") {
		t.Fatalf("PR2 build status = %+v", status)
	}
	for _, post := range fake.statuses {
		if post.repo != "repo2" {
			t.Fatalf("posted on PR1, which has no cross-repo predecessor: %+v", post)
		}
	}
	merged := landOnTrunk(t, fixture)
	fake.commits[merged[:12]] = merged
	fake.pulls["repo1"]["state"], fake.pulls["repo1"]["merge_commit"] = "MERGED", map[string]string{"hash": merged[:12]}
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	if status := lastPost(t, fake.statuses, "repo2", fixture.head2); status["state"] != "SUCCESSFUL" {
		t.Fatalf("PR2 build status after PR1 landed = %+v", status)
	}
	if status, err := fixture.store.LandingStatus(context.Background(), "W", "C1"); err != nil || status.State != "landed" {
		t.Fatalf("PR1 landing = %+v, %v", status, err)
	}
	_, enforcement, err := fixture.store.DependencyChecks(context.Background())
	if err != nil || len(enforcement) != 1 || enforcement[0].State != "not_enforced" || !strings.Contains(enforcement[0].Reason, "not enforced") {
		t.Fatalf("enforcement = %+v, %v", enforcement, err)
	}
}

func TestBitbucketRateLimitedPostRetriesAndNeverShowsFalseSuccess(t *testing.T) {
	fixture, fake, options := newBitbucketDependencyFixture(t)
	fake.restrictions = []map[string]any{{"type": "branchrestriction", "id": 3, "kind": "require_passing_builds_to_merge",
		"branch_match_kind": "glob", "pattern": "main", "value": 1}}
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	merged := landOnTrunk(t, fixture)
	fake.commits[merged[:12]] = merged
	fake.pulls["repo1"]["state"], fake.pulls["repo1"]["merge_commit"] = "MERGED", map[string]string{"hash": merged[:12]}
	fake.rateLimits = 1
	if err := reconcileWith(t, fixture, options); err == nil {
		t.Fatal("rate-limited success post was not reported")
	}
	if status := lastPost(t, fake.statuses, "repo2", fixture.head2); status["state"] != "INPROGRESS" {
		t.Fatalf("PR2 after a failed post = %+v", status)
	}
	if err := reconcileWith(t, fixture, options); err != nil {
		t.Fatal(err)
	}
	if status := lastPost(t, fake.statuses, "repo2", fixture.head2); status["state"] != "SUCCESSFUL" {
		t.Fatalf("retried success post = %+v", status)
	}
	_, enforcement, err := fixture.store.DependencyChecks(context.Background())
	if err != nil || len(enforcement) != 1 || enforcement[0].State != "not_enforced" {
		t.Fatalf("enforcement = %+v, %v", enforcement, err)
	}
}
