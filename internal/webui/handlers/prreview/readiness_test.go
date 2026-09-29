package prreview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/prreadiness"
)

// ---- fake clock -----------------------------------------------------------

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var readinessT0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// ---- fake GitHub GraphQL ----------------------------------------------------

// fakeRepoFailure makes the fake answer a repository's readiness query with
// an error (or block until the client gives up when slow is set).
type fakeRepoFailure struct {
	status int
	header map[string]string
	body   any
	slow   bool
}

// fakeReadiness serves the readiness GraphQL query from per-repo PR nodes.
type fakeReadiness struct {
	mu       sync.Mutex
	nodes    map[string]map[int]map[string]any // "owner/repo" -> number -> node
	failures map[string]fakeRepoFailure
	queries  map[string][][]int // "owner/repo" -> numbers per query
	// onQuery runs before each answer (e.g. to advance the fake clock, as
	// real network time would).
	onQuery func(repo string, numbers []int)
}

var readinessAliasRE = regexp.MustCompile(`pr_(\d+): pullRequest`)

func installFakeReadiness(h *prReviewHarness) *fakeReadiness {
	f := &fakeReadiness{
		nodes:    map[string]map[int]map[string]any{},
		failures: map[string]fakeRepoFailure{},
		queries:  map[string][][]int{},
	}
	h.github.mu.Lock()
	h.github.graphql = f.serve
	h.github.mu.Unlock()
	return f
}

func (f *fakeReadiness) setNode(repo string, node map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nodes[repo] == nil {
		f.nodes[repo] = map[int]map[string]any{}
	}
	f.nodes[repo][node["number"].(int)] = node
}

func (f *fakeReadiness) setFailure(repo string, fail *fakeRepoFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fail == nil {
		delete(f.failures, repo)
		return
	}
	f.failures[repo] = *fail
}

func (f *fakeReadiness) queryCount(repo string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries[repo])
}

func (f *fakeReadiness) lastQuery(repo string) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.queries[repo]
	if len(q) == 0 {
		return nil
	}
	return q[len(q)-1]
}

func (f *fakeReadiness) serve(r *http.Request, body map[string]any) (int, map[string]string, any) {
	query, _ := body["query"].(string)
	vars, _ := body["variables"].(map[string]any)
	owner, _ := vars["owner"].(string)
	name, _ := vars["repo"].(string)
	repo := owner + "/" + name
	var numbers []int
	for _, m := range readinessAliasRE.FindAllStringSubmatch(query, -1) {
		n, _ := strconv.Atoi(m[1])
		numbers = append(numbers, n)
	}
	f.mu.Lock()
	f.queries[repo] = append(f.queries[repo], numbers)
	fail, failing := f.failures[repo]
	onQuery := f.onQuery
	f.mu.Unlock()
	if onQuery != nil {
		onQuery(repo, numbers)
	}
	if failing {
		if fail.slow {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return http.StatusGatewayTimeout, nil, map[string]any{"message": "slow"}
		}
		return fail.status, fail.header, fail.body
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	data := map[string]any{}
	var errs []any
	for _, n := range numbers {
		alias := "pr_" + strconv.Itoa(n)
		node, ok := f.nodes[repo][n]
		if !ok {
			data[alias] = nil
			errs = append(errs, map[string]any{
				"type": "NOT_FOUND", "path": []any{"repository", alias},
				"message": "Could not resolve to a PullRequest with the number of " + strconv.Itoa(n) + ".",
			})
			continue
		}
		data[alias] = node
	}
	resp := map[string]any{"data": map[string]any{
		"repository": data,
		"rateLimit":  map[string]any{"cost": 1, "remaining": 4999, "resetAt": "2026-09-24T13:00:00Z"},
	}}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	return http.StatusOK, nil, resp
}

// readyNode is an open, mergeable, clean PR with one passing required check.
func readyNode(number int, head, headRef, baseRef string) map[string]any {
	return map[string]any{
		"number": number, "id": "PR_" + strconv.Itoa(number), "state": "OPEN",
		"isDraft": false, "merged": false,
		"headRefName": headRef, "headRefOid": head,
		"baseRefName": baseRef, "baseRefOid": "base-" + baseRef,
		"mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "reviewDecision": "APPROVED",
		"isInMergeQueue": false, "mergeQueueEntry": nil,
		"commits": checksCommits(head, map[string]any{
			"__typename": "CheckRun", "name": "ci", "status": "COMPLETED", "conclusion": "SUCCESS", "isRequired": true,
		}),
	}
}

func checksCommits(head string, contexts ...map[string]any) map[string]any {
	nodes := make([]any, 0, len(contexts))
	for _, c := range contexts {
		nodes = append(nodes, c)
	}
	return map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
		"oid": head,
		"statusCheckRollup": map[string]any{"state": "SUCCESS", "contexts": map[string]any{
			"totalCount": len(nodes), "pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes,
		}},
	}}}}
}

// ---- harness helpers --------------------------------------------------------

func newReadinessHarness(t *testing.T) (*prReviewHarness, *fakeReadiness, *fakeClock) {
	t.Helper()
	h := newPRReviewHarness(t, true)
	clock := &fakeClock{t: readinessT0}
	h.module.now = clock.now
	h.module.readinessBackoff = []time.Duration{0}
	f := installFakeReadiness(h)
	return h, f, clock
}

func readinessPath(force bool, keys ...string) string {
	q := url.Values{}
	for _, k := range keys {
		q.Add("pr", k)
	}
	if force {
		q.Set("force", "true")
	}
	return "/api/workspaces/WS/pull-requests/readiness?" + q.Encode()
}

