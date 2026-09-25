package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func readinessSpec(numbers ...any) CallSpec {
	return CallSpec{
		Action:     ActionGitHubPullRequestReadinessRead,
		Resource:   "repo:octocat/hello",
		Args:       map[string]any{"owner": "octocat", "repo": "hello", "numbers": numbers},
		Credential: testToken,
	}
}

func readinessFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "readiness_graphql_partial.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

const readinessOnePR = `{"data":{"repository":{"pr_7":{
  "number":7,"id":"PR_7","state":"OPEN","isDraft":false,"merged":false,
  "headRefName":"feat","headRefOid":"head7","baseRefName":"main","baseRefOid":"base7",
  "mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","reviewDecision":null,
  "isInMergeQueue":true,"mergeQueueEntry":{"state":"AWAITING_CHECKS"},
  "commits":{"nodes":[{"commit":{"oid":"head7","statusCheckRollup":{"state":"PENDING","contexts":{
    "totalCount":2,"pageInfo":{"hasNextPage":false},"nodes":[
      {"__typename":"CheckRun","name":"build","status":"IN_PROGRESS","conclusion":null,"isRequired":true},
      {"__typename":"StatusContext","context":"ci/legacy","state":"SUCCESS","isRequired":false}
  ]}}}}]}}},
  "rateLimit":{"cost":1,"remaining":4999,"resetAt":"2026-09-24T13:00:00Z"}}}`

func TestGitHubReadinessRequestShape(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK, body: readinessOnePR})

	result, err := fake.provider().Call(context.Background(), readinessSpec(float64(7)))
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if result.Status != http.StatusOK || result.Decision != domain.ConnectorCallGranted {
		t.Fatalf("result = %+v, want 200 granted", result)
	}
	reqs := fake.recorded()
	if len(reqs) != 1 {
		t.Fatalf("fake saw %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodPost || req.Path != "/graphql" {
		t.Fatalf("request = %s %s, want POST /graphql", req.Method, req.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization = %q", got)
	}
	query, _ := req.Body["query"].(string)
	for _, want := range []string{"pr_7: pullRequest(number: 7)", "isRequired(pullRequestNumber: 7)", "$owner: String!", "rateLimit"} {
		if !strings.Contains(query, want) {
			t.Errorf("query missing %q:\n%s", want, query)
		}
	}
	if strings.Contains(strings.ToLower(query), "mutation") {
		t.Errorf("readiness query contains a mutation:\n%s", query)
	}
	vars, _ := req.Body["variables"].(map[string]any)
	if vars["owner"] != "octocat" || vars["repo"] != "hello" {
		t.Errorf("variables = %v, want owner/repo", vars)
	}

	pulls, _ := result.Body["pullRequests"].([]map[string]any)
	if len(pulls) != 1 {
		t.Fatalf("pullRequests = %#v, want one", result.Body["pullRequests"])
	}
	pr := pulls[0]
	checks := map[string]any{
		"number": 7, "nodeId": "PR_7", "state": "OPEN", "headRefOid": "head7", "baseRefName": "main",
		"mergeable": "MERGEABLE", "reviewDecision": "", "isInMergeQueue": true,
		"mergeQueueState": "AWAITING_CHECKS", "checksTruncated": false,
	}
	for k, want := range checks {
		if pr[k] != want {
			t.Errorf("pr[%q] = %#v, want %#v", k, pr[k], want)
		}
	}
	ctxs, _ := pr["checks"].([]map[string]any)
	if len(ctxs) != 2 {
		t.Fatalf("checks = %#v, want 2", pr["checks"])
	}
	if ctxs[0]["kind"] != "check_run" || ctxs[0]["name"] != "build" || ctxs[0]["status"] != "IN_PROGRESS" || ctxs[0]["isRequired"] != true {
		t.Errorf("check_run = %#v", ctxs[0])
	}
	if ctxs[1]["kind"] != "status_context" || ctxs[1]["name"] != "ci/legacy" || ctxs[1]["conclusion"] != "SUCCESS" || ctxs[1]["isRequired"] != false {
		t.Errorf("status_context = %#v", ctxs[1])
	}
	missing, _ := result.Body["missing"].([]map[string]any)
	if missing == nil || len(missing) != 0 {
		t.Errorf("missing = %#v, want empty non-nil", result.Body["missing"])
	}
	rl, _ := result.Body["rateLimit"].(map[string]any)
	if rl["remaining"] != 4999 {
		t.Errorf("rateLimit = %#v", result.Body["rateLimit"])
	}
}

func TestGitHubReadinessQueryAliasesEveryNumberOnce(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK, body: `{"data":{"repository":{}}}`})

	result, err := fake.provider().Call(context.Background(), readinessSpec(3, float64(5), 3))
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	query, _ := fake.recorded()[0].Body["query"].(string)
	if strings.Count(query, "pr_3:") != 1 || strings.Count(query, "pr_5:") != 1 {
		t.Fatalf("query aliases wrong (duplicates must collapse):\n%s", query)
	}
	// Aliases absent from data are reported missing (upstream_error), not dropped.
	missing, _ := result.Body["missing"].([]map[string]any)
	if len(missing) != 2 || missing[0]["code"] != "upstream_error" || missing[0]["number"] != 3 || missing[1]["number"] != 5 {
		t.Fatalf("missing = %#v, want 3 and 5 upstream_error", missing)
	}
}