func previewPath(keys ...string) string {
	q := url.Values{}
	for _, k := range keys {
		q.Add("pr", k)
	}
	return "/api/workspaces/WS/pull-requests/readiness/preview?" + q.Encode()
}

// decodeStrict decodes the success envelope and then decodes data into T
// with DisallowUnknownFields, proving the wire shape matches the generated
// OpenAPI type.
func decodeStrict[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var env struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v (body %s)", err, raw)
	}
	if !env.Success {
		t.Fatalf("response not successful: %s", raw)
	}
	var out T
	dec := json.NewDecoder(bytes.NewReader(env.Data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("data does not match OpenAPI schema: %v (data %s)", err, env.Data)
	}
	return out
}

func getReadiness(t *testing.T, h *prReviewHarness, force bool, keys ...string) gen.PullRequestReadinessList {
	t.Helper()
	status, raw := h.get(t, readinessPath(force, keys...))
	if status != http.StatusOK {
		t.Fatalf("readiness status = %d, want 200 (body %s)", status, raw)
	}
	return decodeStrict[gen.PullRequestReadinessList](t, raw)
}

func viewFor(t *testing.T, list gen.PullRequestReadinessList, key string) gen.PullRequestReadinessView {
	t.Helper()
	for _, v := range list.PullRequests {
		if v.PrKey == key {
			return v
		}
	}
	t.Fatalf("no view for %s in %+v", key, list.PullRequests)
	return gen.PullRequestReadinessView{}
}

func repoErrorFor(list []gen.PullRequestReadinessRepoError, repo string) *gen.PullRequestReadinessRepoError {
	for i := range list {
		if list[i].Repo == repo {
			return &list[i]
		}
	}
	return nil
}

// assertReadOnlyGitHub proves the readiness surfaces never write to GitHub:
// every upstream call is a GET or the read-only GraphQL query.
func assertReadOnlyGitHub(t *testing.T, h *prReviewHarness) {
	t.Helper()
	for _, c := range h.github.snapshot() {
		if c.method == http.MethodGet {
			continue
		}
		if c.method == http.MethodPost && c.path == "/graphql" {
			if q, _ := c.body["query"].(string); strings.Contains(strings.ToLower(q), "mutation") {
				t.Errorf("GraphQL call contains a mutation: %s", q)
			}
			continue
		}
		t.Errorf("unexpected GitHub write: %s %s", c.method, c.path)
	}
}

func assertVerdict(t *testing.T, v gen.PullRequestReadinessView, verdict string, reasons ...string) {
	t.Helper()
	if string(v.CurrentVerdict) != verdict {
		t.Errorf("%s current_verdict = %q, want %q (reasons %v)", v.PrKey, v.CurrentVerdict, verdict, v.CurrentReasons)
	}
	if reasons == nil {
		reasons = []string{}
	}
	if !slices.Equal(v.CurrentReasons, reasons) {
		t.Errorf("%s current_reasons = %v, want %v", v.PrKey, v.CurrentReasons, reasons)
	}
}

const (
	keyHello7 = "github:octocat/hello#7"
	keyHello8 = "github:octocat/hello#8"
	keyWorld3 = "github:octocat/world#3"
)

// ---- tests --------------------------------------------------------------------

func TestReadinessHappyPathPinsSnapshot(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))

	list := getReadiness(t, h, false, keyHello7, "github:octocat/hello#404")

	if !list.ServerNow.Equal(clock.now()) || list.FreshForS != 60 || list.StaleAfterS != 600 {
		t.Fatalf("envelope = now %v fresh %d stale %d, want %v/60/600", list.ServerNow, list.FreshForS, list.StaleAfterS, clock.now())
	}
	if len(list.RepoErrors) != 0 {
		t.Fatalf("repo_errors = %+v, want none", list.RepoErrors)
	}
	if len(list.PullRequests) != 2 || list.PullRequests[0].PrKey != keyHello7 {
		t.Fatalf("pull_requests = %+v, want request order", list.PullRequests)
	}
	v := viewFor(t, list, keyHello7)
	assertVerdict(t, v, "ready")
	if v.Freshness != "fresh" || v.AgeSeconds != 0 || v.Invalidated != nil || v.LastError != nil {
		t.Errorf("view = %+v, want fresh age 0", v)
	}
	s := v.Snapshot
	if s == nil {
		t.Fatal("snapshot missing")
	}
	if s.PrKey != keyHello7 || s.HeadSha != "head-7a" || s.HeadRef != "feat-7" || s.BaseRef != "main" ||
		s.BaseSha != "base-main" || !s.ObservedAt.Equal(readinessT0) || s.Verdict != "ready" || s.Fingerprint == "" {
		t.Errorf("snapshot = %+v", s)
	}
	if s.Facts.RequiredChecks.Status != "known" || *s.Facts.RequiredChecks.Value != "passing" || s.Facts.RequiredCheckCounts.Passed != 1 {
		t.Errorf("required checks = %+v %+v", s.Facts.RequiredChecks, s.Facts.RequiredCheckCounts)
	}

	missing := viewFor(t, list, "github:octocat/hello#404")
	assertVerdict(t, missing, "unknown", "repo_error:not_found")
	if missing.Snapshot != nil || missing.LastError == nil || missing.LastError.Code != "not_found" {
		t.Errorf("missing PR view = %+v", missing)
	}

	if n := f.queryCount("octocat/hello"); n != 1 {
		t.Fatalf("GraphQL queries = %d, want 1 (one query per repository)", n)
	}
	if got := f.lastQuery("octocat/hello"); !slices.Equal(got, []int{7, 404}) {
		t.Fatalf("query numbers = %v", got)
	}
	assertGrantActions(t, h, prReadActions)
	assertReadOnlyGitHub(t, h)
}

func TestReadinessDropsDuplicateKeysAndAcceptsLegacyForm(t *testing.T) {
	h, f, _ := newReadinessHarness(t)
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))

	list := getReadiness(t, h, false, keyHello7, "octocat/hello#7", "GITHUB:Octocat/Hello#7")
	if len(list.PullRequests) != 1 || list.PullRequests[0].PrKey != keyHello7 {
		t.Fatalf("pull_requests = %+v, want one canonical row", list.PullRequests)
	}
	if got := f.lastQuery("octocat/hello"); !slices.Equal(got, []int{7}) {
		t.Fatalf("query numbers = %v, want [7]", got)
	}
}

func TestReadinessPartialSuccessForbiddenRepo(t *testing.T) {
	h, f, _ := newReadinessHarness(t)
	h.addRepo(t, "world", "https://github.com/octocat/world")
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	f.setFailure("octocat/world", &fakeRepoFailure{status: http.StatusForbidden, body: map[string]any{"message": "Resource not accessible by integration"}})

	list := getReadiness(t, h, false, keyHello7, keyWorld3)

	assertVerdict(t, viewFor(t, list, keyHello7), "ready")
	world := viewFor(t, list, keyWorld3)
	assertVerdict(t, world, "unknown", "repo_error:forbidden")
	if len(list.RepoErrors) != 1 {
		t.Fatalf("repo_errors = %+v, want one", list.RepoErrors)
	}
	re := list.RepoErrors[0]
	if re.Repo != "octocat/world" || re.Code != "forbidden" || re.Retryable || !slices.Equal(re.PrKeys, []string{keyWorld3}) ||
		re.SourceRepo == nil || *re.SourceRepo != "world" {
		t.Fatalf("repo error = %+v", re)
	}
	assertReadOnlyGitHub(t, h)
}

func TestReadinessRateLimitedRepoBacksOffEvenWithForce(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	h.addRepo(t, "world", "https://github.com/octocat/world")
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	f.setNode("octocat/world", readyNode(3, "head-3a", "feat-3", "main"))

	// First read: both succeed, world is ready.
	list := getReadiness(t, h, false, keyHello7, keyWorld3)
	assertVerdict(t, viewFor(t, list, keyWorld3), "ready")

	// World gets rate limited (403 + exhausted primary limit + Retry-After).
	f.setFailure("octocat/world", &fakeRepoFailure{
		status: http.StatusForbidden,
		header: map[string]string{"Retry-After": "120", "X-RateLimit-Remaining": "0"},
		body:   map[string]any{"message": "API rate limit exceeded"},
	})
	clock.advance(time.Second)
	list = getReadiness(t, h, true, keyHello7, keyWorld3)
	assertVerdict(t, viewFor(t, list, keyHello7), "ready")
	world := viewFor(t, list, keyWorld3)
	assertVerdict(t, world, "unknown", "repo_error:rate_limited")
	if world.Snapshot == nil || world.Snapshot.Verdict != "ready" || world.Snapshot.HeadSha != "head-3a" {
		t.Fatalf("world snapshot = %+v, want last-known ready kept as history", world.Snapshot)
	}
	re := repoErrorFor(list.RepoErrors, "octocat/world")
	if re == nil || re.Code != "rate_limited" || !re.Retryable || re.RetryAfterS == nil || *re.RetryAfterS != 120 ||
		!slices.Equal(re.PrKeys, []string{keyWorld3}) {
		t.Fatalf("repo error = %+v", re)
	}
	if n := f.queryCount("octocat/world"); n != 2 {
		t.Fatalf("world queries = %d, want 2", n)
	}

	// Within retry_after, even force must not re-read world; hello still is.
	clock.advance(30 * time.Second)
	helloBefore := f.queryCount("octocat/hello")
	list = getReadiness(t, h, true, keyHello7, keyWorld3)
	if n := f.queryCount("octocat/world"); n != 2 {
		t.Fatalf("world re-read during backoff (queries = %d)", n)
	}
	if n := f.queryCount("octocat/hello"); n != helloBefore+1 {
		t.Fatalf("hello queries = %d, want %d (force re-reads)", n, helloBefore+1)
	}
	re = repoErrorFor(list.RepoErrors, "octocat/world")
	if re == nil || re.Code != "rate_limited" || re.RetryAfterS == nil || *re.RetryAfterS != 90 {
		t.Fatalf("backoff repo error = %+v, want rate_limited retry_after_s 90", re)
	}
	assertVerdict(t, viewFor(t, list, keyWorld3), "unknown", "repo_error:rate_limited")

	// After retry_after, world is read again and recovers.
	f.setFailure("octocat/world", nil)
	clock.advance(91 * time.Second)
	list = getReadiness(t, h, false, keyHello7, keyWorld3)
	if n := f.queryCount("octocat/world"); n != 3 {
		t.Fatalf("world queries = %d, want 3 after backoff", n)
	}
	if len(list.RepoErrors) != 0 {
		t.Fatalf("repo_errors = %+v, want none after recovery", list.RepoErrors)
	}
	assertVerdict(t, viewFor(t, list, keyWorld3), "ready")
	assertReadOnlyGitHub(t, h)
}