func TestGitHubReadinessParsesRealPartialFixture(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK, body: readinessFixture(t)})

	result, err := fake.provider().Call(context.Background(), readinessSpec(785, 99999))
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if result.Decision != domain.ConnectorCallGranted {
		t.Fatalf("decision = %v, want granted (partial data kept)", result.Decision)
	}
	pulls, _ := result.Body["pullRequests"].([]map[string]any)
	if len(pulls) != 1 {
		t.Fatalf("pullRequests = %#v, want PR 785 only", result.Body["pullRequests"])
	}
	pr := pulls[0]
	if pr["number"] != 785 || pr["mergeStateStatus"] != "BLOCKED" || pr["reviewDecision"] != "REVIEW_REQUIRED" ||
		pr["headRefOid"] != "d4307ab52b67c001176511eba8e2ce0f9812af7b" || pr["baseRefName"] != "v5" ||
		pr["mergeQueueState"] != "" || pr["checksTruncated"] != false {
		t.Errorf("pr 785 = %#v", pr)
	}
	checks, _ := pr["checks"].([]map[string]any)
	if len(checks) != 9 {
		t.Errorf("checks = %d, want 9", len(checks))
	}
	missing, _ := result.Body["missing"].([]map[string]any)
	if len(missing) != 1 || missing[0]["number"] != 99999 || missing[0]["code"] != "not_found" {
		t.Fatalf("missing = %#v, want 99999 not_found", missing)
	}
	if msg, _ := missing[0]["message"].(string); !strings.Contains(msg, "99999") {
		t.Errorf("missing message = %q", msg)
	}
}

func TestGitHubReadinessTruncatedContexts(t *testing.T) {
	tests := []struct {
		name     string
		contexts string
	}{
		{"hasNextPage", `"totalCount":1,"pageInfo":{"hasNextPage":true},"nodes":[{"__typename":"CheckRun","name":"a","status":"COMPLETED","conclusion":"SUCCESS","isRequired":true}]`},
		{"totalCount exceeds nodes", `"totalCount":3,"pageInfo":{"hasNextPage":false},"nodes":[{"__typename":"CheckRun","name":"a","status":"COMPLETED","conclusion":"SUCCESS","isRequired":true}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			body := `{"data":{"repository":{"pr_7":{"number":7,"state":"OPEN","commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{` +
				tt.contexts + `}}}}]}}}}}`
			fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK, body: body})
			result, err := fake.provider().Call(context.Background(), readinessSpec(7))
			if err != nil {
				t.Fatalf("readiness: %v", err)
			}
			pr := result.Body["pullRequests"].([]map[string]any)[0]
			if pr["checksTruncated"] != true {
				t.Fatalf("checksTruncated = %v, want true", pr["checksTruncated"])
			}
		})
	}
}

func TestGitHubReadinessNoRollupHasNoChecks(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK,
		body: `{"data":{"repository":{"pr_7":{"number":7,"state":"OPEN","commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}}}}`})
	result, err := fake.provider().Call(context.Background(), readinessSpec(7))
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	pr := result.Body["pullRequests"].([]map[string]any)[0]
	checks, _ := pr["checks"].([]map[string]any)
	if checks == nil || len(checks) != 0 || pr["checksTruncated"] != false {
		t.Fatalf("checks = %#v truncated = %v, want empty and false", pr["checks"], pr["checksTruncated"])
	}
}