func TestReadinessGraphQLRateLimitWithoutRetryAfterUsesDefaultWait(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	f.setFailure("octocat/hello", &fakeRepoFailure{
		status: http.StatusOK,
		body:   map[string]any{"errors": []any{map[string]any{"type": "RATE_LIMITED", "message": "rate limited"}}},
	})
	list := getReadiness(t, h, false, keyHello7)
	re := repoErrorFor(list.RepoErrors, "octocat/hello")
	if re == nil || re.Code != "rate_limited" || re.RetryAfterS == nil || *re.RetryAfterS != int(readinessDefaultRateLimitWait/time.Second) {
		t.Fatalf("repo error = %+v, want default %v wait", re, readinessDefaultRateLimitWait)
	}
	v := viewFor(t, list, keyHello7)
	if v.LastError == nil || v.LastError.RetryAfterS == nil || *v.LastError.RetryAfterS != 60 {
		t.Fatalf("last_error = %+v, want retry_after_s 60", v.LastError)
	}
	clock.advance(59 * time.Second)
	getReadiness(t, h, true, keyHello7)
	if n := f.queryCount("octocat/hello"); n != 1 {
		t.Fatalf("queries = %d, want 1 during default backoff", n)
	}
}

func TestReadinessTimeoutRepoDoesNotFailOtherRepo(t *testing.T) {
	h, f, _ := newReadinessHarness(t)
	h.addRepo(t, "world", "https://github.com/octocat/world")
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	f.setFailure("octocat/world", &fakeRepoFailure{slow: true})

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	start := time.Now()
	status, raw := h.streamWithContext(t, ctx, readinessPath(false, keyHello7, keyWorld3))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("readiness request took %v; deadline not honored", elapsed)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d (body %s)", status, raw)
	}
	list := decodeStrict[gen.PullRequestReadinessList](t, raw)
	assertVerdict(t, viewFor(t, list, keyHello7), "ready")
	assertVerdict(t, viewFor(t, list, keyWorld3), "unknown", "repo_error:timeout")
	re := repoErrorFor(list.RepoErrors, "octocat/world")
	if re == nil || re.Code != "timeout" || !re.Retryable {
		t.Fatalf("repo error = %+v, want retryable timeout", re)
	}
}

func TestReadinessUnregisteredRepoDoesNotCallGitHub(t *testing.T) {
	h, _, _ := newReadinessHarness(t)

	list := getReadiness(t, h, true, "github:someone/else#1", "github:someone/else#2")

	if calls := h.github.snapshot(); len(calls) != 0 {
		t.Fatalf("GitHub calls = %+v, want none", calls)
	}
	if len(list.RepoErrors) != 1 {
		t.Fatalf("repo_errors = %+v", list.RepoErrors)
	}
	re := list.RepoErrors[0]
	if re.Code != "repo_unregistered" || re.Repo != "someone/else" || re.Retryable ||
		!slices.Equal(re.PrKeys, []string{"github:someone/else#1", "github:someone/else#2"}) {
		t.Fatalf("repo error = %+v", re)
	}
	assertVerdict(t, viewFor(t, list, "github:someone/else#1"), "unknown", "repo_error:repo_unregistered")
}

func TestReadinessConnectorUnavailable(t *testing.T) {
	h := newPRReviewHarnessWithCredential(t, true, nil, testCredentialNone, "")
	list := getReadiness(t, h, false, keyHello7)
	if calls := h.github.snapshot(); len(calls) != 0 {
		t.Fatalf("GitHub calls = %+v, want none", calls)
	}
	re := repoErrorFor(list.RepoErrors, "octocat/hello")
	if re == nil || re.Code != "connector_unavailable" {
		t.Fatalf("repo errors = %+v, want connector_unavailable", list.RepoErrors)
	}
	assertVerdict(t, viewFor(t, list, keyHello7), "unknown", "repo_error:connector_unavailable")
}

func TestReadinessMinRefetchCoalescesAndForceBypasses(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))

	getReadiness(t, h, false, keyHello7)
	clock.advance(readinessMinRefetch - time.Second)
	list := getReadiness(t, h, false, keyHello7)
	if n := f.queryCount("octocat/hello"); n != 1 {
		t.Fatalf("queries = %d, want 1 (coalesced within %v)", n, readinessMinRefetch)
	}
	v := viewFor(t, list, keyHello7)
	if v.AgeSeconds != 14 || !v.Snapshot.ObservedAt.Equal(readinessT0) {
		t.Fatalf("coalesced view = age %d observed %v, want cached snapshot", v.AgeSeconds, v.Snapshot.ObservedAt)
	}

	getReadiness(t, h, true, keyHello7)
	if n := f.queryCount("octocat/hello"); n != 2 {
		t.Fatalf("queries = %d, want 2 after force", n)
	}

	// Exactly readinessMinRefetch after the last observation re-reads.
	clock.advance(readinessMinRefetch)
	getReadiness(t, h, false, keyHello7)
	if n := f.queryCount("octocat/hello"); n != 3 {
		t.Fatalf("queries = %d, want 3 at min-refetch boundary", n)
	}
	assertReadOnlyGitHub(t, h)
}

func TestReadinessAgingAndStaleKeepHistory(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	getReadiness(t, h, false, keyHello7)

	// Without any further read (cache view at the fake clock).
	clock.advance(61 * time.Second)
	v := h.module.readiness.view(prReviewTestWorkspace, keyHello7, clock.now())
	if v.CurrentVerdict != prreadiness.VerdictUnknown || !slices.Equal(v.CurrentReasons, []string{prreadiness.ReasonAging}) ||
		v.Freshness != prreadiness.FreshnessAging || v.Snapshot == nil || v.Snapshot.Verdict != prreadiness.VerdictReady {
		t.Fatalf("61s view = %+v, want unknown readiness_aging with ready history", v)
	}

	// Through the endpoint: GitHub now rate-limits, so the re-read fails and
	// the snapshot is shown as history only.
	f.setFailure("octocat/hello", &fakeRepoFailure{
		status: http.StatusOK,
		header: map[string]string{"Retry-After": "600"},
		body:   map[string]any{"errors": []any{map[string]any{"type": "RATE_LIMITED", "message": "rate limited"}}},
	})
	list := getReadiness(t, h, false, keyHello7)
	ev := viewFor(t, list, keyHello7)
	assertVerdict(t, ev, "unknown", prreadiness.ReasonAging, "repo_error:rate_limited")
	if ev.Freshness != "aging" || ev.AgeSeconds != 61 || ev.Snapshot == nil || ev.Snapshot.Verdict != "ready" {
		t.Fatalf("aging view = %+v", ev)
	}

	clock.advance(9*time.Minute + 30*time.Second) // 10m31s after observation; backoff runs to 11m01s
	list = getReadiness(t, h, false, keyHello7)
	ev = viewFor(t, list, keyHello7)
	assertVerdict(t, ev, "unknown", "stale", "repo_error:rate_limited")
	if ev.Freshness != "stale" || ev.Snapshot == nil || ev.Snapshot.Verdict != "ready" || !ev.Snapshot.ObservedAt.Equal(readinessT0) {
		t.Fatalf("stale view = %+v, want ready snapshot retained as history", ev)
	}
	if n := f.queryCount("octocat/hello"); n != 2 {
		t.Fatalf("queries = %d, want 2 (second read rate limited, third skipped by backoff)", n)
	}
}

func TestReadinessHeadMovementFromListInvalidates(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	getReadiness(t, h, false, keyHello7)

	// The PR list now reports a new head SHA.
	clock.advance(2 * time.Second)
	h.github.setListPayload("octocat", "hello", []map[string]any{{
		"number": 7, "state": "open", "title": "PR 7",
		"html_url": "https://github.com/octocat/hello/pull/7",
		"head":     map[string]any{"sha": "head-7b", "ref": "feat-7"},
		"base":     map[string]any{"sha": "base-main", "ref": "main"},
	}})
	if status, raw := h.get(t, "/api/workspaces/WS/pull-requests?state=open"); status != http.StatusOK {
		t.Fatalf("list status = %d (body %s)", status, raw)
	}
	v := h.module.readiness.view(prReviewTestWorkspace, keyHello7, clock.now())
	if v.Invalidated != prreadiness.InvalidatedHeadMoved || v.Freshness != prreadiness.FreshnessStale ||
		v.CurrentVerdict == prreadiness.VerdictReady || v.Snapshot == nil || v.Snapshot.HeadSHA != "head-7a" {
		t.Fatalf("view after head move = %+v, want invalidated head_moved, stale, not ready", v)
	}
	if !h.module.readiness.needsRead(prReviewTestWorkspace, keyHello7, clock.now()) {
		t.Fatal("invalidated snapshot inside min-refetch window must still need a read")
	}

	// Re-read on the new head: checks pending → waiting, invalidation cleared.
	moved := readyNode(7, "head-7b", "feat-7", "main")
	moved["mergeStateStatus"] = "BLOCKED"
	moved["commits"] = checksCommits("head-7b", map[string]any{
		"__typename": "CheckRun", "name": "ci", "status": "IN_PROGRESS", "conclusion": nil, "isRequired": true,
	})
	f.setNode("octocat/hello", moved)
	clock.advance(time.Second)
	list := getReadiness(t, h, false, keyHello7)
	ev := viewFor(t, list, keyHello7)
	assertVerdict(t, ev, "waiting", prreadiness.ReasonRequiredChecksPending)
	if ev.Invalidated != nil || ev.Freshness != "fresh" || ev.Snapshot == nil || ev.Snapshot.HeadSha != "head-7b" {
		t.Fatalf("view after re-read = %+v", ev)
	}
	if ev.Snapshot.Facts.RequiredCheckCounts.PendingNames == nil || (*ev.Snapshot.Facts.RequiredCheckCounts.PendingNames)[0] != "ci" {
		t.Fatalf("pending names = %+v", ev.Snapshot.Facts.RequiredCheckCounts)
	}
	if n := f.queryCount("octocat/hello"); n != 2 {
		t.Fatalf("queries = %d, want 2", n)
	}
	assertReadOnlyGitHub(t, h)
}