func TestGitHubReadinessRateLimited(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(90*time.Second).Unix(), 10)
	tests := []struct {
		name          string
		response      fakeResponse
		wantStatus    int
		wantRetryMin  time.Duration
		wantRetryMax  time.Duration
		wantRetryZero bool
	}{
		{
			name: "200 with RATE_LIMITED error and Retry-After",
			response: fakeResponse{
				status: http.StatusOK,
				header: map[string]string{"Retry-After": "42"},
				body:   `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded for ` + testToken + `"}]}`,
			},
			wantStatus:   http.StatusOK,
			wantRetryMin: 42 * time.Second,
			wantRetryMax: 42 * time.Second,
		},
		{
			name: "200 with RATE_LIMITED and no headers",
			response: fakeResponse{
				status: http.StatusOK,
				body:   `{"errors":[{"type":"RATE_LIMITED","message":"rate limited"}]}`,
			},
			wantStatus:    http.StatusOK,
			wantRetryZero: true,
		},
		{
			name: "200 with RATE_LIMITED and exhausted primary limit",
			response: fakeResponse{
				status: http.StatusOK,
				header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset},
				body:   `{"errors":[{"type":"RATE_LIMITED","message":"rate limited"}]}`,
			},
			wantStatus:   http.StatusOK,
			wantRetryMin: 60 * time.Second,
			wantRetryMax: 91 * time.Second,
		},
		{
			name: "403 with X-RateLimit-Remaining 0 and reset",
			response: fakeResponse{
				status: http.StatusForbidden,
				header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset},
				body:   `{"message":"API rate limit exceeded"}`,
			},
			wantStatus:   http.StatusForbidden,
			wantRetryMin: 60 * time.Second,
			wantRetryMax: 91 * time.Second,
		},
		{
			name: "429 prefers Retry-After over reset",
			response: fakeResponse{
				status: http.StatusTooManyRequests,
				header: map[string]string{"Retry-After": "5", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset},
				body:   `{"message":"secondary rate limit"}`,
			},
			wantStatus:   http.StatusTooManyRequests,
			wantRetryMin: 5 * time.Second,
			wantRetryMax: 5 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodPost, "/graphql", tt.response)
			result, err := fake.provider().Call(context.Background(), readinessSpec(7))
			var rl *RateLimited
			if !errors.As(err, &rl) {
				t.Fatalf("err = %v (%T), want *RateLimited", err, err)
			}
			assertNoCredential(t, err)
			if !errors.Is(err, ErrUpstream) || !Retryable(err) {
				t.Errorf("rate limit not matched as retryable upstream error: %v", err)
			}
			if rl.Status != tt.wantStatus || result.Status != tt.wantStatus {
				t.Errorf("status = %d / result %d, want %d", rl.Status, result.Status, tt.wantStatus)
			}
			if result.Decision != domain.ConnectorCallUpstreamError {
				t.Errorf("decision = %v, want upstream error", result.Decision)
			}
			if tt.wantRetryZero {
				if rl.RetryAfter != 0 {
					t.Errorf("RetryAfter = %v, want 0", rl.RetryAfter)
				}
				return
			}
			if rl.RetryAfter < tt.wantRetryMin || rl.RetryAfter > tt.wantRetryMax {
				t.Errorf("RetryAfter = %v, want in [%v, %v]", rl.RetryAfter, tt.wantRetryMin, tt.wantRetryMax)
			}
		})
	}
}