func TestReadinessDetailReadInvalidatesOnHeadAndBaseChange(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	// The fake detail endpoint reports head "headsha-123" on base "main".
	f.setNode("octocat/hello", readyNode(7, "headsha-123", "feature/review-api", "main"))
	getReadiness(t, h, false, keyHello7)

	clock.advance(time.Second)
	if status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7"); status != http.StatusOK {
		t.Fatalf("detail status = %d (body %s)", status, raw)
	}
	if v := h.module.readiness.view(prReviewTestWorkspace, keyHello7, clock.now()); v.Invalidated != "" ||
		v.CurrentVerdict != prreadiness.VerdictReady {
		t.Fatalf("unchanged detail invalidated the snapshot: %+v", v)
	}

	h.github.setHead("headsha-456")
	if status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7"); status != http.StatusOK {
		t.Fatalf("detail status = %d (body %s)", status, raw)
	}
	if v := h.module.readiness.view(prReviewTestWorkspace, keyHello7, clock.now()); v.Invalidated != prreadiness.InvalidatedHeadMoved ||
		v.CurrentVerdict != prreadiness.VerdictUnknown {
		t.Fatalf("view after detail head move = %+v", v)
	}

	// Base change: snapshot on base "release", detail says "main".
	h.github.setHead("headsha-123")
	f.setNode("octocat/hello", readyNode(7, "headsha-123", "feature/review-api", "release"))
	clock.advance(time.Second)
	getReadiness(t, h, true, keyHello7)
	clock.advance(time.Second)
	if status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7"); status != http.StatusOK {
		t.Fatalf("detail status = %d (body %s)", status, raw)
	}
	if v := h.module.readiness.view(prReviewTestWorkspace, keyHello7, clock.now()); v.Invalidated != prreadiness.InvalidatedBaseChanged {
		t.Fatalf("view after base change = %+v, want base_changed", v)
	}
}

func TestReadinessComputingMergeableIsReRead(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	computing := readyNode(7, "head-7a", "feat-7", "main")
	computing["mergeable"] = "UNKNOWN"
	computing["mergeStateStatus"] = "UNKNOWN"
	f.setNode("octocat/hello", computing)
	f.setNode("octocat/hello", readyNode(8, "head-8a", "feat-8", "release"))
	f.onQuery = func(_ string, _ []int) {
		clock.advance(time.Second) // network time between reads
		if f.queryCount("octocat/hello") == 2 {
			f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
		}
	}

	list := getReadiness(t, h, false, keyHello7, keyHello8)

	if n := f.queryCount("octocat/hello"); n != 2 {
		t.Fatalf("queries = %d, want 2 (initial + one computing re-read)", n)
	}
	if got := f.lastQuery("octocat/hello"); !slices.Equal(got, []int{7}) {
		t.Fatalf("re-read numbers = %v, want only the computing PR", got)
	}
	assertVerdict(t, viewFor(t, list, keyHello7), "ready")
	assertVerdict(t, viewFor(t, list, keyHello8), "ready")
}

func TestReadinessComputingWithoutBackoffStaysWaiting(t *testing.T) {
	h, f, _ := newReadinessHarness(t)
	h.module.readinessBackoff = []time.Duration{} // no re-read attempts
	computing := readyNode(7, "head-7a", "feat-7", "main")
	computing["mergeable"] = nil
	f.setNode("octocat/hello", computing)

	list := getReadiness(t, h, false, keyHello7)
	if n := f.queryCount("octocat/hello"); n != 1 {
		t.Fatalf("queries = %d, want 1", n)
	}
	v := viewFor(t, list, keyHello7)
	assertVerdict(t, v, "waiting", prreadiness.ReasonGitHubComputing)
	if v.Snapshot == nil || v.Snapshot.Facts.Conflicts.Status != "computing" {
		t.Fatalf("snapshot facts = %+v", v.Snapshot)
	}
}

func TestReadinessPreview(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	h.addRepo(t, "world", "https://github.com/octocat/world")
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	f.setNode("octocat/hello", readyNode(8, "head-8a", "feat-8", "feat-7")) // stacked on #7
	f.setNode("octocat/world", readyNode(3, "head-3a", "feat-3", "main"))

	status, raw := h.get(t, previewPath(keyWorld3, keyHello7, keyHello8))
	if status != http.StatusOK {
		t.Fatalf("status = %d (body %s)", status, raw)
	}
	resp := decodeStrict[gen.PullRequestReadinessPreviewResponse](t, raw)
	p := resp.Preview
	if resp.FreshForS != 60 || resp.StaleAfterS != 600 || !resp.ServerNow.Equal(clock.now()) {
		t.Fatalf("envelope = %+v", resp)
	}
	gotKeys := make([]string, 0, len(p.Members))
	gotPos := make([]string, 0, len(p.Members))
	for _, m := range p.Members {
		gotKeys = append(gotKeys, m.Readiness.PrKey)
		gotPos = append(gotPos, string(m.Position))
	}
	if !slices.Equal(gotKeys, []string{keyWorld3, keyHello7, keyHello8}) {
		t.Fatalf("member order = %v", gotKeys)
	}
	if !slices.Equal(gotPos, []string{"in_prefix", "in_prefix", "stop"}) {
		t.Fatalf("positions = %v", gotPos)
	}
	if p.ReadyCount != 2 || p.StoppedBy == nil || p.StoppedBy.PrKey != keyHello8 || p.StoppedBy.Verdict != "waiting" ||
		!slices.Equal(p.StoppedBy.Reasons, []string{prreadiness.ReasonPredecessorRetarget}) {
		t.Fatalf("preview = ready %d stopped %+v", p.ReadyCount, p.StoppedBy)
	}
	if p.ExpiresAt == nil || !p.ExpiresAt.Equal(readinessT0.Add(prreadiness.FreshFor)) || p.Fingerprint == "" {
		t.Fatalf("expires_at = %v fingerprint %q", p.ExpiresAt, p.Fingerprint)
	}

	// Preview always re-reads, even inside the min-refetch window.
	clock.advance(time.Second)
	if status, raw := h.get(t, previewPath(keyWorld3, keyHello7, keyHello8)); status != http.StatusOK {
		t.Fatalf("second preview status = %d (body %s)", status, raw)
	}
	if f.queryCount("octocat/hello") != 2 || f.queryCount("octocat/world") != 2 {
		t.Fatalf("queries hello=%d world=%d, want 2 each (preview forces)", f.queryCount("octocat/hello"), f.queryCount("octocat/world"))
	}
	assertReadOnlyGitHub(t, h)
}

func TestReadinessPreviewRepoErrorStopsPrefix(t *testing.T) {
	h, f, _ := newReadinessHarness(t)
	h.addRepo(t, "world", "https://github.com/octocat/world")
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	f.setFailure("octocat/world", &fakeRepoFailure{status: http.StatusForbidden, body: map[string]any{"message": "nope"}})

	status, raw := h.get(t, previewPath(keyWorld3, keyHello7))
	if status != http.StatusOK {
		t.Fatalf("status = %d (body %s)", status, raw)
	}
	resp := decodeStrict[gen.PullRequestReadinessPreviewResponse](t, raw)
	if resp.Preview.ReadyCount != 0 || resp.Preview.ExpiresAt != nil || resp.Preview.StoppedBy == nil ||
		resp.Preview.StoppedBy.PrKey != keyWorld3 {
		t.Fatalf("preview = %+v, want stop at failing first member", resp.Preview)
	}
	if resp.Preview.Members[1].Position != "after_stop" {
		t.Fatalf("second member position = %q", resp.Preview.Members[1].Position)
	}
	if repoErrorFor(resp.RepoErrors, "octocat/world") == nil {
		t.Fatalf("repo_errors = %+v", resp.RepoErrors)
	}
}

func TestReadinessRejectsBadKeys(t *testing.T) {
	h, _, _ := newReadinessHarness(t)
	many := func(n int) []string {
		keys := make([]string, 0, n)
		for i := 1; i <= n; i++ {
			keys = append(keys, "github:octocat/hello#"+strconv.Itoa(i))
		}
		return keys
	}
	tests := []struct {
		name string
		path string
	}{
		{"readiness without pr", "/api/workspaces/WS/pull-requests/readiness"},
		{"readiness malformed key", readinessPath(false, "octocat/hello")},
		{"readiness zero number", readinessPath(false, "github:octocat/hello#0")},
		{"readiness too many keys", readinessPath(false, many(maxReadinessKeys+1)...)},
		{"preview without pr", "/api/workspaces/WS/pull-requests/readiness/preview"},
		{"preview duplicate key", previewPath(keyHello7, keyHello8, "octocat/hello#7")},
		{"preview too many keys", previewPath(many(maxPreviewKeys + 1)...)},
		{"preview malformed key", previewPath("github:octocat#7")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, raw := h.get(t, tt.path)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", status, raw)
			}
			if code := decodeErrorCode(t, raw); code != "invalid" {
				t.Fatalf("code = %q, want invalid", code)
			}
		})
	}
	if calls := h.github.snapshot(); len(calls) != 0 {
		t.Fatalf("GitHub calls = %+v, want none for rejected requests", calls)
	}

	// Limits are inclusive.
	if status, raw := h.get(t, readinessPath(false, many(maxReadinessKeys)...)); status != http.StatusOK {
		t.Fatalf("readiness with %d keys status = %d (body %s)", maxReadinessKeys, status, raw)
	}
}

func TestReadinessChunksLargeRepoReads(t *testing.T) {
	h, f, _ := newReadinessHarness(t)
	keys := make([]string, 0, 25)
	for i := 1; i <= 25; i++ {
		keys = append(keys, "github:octocat/hello#"+strconv.Itoa(i))
		f.setNode("octocat/hello", readyNode(i, "head-"+strconv.Itoa(i), "feat-"+strconv.Itoa(i), "main"))
	}
	list := getReadiness(t, h, false, keys...)
	if n := f.queryCount("octocat/hello"); n != 2 {
		t.Fatalf("queries = %d, want 2 chunks of <= %d", n, providers.MaxReadinessNumbers)
	}
	if got := f.lastQuery("octocat/hello"); len(got) != 5 {
		t.Fatalf("second chunk = %v, want 5 numbers", got)
	}
	for _, v := range list.PullRequests {
		assertVerdict(t, v, "ready")
	}
}

func TestReadinessCredentialInvalidationClearsCache(t *testing.T) {
	h, f, clock := newReadinessHarness(t)
	f.setNode("octocat/hello", readyNode(7, "head-7a", "feat-7", "main"))
	getReadiness(t, h, false, keyHello7)
	h.module.InvalidateCredentialSeeds()
	v := h.module.readiness.view(prReviewTestWorkspace, keyHello7, clock.now())
	if v.Snapshot != nil || !slices.Equal(v.CurrentReasons, []string{prreadiness.ReasonNotObserved}) {
		t.Fatalf("view after credential invalidation = %+v, want not_observed", v)
	}
}

// ---- readinessCache unit tests ----------------------------------------------

func cacheSnap(t *testing.T, head, base string, lifecycle string, at time.Time) prreadiness.Snapshot {
	t.Helper()
	facts := prreadiness.Facts{
		Lifecycle:      prreadiness.Known(lifecycle),
		Conflicts:      prreadiness.Known(prreadiness.ConflictsNone),
		MergeState:     prreadiness.Known(prreadiness.MergeStateClean),
		Review:         prreadiness.Known(prreadiness.ReviewApproved),
		RequiredChecks: prreadiness.Known(prreadiness.ChecksPassing),
		OptionalChecks: prreadiness.Known(prreadiness.ChecksNone),
		Queue:          prreadiness.Known(prreadiness.QueueNotQueued),
	}
	return prreadiness.NewSnapshot(keyHello7, head, "feat", base, "b", at, facts)
}