func TestRetryAfterFromHeaders(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"none", h(), 0},
		{"retry-after seconds", h("Retry-After", "30"), 30 * time.Second},
		{"retry-after wins over reset", h("Retry-After", "3", "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "1000100"), 3 * time.Second},
		{"exhausted with reset", h("X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "1000100"), 100 * time.Second},
		{"remaining non-zero ignores reset", h("X-RateLimit-Remaining", "12", "X-RateLimit-Reset", "1000100"), 0},
		{"reset in the past clamps to zero", h("X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "999000"), 0},
		{"malformed reset", h("X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "soon"), 0},
		{"negative retry-after falls through to reset", h("Retry-After", "-4", "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "1000010"), 10 * time.Second},
		{"http-date retry-after falls through", h("Retry-After", "Wed, 21 Oct 2015 07:28:00 GMT"), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryAfterFromHeaders(tt.header, now); got != tt.want {
				t.Errorf("retryAfterFromHeaders = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGitHubReadinessRepositoryErrors(t *testing.T) {
	tests := []struct {
		name       string
		response   fakeResponse
		wantStatus int
		wantClass  string
	}{
		{
			name:       "null repository NOT_FOUND is 404",
			response:   fakeResponse{status: http.StatusOK, body: `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"message":"Could not resolve to a Repository with the name 'octocat/hello'."}]}`},
			wantStatus: http.StatusNotFound,
			wantClass:  ClassClientError,
		},
		{
			name:       "null repository FORBIDDEN is 403",
			response:   fakeResponse{status: http.StatusOK, body: `{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","path":["repository"],"message":"Resource not accessible by integration"}]}`},
			wantStatus: http.StatusForbidden,
			wantClass:  ClassClientError,
		},
		{
			name:       "no data and unknown error is a server error",
			response:   fakeResponse{status: http.StatusOK, body: `{"errors":[{"type":"INTERNAL","message":"boom"}]}`},
			wantStatus: http.StatusBadGateway,
			wantClass:  ClassServerError,
		},
		{
			name:       "http 401 is a client error",
			response:   fakeResponse{status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`},
			wantStatus: http.StatusUnauthorized,
			wantClass:  ClassClientError,
		},
		{
			name:       "http 502 is a server error",
			response:   fakeResponse{status: http.StatusBadGateway, body: `{"message":"bad gateway"}`},
			wantStatus: http.StatusBadGateway,
			wantClass:  ClassServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodPost, "/graphql", tt.response)
			result, err := fake.provider().Call(context.Background(), readinessSpec(7))
			var up *UpstreamError
			if !errors.As(err, &up) {
				t.Fatalf("err = %v (%T), want *UpstreamError", err, err)
			}
			assertNoCredential(t, err)
			if up.Status != tt.wantStatus || up.Class != tt.wantClass {
				t.Errorf("upstream = status %d class %s, want %d %s", up.Status, up.Class, tt.wantStatus, tt.wantClass)
			}
			if result.Decision != domain.ConnectorCallUpstreamError {
				t.Errorf("decision = %v", result.Decision)
			}
		})
	}
}

func TestGitHubReadinessInvalidNumbers(t *testing.T) {
	tooMany := make([]any, MaxReadinessNumbers+1)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	exactlyMax := tooMany[:MaxReadinessNumbers]
	tests := []struct {
		name string
		args map[string]any
	}{
		{"missing numbers", map[string]any{"owner": "octocat", "repo": "hello"}},
		{"numbers not a list", map[string]any{"owner": "octocat", "repo": "hello", "numbers": 7}},
		{"empty list", map[string]any{"owner": "octocat", "repo": "hello", "numbers": []any{}}},
		{"more than max", map[string]any{"owner": "octocat", "repo": "hello", "numbers": tooMany}},
		{"zero", map[string]any{"owner": "octocat", "repo": "hello", "numbers": []any{0}}},
		{"negative", map[string]any{"owner": "octocat", "repo": "hello", "numbers": []any{float64(-3)}}},
		{"fractional", map[string]any{"owner": "octocat", "repo": "hello", "numbers": []any{1.5}}},
		{"string", map[string]any{"owner": "octocat", "repo": "hello", "numbers": []any{"7"}}},
		{"missing owner", map[string]any{"repo": "hello", "numbers": []any{7}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			spec := readinessSpec()
			spec.Args = tt.args
			_, err := fake.provider().Call(context.Background(), spec)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if n := len(fake.recorded()); n != 0 {
				t.Fatalf("fake saw %d requests, want none for invalid args", n)
			}
		})
	}

	t.Run("exactly max is accepted", func(t *testing.T) {
		fake := newFakeGitHub(t)
		fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK, body: `{"data":{"repository":{}}}`})
		if _, err := fake.provider().Call(context.Background(), readinessSpec(exactlyMax...)); err != nil {
			t.Fatalf("readiness with %d numbers: %v", MaxReadinessNumbers, err)
		}
	})

	t.Run("[]int numbers accepted", func(t *testing.T) {
		fake := newFakeGitHub(t)
		fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK, body: `{"data":{"repository":{}}}`})
		spec := readinessSpec()
		spec.Args["numbers"] = []int{4, 5}
		if _, err := fake.provider().Call(context.Background(), spec); err != nil {
			t.Fatalf("readiness with []int: %v", err)
		}
	})
}

func TestGitHubReadinessMalformedJSON(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPost, "/graphql", fakeResponse{status: http.StatusOK, body: `{"data": not json ` + testToken})
	result, err := fake.provider().Call(context.Background(), readinessSpec(7))
	if err == nil {
		t.Fatal("malformed JSON: want error")
	}
	assertNoCredential(t, err)
	if result.Decision != domain.ConnectorCallUpstreamError {
		t.Fatalf("decision = %v", result.Decision)
	}
}

func TestGitHubReadinessHonorsContextDeadline(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := NewGitHub(server.Client(), server.URL).Call(ctx, readinessSpec(7))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("slow server: want error, got nil")
		}
		assertNoCredential(t, err)
		var up *UpstreamError
		if !errors.As(err, &up) || up.Class != ClassNetwork {
			t.Fatalf("err = %v (%T), want network UpstreamError", err, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readiness call hung past its context deadline")
	}
}