func TestReadinessCacheApplyOrdering(t *testing.T) {
	var c readinessCache
	t0 := readinessT0
	if !c.apply("WS", cacheSnap(t, "h1", "main", prreadiness.LifecycleOpen, t0), t0) {
		t.Fatal("first apply rejected")
	}
	if c.apply("WS", cacheSnap(t, "h0", "main", prreadiness.LifecycleOpen, t0.Add(-time.Second)), t0) {
		t.Fatal("older snapshot applied")
	}
	if c.apply("WS", cacheSnap(t, "h0", "main", prreadiness.LifecycleOpen, t0), t0) {
		t.Fatal("equal-time snapshot applied")
	}
	if !c.apply("WS", cacheSnap(t, "h2", "main", prreadiness.LifecycleMerged, t0.Add(time.Second)), t0) {
		t.Fatal("merged snapshot rejected")
	}
	if c.apply("WS", cacheSnap(t, "h3", "main", prreadiness.LifecycleOpen, t0.Add(2*time.Second)), t0) {
		t.Fatal("merged was un-merged by a later open snapshot")
	}
	// Other workspace is independent.
	if v := c.view("OTHER", keyHello7, t0); v.Snapshot != nil {
		t.Fatalf("workspace leak: %+v", v)
	}
}

func TestReadinessCacheInvalidation(t *testing.T) {
	var c readinessCache
	t0 := readinessT0
	c.apply("WS", cacheSnap(t, "h1", "main", prreadiness.LifecycleOpen, t0), t0)

	// Empty values never invalidate; unknown keys are ignored.
	c.observeRefs("WS", keyHello7, "", "", t0.Add(time.Second))
	c.observeRefs("WS", "github:octocat/hello#99", "zzz", "dev", t0)
	c.observeRefs("WS", "", "zzz", "dev", t0)
	if c.needsRead("WS", keyHello7, t0.Add(time.Second)) {
		t.Fatal("snapshot needs read after no-op observations")
	}

	c.observeRefs("WS", keyHello7, "h2", "", t0.Add(2*time.Second))
	v := c.view("WS", keyHello7, t0.Add(2*time.Second))
	if v.Invalidated != prreadiness.InvalidatedHeadMoved {
		t.Fatalf("invalidated = %q", v.Invalidated)
	}
	// A read that started before the invalidation must not clear it.
	if c.apply("WS", cacheSnap(t, "h1", "main", prreadiness.LifecycleOpen, t0.Add(3*time.Second)), t0.Add(time.Second)) {
		t.Fatal("read started before invalidation was applied")
	}
	// A read that started after it does.
	if !c.apply("WS", cacheSnap(t, "h2", "main", prreadiness.LifecycleOpen, t0.Add(4*time.Second)), t0.Add(3*time.Second)) {
		t.Fatal("fresh read after invalidation rejected")
	}
	if v := c.view("WS", keyHello7, t0.Add(4*time.Second)); v.Invalidated != "" || v.CurrentVerdict != prreadiness.VerdictReady {
		t.Fatalf("view after fresh read = %+v", v)
	}

	// Base change.
	c.observeRefs("WS", keyHello7, "h2", "release", t0.Add(5*time.Second))
	if v := c.view("WS", keyHello7, t0.Add(5*time.Second)); v.Invalidated != prreadiness.InvalidatedBaseChanged {
		t.Fatalf("invalidated = %q, want base_changed", v.Invalidated)
	}

	// Merged snapshots are never invalidated.
	var m readinessCache
	m.apply("WS", cacheSnap(t, "h1", "main", prreadiness.LifecycleMerged, t0), t0)
	m.observeRefs("WS", keyHello7, "h9", "dev", t0.Add(time.Second))
	if v := m.view("WS", keyHello7, t0.Add(time.Second)); v.Invalidated != "" || v.CurrentVerdict != prreadiness.VerdictMerged {
		t.Fatalf("merged view = %+v", v)
	}
}

func TestReadinessCacheNeedsReadAndErrors(t *testing.T) {
	var c readinessCache
	t0 := readinessT0
	if !c.needsRead("WS", keyHello7, t0) {
		t.Fatal("missing entry must need a read")
	}
	c.recordError("WS", keyHello7, prreadiness.ErrTimeout, 0, t0)
	if !c.needsRead("WS", keyHello7, t0) {
		t.Fatal("error-only entry must need a read")
	}
	c.apply("WS", cacheSnap(t, "h1", "main", prreadiness.LifecycleOpen, t0.Add(time.Second)), t0)
	if v := c.view("WS", keyHello7, t0.Add(time.Second)); v.LastError != nil {
		t.Fatalf("older error survived a newer snapshot: %+v", v.LastError)
	}
	if c.needsRead("WS", keyHello7, t0.Add(2*time.Second)) {
		t.Fatal("young snapshot must not need a read")
	}
	c.recordError("WS", keyHello7, prreadiness.ErrRateLimited, 1500*time.Millisecond, t0.Add(3*time.Second))
	if !c.needsRead("WS", keyHello7, t0.Add(3*time.Second)) {
		t.Fatal("snapshot with a newer failed read must need a read")
	}
	v := c.view("WS", keyHello7, t0.Add(3*time.Second))
	if v.LastError == nil || v.LastError.RetryAfterSeconds != 2 || v.CurrentVerdict != prreadiness.VerdictUnknown {
		t.Fatalf("view = %+v, want unknown with rate_limited last error retry 2s", v)
	}
}
