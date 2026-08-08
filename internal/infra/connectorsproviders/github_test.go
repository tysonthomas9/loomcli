package connectorsproviders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	legacydomain "github.com/tysonthomas9/loomcli/internal/domain"
	domain "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

const testToken = "ghp_SECRETtoken1234567890abcdef"

// recordedRequest captures one request the fake GitHub received.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   map[string]any
}

// fakeGitHub is an httptest-backed GitHub API double that records every
// request and replies from a route table keyed by "METHOD path".
type fakeGitHub struct {
	t  *testing.T
	mu sync.Mutex

	requests []recordedRequest
	routes   map[string]fakeResponse

	server *httptest.Server
}

type fakeResponse struct {
	status  int
	header  map[string]string
	body    string
	respond func(recordedRequest) fakeResponse
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{t: t, routes: map[string]fakeResponse{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGitHub) route(method, path string, resp fakeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = resp
}

func (f *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	recorded := recordedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   body,
	}
	f.mu.Lock()
	f.requests = append(f.requests, recorded)
	resp, ok := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if !ok {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	if resp.respond != nil {
		resp = resp.respond(recorded)
	}
	for k, v := range resp.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(resp.status)
	_, _ = w.Write([]byte(resp.body))
}

func (f *fakeGitHub) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeGitHub) provider() *GitHub {
	return NewGitHub(f.server.Client(), f.server.URL)
}

func mergeSpec() CallSpec {
	return CallSpec{
		Action:         ActionGitHubMerge,
		Resource:       "repo:octocat/hello",
		Args:           map[string]any{"owner": "octocat", "repo": "hello", "number": 7},
		Preconditions:  Preconditions{ExpectedHeadSha: "deadbeef"},
		IdempotencyKey: "run-1#github.merge#0",
		Credential:     testToken,
	}
}

func reviewSpec() CallSpec {
	return CallSpec{
		Action:   ActionGitHubReviewPost,
		Resource: "repo:octocat/hello",
		Args: map[string]any{
			"owner": "octocat", "repo": "hello", "number": float64(7),
			"event": "APPROVE", "body": "lgtm",
		},
		Preconditions:  Preconditions{ExpectedHeadSha: "deadbeef"},
		IdempotencyKey: "run-1#github.review.post#0",
		Credential:     testToken,
	}
}

func pullsListSpec() CallSpec {
	return CallSpec{
		Action:     ActionGitHubPullsList,
		Resource:   "repo:octocat/hello",
		Args:       map[string]any{"owner": "octocat", "repo": "hello"},
		Credential: testToken,
	}
}

func assertNoCredential(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("credential leaked into error text: %v", err)
	}
}

func TestGitHubActionsPassActionGrammar(t *testing.T) {
	for _, action := range GitHubActions() {
		if err := legacydomain.ValidateConnectorAction(action); err != nil {
			t.Errorf("action %q fails the CV1 grammar: %v", action, err)
		}
	}
}

func TestGitHubMergeHappyPathSendsShaAndIdempotencyKey(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPut, "/repos/octocat/hello/pulls/7/merge", fakeResponse{
		status: http.StatusOK,
		body:   `{"sha":"abc123","merged":true,"message":"Pull Request successfully merged"}`,
	})

	result, err := fake.provider().Call(context.Background(), mergeSpec())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if result.Status != http.StatusOK || result.Decision != domain.ConnectorCallGranted {
		t.Fatalf("result = %+v, want status 200 granted", result)
	}
	if result.Body["merged"] != true || result.Body["sha"] != "abc123" {
		t.Fatalf("body = %+v, want merged true sha abc123", result.Body)
	}

	reqs := fake.recorded()
	if len(reqs) != 1 {
		t.Fatalf("fake saw %d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if got := req.Body["sha"]; got != "deadbeef" {
		t.Errorf("merge body sha = %v, want the expectedHeadSha precondition", got)
	}
	if got := req.Header.Get("Idempotency-Key"); got != "run-1#github.merge#0" {
		t.Errorf("Idempotency-Key = %q, want runID-derived key", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want bearer credential", got)
	}
}

func TestGitHubMergeFailures(t *testing.T) {
	tests := []struct {
		name          string
		response      fakeResponse
		wantStale     bool
		wantRateLimit bool
		wantClass     string
		wantRetryable bool
		wantDecision  domain.ConnectorCallDecision
	}{
		{
			name: "409 stale head maps to StaleSubject",
			response: fakeResponse{
				status: http.StatusConflict,
				body:   `{"message":"Head branch was modified. Review and try the merge again."}`,
			},
			wantStale:    true,
			wantDecision: domain.ConnectorCallStaleSubject,
		},
		{
			name: "403 rate limit maps to retryable RateLimited",
			response: fakeResponse{
				status: http.StatusForbidden,
				header: map[string]string{"X-RateLimit-Remaining": "0", "Retry-After": "30"},
				body:   `{"message":"API rate limit exceeded for installation."}`,
			},
			wantRateLimit: true,
			wantRetryable: true,
			wantDecision:  domain.ConnectorCallUpstreamError,
		},
		{
			name: "429 maps to retryable RateLimited",
			response: fakeResponse{
				status: http.StatusTooManyRequests,
				body:   `{"message":"too many requests"}`,
			},
			wantRateLimit: true,
			wantRetryable: true,
			wantDecision:  domain.ConnectorCallUpstreamError,
		},
		{
			name: "405 not mergeable is a non-retryable client error",
			response: fakeResponse{
				status: http.StatusMethodNotAllowed,
				body:   `{"message":"Pull Request is not mergeable"}`,
			},
			wantClass:    ClassClientError,
			wantDecision: domain.ConnectorCallUpstreamError,
		},
		{
			name: "500 echoing the token is retryable and redacted",
			response: fakeResponse{
				status: http.StatusInternalServerError,
				body:   `{"message":"internal error processing token ` + testToken + `"}`,
			},
			wantClass:     ClassServerError,
			wantRetryable: true,
			wantDecision:  domain.ConnectorCallUpstreamError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodPut, "/repos/octocat/hello/pulls/7/merge", tt.response)

			result, err := fake.provider().Call(context.Background(), mergeSpec())
			if err == nil {
				t.Fatal("want error")
			}
			assertNoCredential(t, err)
			if result.Decision != tt.wantDecision {
				t.Errorf("decision = %q, want %q", result.Decision, tt.wantDecision)
			}
			if result.Status != tt.response.status {
				t.Errorf("result status = %d, want %d", result.Status, tt.response.status)
			}
			if Retryable(err) != tt.wantRetryable {
				t.Errorf("Retryable = %v, want %v", Retryable(err), tt.wantRetryable)
			}
			if tt.wantStale {
				var stale *StaleSubject
				if !errors.As(err, &stale) {
					t.Fatalf("error %T is not *StaleSubject", err)
				}
				if stale.Expected != "deadbeef" {
					t.Errorf("stale.Expected = %q", stale.Expected)
				}
				if !errors.Is(err, domain.ErrConflict) {
					t.Error("StaleSubject must match domain.ErrConflict")
				}
				if DecisionForError(err) != domain.ConnectorCallStaleSubject {
					t.Error("DecisionForError must classify StaleSubject as stale_subject")
				}
			}
			if tt.wantRateLimit {
				var rl *RateLimited
				if !errors.As(err, &rl) {
					t.Fatalf("error %T is not *RateLimited", err)
				}
				if !rl.Retryable() {
					t.Error("RateLimited must be retryable")
				}
				if !errors.Is(err, ErrUpstream) {
					t.Error("RateLimited must match ErrUpstream")
				}
			}
			if tt.wantClass != "" {
				var ue *UpstreamError
				if !errors.As(err, &ue) {
					t.Fatalf("error %T is not *UpstreamError", err)
				}
				if ue.Class != tt.wantClass {
					t.Errorf("class = %q, want %q", ue.Class, tt.wantClass)
				}
				if strings.Contains(ue.Summary, testToken) {
					t.Errorf("summary leaked credential: %q", ue.Summary)
				}
			}
		})
	}
}

func TestGitHubMergeRetryAfterParsed(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPut, "/repos/octocat/hello/pulls/7/merge", fakeResponse{
		status: http.StatusForbidden,
		header: map[string]string{"X-RateLimit-Remaining": "0", "Retry-After": "30"},
		body:   `{"message":"API rate limit exceeded"}`,
	})
	_, err := fake.provider().Call(context.Background(), mergeSpec())
	var rl *RateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("error %T is not *RateLimited", err)
	}
	if rl.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", rl.RetryAfter)
	}
}

func TestGitHubMergePreconditionRequired(t *testing.T) {
	fake := newFakeGitHub(t)
	spec := mergeSpec()
	spec.Preconditions.ExpectedHeadSha = ""

	result, err := fake.provider().Call(context.Background(), spec)
	var pre *PreconditionRequired
	if !errors.As(err, &pre) {
		t.Fatalf("error %T is not *PreconditionRequired", err)
	}
	if len(pre.Fields) != 1 || pre.Fields[0] != "expectedHeadSha" {
		t.Errorf("fields = %v, want [expectedHeadSha]", pre.Fields)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Error("PreconditionRequired must match domain.ErrInvalid")
	}
	if result.Decision != domain.ConnectorCallPreconditionRequired {
		t.Errorf("decision = %q, want precondition_required", result.Decision)
	}
	if DecisionForError(err) != domain.ConnectorCallPreconditionRequired {
		t.Error("DecisionForError must classify PreconditionRequired")
	}
	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0 (refused before egress)", n)
	}
}

func TestGitHubWritesRequireIdempotencyKey(t *testing.T) {
	fake := newFakeGitHub(t)
	for _, spec := range []CallSpec{mergeSpec(), reviewSpec()} {
		spec.IdempotencyKey = ""
		_, err := fake.provider().Call(context.Background(), spec)
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s without idempotency key: err = %v, want domain.ErrInvalid", spec.Action, err)
		}
	}
	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0", n)
	}
}

func TestGitHubReviewPost(t *testing.T) {
	prPath := "/repos/octocat/hello/pulls/7"
	openPR := `{"number":7,"state":"open","head":{"sha":"deadbeef","ref":"feat"},"base":{"sha":"aaa","ref":"main"}}`
	reviewURL := "https://github.com/octocat/hello/pull/7#pullrequestreview-42"

	tests := []struct {
		name        string
		prResponse  fakeResponse
		wantStale   bool
		staleReason string
	}{
		{
			name:       "happy path posts review after liveness read",
			prResponse: fakeResponse{status: http.StatusOK, body: openPR},
		},
		{
			name: "closed PR refuses without issuing the write",
			prResponse: fakeResponse{
				status: http.StatusOK,
				body:   `{"number":7,"state":"closed","head":{"sha":"deadbeef"}}`,
			},
			wantStale:   true,
			staleReason: "not open",
		},
		{
			name: "moved head refuses without issuing the write",
			prResponse: fakeResponse{
				status: http.StatusOK,
				body:   `{"number":7,"state":"open","head":{"sha":"0ther5ha"}}`,
			},
			wantStale:   true,
			staleReason: "moved",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodGet, prPath, tt.prResponse)
			fake.route(http.MethodPost, prPath+"/reviews", fakeResponse{
				status: http.StatusOK,
				body:   `{"id":42,"state":"APPROVED","html_url":"` + reviewURL + `","url":"https://api.github.com/repos/octocat/hello/pulls/7/reviews/42"}`,
			})

			spec := reviewSpec()
			// commit_id is provider-owned: a workflow cannot override the
			// ExpectedHeadSha precondition through ordinary args.
			spec.Args["commit_id"] = "workflow-controlled-sha"
			spec.Args["comments"] = []any{
				map[string]any{
					"path": "internal/worker.go",
					"line": float64(73),
					"body": "Release the lease on this error path.",
				},
			}
			result, err := fake.provider().Call(context.Background(), spec)
			reqs := fake.recorded()
			for _, req := range reqs {
				if got := req.Header.Get("Idempotency-Key"); got != "run-1#github.review.post#0" {
					t.Errorf("%s %s Idempotency-Key = %q, want runID-derived key", req.Method, req.Path, got)
				}
			}

			if tt.wantStale {
				var stale *StaleSubject
				if !errors.As(err, &stale) {
					t.Fatalf("error %T is not *StaleSubject (err=%v)", err, err)
				}
				if !strings.Contains(stale.Reason, tt.staleReason) {
					t.Errorf("reason = %q, want substring %q", stale.Reason, tt.staleReason)
				}
				if result.Decision != domain.ConnectorCallStaleSubject {
					t.Errorf("decision = %q, want stale_subject", result.Decision)
				}
				assertNoCredential(t, err)
				for _, req := range reqs {
					if req.Method == http.MethodPost {
						t.Fatalf("write was issued despite stale subject: %s %s", req.Method, req.Path)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("review post: %v", err)
			}
			if result.Decision != domain.ConnectorCallGranted {
				t.Errorf("decision = %q, want granted", result.Decision)
			}
			if result.Body["id"] != float64(42) || result.Body["state"] != "APPROVED" ||
				result.Body["htmlUrl"] != reviewURL {
				t.Errorf("body = %+v", result.Body)
			}
			if _, ok := result.Body["html_url"]; ok {
				t.Errorf("body contains upstream snake_case html_url: %+v", result.Body)
			}
			if _, ok := result.Body["url"]; ok {
				t.Errorf("body exposes the raw API URL: %+v", result.Body)
			}
			if len(reqs) != 2 || reqs[0].Method != http.MethodGet || reqs[1].Method != http.MethodPost {
				t.Fatalf("requests = %+v, want GET then POST", reqs)
			}
			wantBody := `lgtm

Unanchored review findings:
- file "internal/worker.go", unverified line 73: Release the lease on this error path.`
			if reqs[1].Body["event"] != "APPROVE" || reqs[1].Body["body"] != wantBody {
				t.Errorf("review payload = %+v", reqs[1].Body)
			}
			if reqs[1].Body["commit_id"] != "deadbeef" {
				t.Errorf("review commit_id = %#v, want expected head sha", reqs[1].Body["commit_id"])
			}
			if _, ok := reqs[1].Body["comments"]; ok {
				t.Errorf("review payload contains uncertified inline comments: %+v", reqs[1].Body)
			}
		})
	}
}

func TestGitHubReviewPostRejectsUnsafeInlineCommentsBeforeEgress(t *testing.T) {
	validComment := func() map[string]any {
		return map[string]any{
			"path": "internal/worker.go",
			"line": float64(73),
			"body": "Release the lease on this error path.",
		}
	}
	tooMany := make([]any, maxGitHubReviewComments+1)
	for i := range tooMany {
		tooMany[i] = validComment()
	}

	tests := []struct {
		name     string
		comments any
	}{
		{name: "comments must be an array", comments: map[string]any{"0": validComment()}},
		{name: "comment count is bounded", comments: tooMany},
		{name: "entry must be an object", comments: []any{"not-an-object"}},
		{name: "unknown fields are rejected", comments: []any{map[string]any{
			"path": "internal/worker.go", "line": 73, "body": "finding", testToken: "payload",
		}}},
		{name: "side is not in the public finding schema", comments: []any{map[string]any{
			"path": "internal/worker.go", "line": 73, "side": "LEFT", "body": "finding",
		}}},
		{name: "path is required", comments: []any{map[string]any{"line": 73, "body": "finding"}}},
		{name: "path must be relative", comments: []any{map[string]any{
			"path": "/etc/passwd", "line": 73, "body": "finding",
		}}},
		{name: "path cannot traverse", comments: []any{map[string]any{
			"path": "internal/../secrets.txt", "line": 73, "body": "finding",
		}}},
		{name: "path cannot start above repository", comments: []any{map[string]any{
			"path": "../secrets.txt", "line": 73, "body": "finding",
		}}},
		{name: "path cannot contain control characters", comments: []any{map[string]any{
			"path": "internal/\nworker.go", "line": 73, "body": "finding",
		}}},
		{name: "path is bounded", comments: []any{map[string]any{
			"path": strings.Repeat("a", maxGitHubReviewPathBytes+1), "line": 73, "body": "finding",
		}}},
		{name: "path must be valid UTF-8", comments: []any{map[string]any{
			"path": string([]byte{'a', 0xff}), "line": 73, "body": "finding",
		}}},
		{name: "body is required", comments: []any{map[string]any{
			"path": "internal/worker.go", "line": 73,
		}}},
		{name: "body rejects unsafe controls", comments: []any{map[string]any{
			"path": "internal/worker.go", "line": 73, "body": "finding\x00" + testToken,
		}}},
		{name: "body is bounded", comments: []any{map[string]any{
			"path": "internal/worker.go", "line": 73,
			"body": strings.Repeat("x", maxGitHubReviewCommentBodyBytes+1),
		}}},
		{name: "body must be valid UTF-8", comments: []any{map[string]any{
			"path": "internal/worker.go", "line": 73, "body": string([]byte{'a', 0xff}),
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			spec := reviewSpec()
			spec.Args["comments"] = tt.comments

			result, err := fake.provider().Call(context.Background(), spec)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("error = %v, want domain.ErrInvalid", err)
			}
			if result.Decision != domain.ConnectorCallUpstreamError {
				t.Fatalf("decision = %q, want upstream_error", result.Decision)
			}
			assertNoCredential(t, err)
			if n := len(fake.recorded()); n != 0 {
				t.Fatalf("fake saw %d request(s), want zero (invalid comments refused before egress)", n)
			}
		})
	}
}

func TestGitHubReviewPostFallsBackAllUncertifiedCommentsAndStillPosts(t *testing.T) {
	const (
		prPath = "/repos/octocat/hello/pulls/7"
		openPR = `{"number":7,"state":"open","head":{"sha":"deadbeef"}}`
	)
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, prPath, fakeResponse{status: http.StatusOK, body: openPR})
	fake.route(http.MethodPost, prPath+"/reviews", fakeResponse{
		respond: func(req recordedRequest) fakeResponse {
			if _, carriesUncertifiedAnchors := req.Body["comments"]; carriesUncertifiedAnchors {
				return fakeResponse{
					status: http.StatusUnprocessableEntity,
					body:   `{"message":"Validation Failed","errors":[{"resource":"PullRequestReviewComment","field":"line","code":"invalid"}]}`,
				}
			}
			return fakeResponse{
				status: http.StatusCreated,
				body:   `{"id":42,"state":"COMMENTED","html_url":"https://github.com/octocat/hello/pull/7#pullrequestreview-42"}`,
			}
		},
	})

	spec := reviewSpec()
	spec.Args["event"] = "COMMENT"
	spec.Args["body"] = "Review summary."
	spec.Args["comments"] = []any{
		map[string]any{
			"path": "internal/anchored.go",
			"line": float64(73),
			"body": "This arbitrary positive line is not proof of a RIGHT-side diff anchor.",
		},
		map[string]any{
			"path": "internal/missing.go",
			"body": "The model omitted a line.",
		},
		map[string]any{
			"path": "internal/fractional.go",
			"line": 73.5,
			"body": "The model returned a fractional line.",
		},
		map[string]any{
			"path": "internal/deletion.go",
			"line": 0,
			"body": "A deletion-side finding cannot use the RIGHT-only schema.",
		},
		map[string]any{
			"path": "internal/unbounded.go",
			"line": float64(maxGitHubReviewLine + 1),
			"body": "The model returned an out-of-range line.",
		},
		map[string]any{
			"path": "internal/wrong-type.go",
			"line": map[string]any{"value": 73},
			"body": "The model returned a non-scalar line.",
		},
	}

	result, err := fake.provider().Call(context.Background(), spec)
	if err != nil {
		t.Fatalf("review post: %v", err)
	}
	if result.Decision != domain.ConnectorCallGranted {
		t.Fatalf("decision = %q, want granted", result.Decision)
	}
	if result.Status != http.StatusCreated {
		t.Fatalf("status = %d, want created", result.Status)
	}
	reqs := fake.recorded()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want GET then POST", len(reqs))
	}
	post := reqs[1].Body
	if post["commit_id"] != "deadbeef" {
		t.Errorf("commit_id = %#v, want deadbeef", post["commit_id"])
	}
	wantBody := `Review summary.

Unanchored review findings:
- file "internal/anchored.go", unverified line 73: This arbitrary positive line is not proof of a RIGHT-side diff anchor.
- file "internal/missing.go": The model omitted a line.
- file "internal/fractional.go": The model returned a fractional line.
- file "internal/deletion.go": A deletion-side finding cannot use the RIGHT-only schema.
- file "internal/unbounded.go": The model returned an out-of-range line.
- file "internal/wrong-type.go": The model returned a non-scalar line.`
	if post["body"] != wantBody {
		t.Errorf("body = %q, want %q", post["body"], wantBody)
	}
	if _, ok := post["comments"]; ok {
		t.Fatalf("POST carried uncertified inline comments that can make GitHub reject the review: %#v", post)
	}
}

func TestGitHubReviewPostValidatesEventAndBodyBeforeEgress(t *testing.T) {
	tests := []struct {
		name   string
		event  string
		body   any
		mutate func(map[string]any)
	}{
		{
			name:  "event is a documented value",
			event: "PENDING",
			body:  "review",
		},
		{
			name:  "COMMENT requires body",
			event: "COMMENT",
			mutate: func(args map[string]any) {
				delete(args, "body")
			},
		},
		{
			name:  "body must be a string",
			event: "COMMENT",
			body:  map[string]any{"text": "review"},
		},
		{
			name:  "body is bounded",
			event: "COMMENT",
			body:  strings.Repeat("x", maxGitHubReviewBodyBytes+1),
		},
		{
			name:  "body rejects unsafe controls",
			event: "COMMENT",
			body:  "review\x00" + testToken,
		},
		{
			name:  "body must be valid UTF-8",
			event: "COMMENT",
			body:  string([]byte{'a', 0xff}),
		},
		{
			name:  "degraded comments keep the aggregate body bounded",
			event: "COMMENT",
			body:  strings.Repeat("x", maxGitHubReviewBodyBytes),
			mutate: func(args map[string]any) {
				args["comments"] = []any{map[string]any{
					"path": "internal/worker.go",
					"body": "unanchored finding",
				}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			spec := reviewSpec()
			spec.Args["event"] = tt.event
			if tt.body != nil {
				spec.Args["body"] = tt.body
			}
			if tt.mutate != nil {
				tt.mutate(spec.Args)
			}

			result, err := fake.provider().Call(context.Background(), spec)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("error = %v, want domain.ErrInvalid", err)
			}
			if result.Decision != domain.ConnectorCallUpstreamError {
				t.Fatalf("decision = %q, want upstream_error", result.Decision)
			}
			assertNoCredential(t, err)
			if n := len(fake.recorded()); n != 0 {
				t.Fatalf("fake saw %d request(s), want zero (invalid review refused before egress)", n)
			}
		})
	}
}

func TestGitHubReviewPostRejectsMalformedSuccessResponse(t *testing.T) {
	const (
		prPath = "/repos/octocat/hello/pulls/7"
		openPR = `{"number":7,"state":"open","head":{"sha":"deadbeef"}}`
	)
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing id",
			body: `{"state":"COMMENTED","html_url":"https://github.com/octocat/hello/pull/7#pullrequestreview-42"}`,
		},
		{
			name: "invalid id",
			body: `{"id":{"nested":"payload"},"state":"COMMENTED","html_url":"https://github.com/octocat/hello/pull/7#pullrequestreview-42"}`,
		},
		{
			name: "missing state",
			body: `{"id":42,"html_url":"https://github.com/octocat/hello/pull/7#pullrequestreview-42"}`,
		},
		{
			name: "state control character",
			body: `{"id":42,"state":"COMMENTED\u0000` + testToken + `","html_url":"https://github.com/octocat/hello/pull/7#pullrequestreview-42"}`,
		},
		{
			name: "missing html URL",
			body: `{"id":42,"state":"COMMENTED"}`,
		},
		{
			name: "non HTTP URL",
			body: `{"id":42,"state":"COMMENTED","html_url":"javascript:alert(1)` + testToken + `"}`,
		},
		{
			name: "URL userinfo",
			body: `{"id":42,"state":"COMMENTED","html_url":"https://user:` + testToken + `@github.com/octocat/hello/pull/7"}`,
		},
		{
			name: "URL control character",
			body: `{"id":42,"state":"COMMENTED","html_url":"https://github.com/octocat/hello/\u0000` + testToken + `"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodGet, prPath, fakeResponse{status: http.StatusOK, body: openPR})
			fake.route(http.MethodPost, prPath+"/reviews", fakeResponse{status: http.StatusOK, body: tt.body})

			result, err := fake.provider().Call(context.Background(), reviewSpec())
			var upstream *UpstreamError
			if !errors.As(err, &upstream) {
				t.Fatalf("error %T is not *UpstreamError (err=%v)", err, err)
			}
			if upstream.Class != ClassServerError || !Retryable(err) {
				t.Fatalf("upstream = %+v, want retryable server_error", upstream)
			}
			if result.Status != http.StatusOK || result.Decision != domain.ConnectorCallUpstreamError {
				t.Fatalf("result = %+v, want status 200 upstream_error", result)
			}
			assertNoCredential(t, err)
			if reqs := fake.recorded(); len(reqs) != 2 ||
				reqs[0].Method != http.MethodGet || reqs[1].Method != http.MethodPost {
				t.Fatalf("requests = %+v, want GET then POST", reqs)
			}
		})
	}
}

func TestGitHubReviewPostRequiresExpectedHeadSha(t *testing.T) {
	fake := newFakeGitHub(t)
	spec := reviewSpec()
	spec.Preconditions.ExpectedHeadSha = ""

	result, err := fake.provider().Call(context.Background(), spec)
	var pre *PreconditionRequired
	if !errors.As(err, &pre) {
		t.Fatalf("error %T is not *PreconditionRequired", err)
	}
	if result.Decision != domain.ConnectorCallPreconditionRequired {
		t.Errorf("decision = %q", result.Decision)
	}
	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0", n)
	}
}

func TestGitHubReads(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/repos/octocat/hello/pulls/7", fakeResponse{
		status: http.StatusOK,
		body: `{"number":7,"state":"open","title":"feat","draft":false,"merged":false,
			"head":{"sha":"deadbeef","ref":"feat"},"base":{"sha":"aaa","ref":"main"}}`,
	})
	fake.route(http.MethodGet, "/repos/octocat/hello/pulls", fakeResponse{
		status: http.StatusOK,
		body: `[{"number":7,"state":"closed","title":"feat","merged_at":"2026-07-13T12:00:00Z",
			"updated_at":"2026-07-13T13:00:00Z","user":{"login":"octocat"},
			"head":{"sha":"deadbeef","ref":"feat"},"base":{"ref":"main"}}]`,
	})
	fake.route(http.MethodGet, "/repos/octocat/hello/compare/main...feat", fakeResponse{
		status: http.StatusOK,
		body:   `{"status":"ahead","ahead_by":2,"behind_by":0,"total_commits":2}`,
	})
	provider := fake.provider()

	t.Run("pull_request.read", func(t *testing.T) {
		result, err := provider.Call(context.Background(), CallSpec{
			Action:     ActionGitHubPullRequestRead,
			Args:       map[string]any{"owner": "octocat", "repo": "hello", "number": 7},
			Credential: testToken,
		})
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if result.Body["headSha"] != "deadbeef" || result.Body["baseRef"] != "main" {
			t.Errorf("body = %+v, want camelCase headSha/baseRef", result.Body)
		}
		if result.Decision != domain.ConnectorCallGranted {
			t.Errorf("decision = %q", result.Decision)
		}
	})

	t.Run("pulls.list", func(t *testing.T) {
		result, err := provider.Call(context.Background(), CallSpec{
			Action: ActionGitHubPullsList,
			Args: map[string]any{
				"owner": "octocat", "repo": "hello",
				"state": "open", "base": "main", "perPage": 50, "page": 2,
			},
			Credential: testToken,
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		pulls, ok := result.Body["pullRequests"].([]map[string]any)
		if !ok || len(pulls) != 1 || pulls[0]["headSha"] != "deadbeef" {
			t.Errorf("body = %+v", result.Body)
		}
		if pulls[0]["merged"] != true || pulls[0]["authorLogin"] != "octocat" ||
			pulls[0]["updatedAt"] != "2026-07-13T13:00:00Z" {
			t.Errorf("projected list fields = %+v", pulls[0])
		}
		reqs := fake.recorded()
		last := reqs[len(reqs)-1]
		for _, want := range []string{"state=open", "base=main", "per_page=50", "page=2"} {
			if !strings.Contains(last.Query, want) {
				t.Errorf("query %q missing %q", last.Query, want)
			}
		}
	})

	t.Run("compare.read", func(t *testing.T) {
		result, err := provider.Call(context.Background(), CallSpec{
			Action:     ActionGitHubCompareRead,
			Args:       map[string]any{"owner": "octocat", "repo": "hello", "base": "main", "head": "feat"},
			Credential: testToken,
		})
		if err != nil {
			t.Fatalf("compare: %v", err)
		}
		if result.Body["aheadBy"] != float64(2) || result.Body["status"] != "ahead" {
			t.Errorf("body = %+v, want camelCase aheadBy", result.Body)
		}
	})
}

func TestGitHubPullsListRejectsOversizedResponse(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/repos/octocat/hello/pulls", fakeResponse{
		status: http.StatusOK,
		body:   strings.Repeat("x", maxResponseBytes+1),
	})

	result, err := fake.provider().Call(context.Background(), pullsListSpec())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error %T is not *UpstreamError", err)
	}
	if result.Decision != domain.ConnectorCallUpstreamError {
		t.Fatalf("decision = %q, want upstream_error", result.Decision)
	}
	want := fmt.Sprintf("response exceeded %d bytes", maxResponseBytes)
	if upstream.Summary != want {
		t.Fatalf("summary = %q, want %q", upstream.Summary, want)
	}
}

func TestGitHubPullsListRejectsMalformedJSON(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/repos/octocat/hello/pulls", fakeResponse{
		status: http.StatusOK,
		body:   `[{`,
	})

	result, err := fake.provider().Call(context.Background(), pullsListSpec())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error %T is not *UpstreamError", err)
	}
	if result.Decision != domain.ConnectorCallUpstreamError {
		t.Fatalf("decision = %q, want upstream_error", result.Decision)
	}
	if !strings.Contains(upstream.Summary, "invalid JSON response") {
		t.Fatalf("summary = %q, want parse context", upstream.Summary)
	}
}

func TestGitHubPullsListAcceptsResponseExactlyAtCap(t *testing.T) {
	const prefix = `[{"number":7,"state":"open","title":"feat","padding":"`
	const suffix = `"}]`
	body := prefix + strings.Repeat("x", maxResponseBytes-len(prefix)-len(suffix)) + suffix
	if len(body) != maxResponseBytes {
		t.Fatalf("test body size = %d, want %d", len(body), maxResponseBytes)
	}
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/repos/octocat/hello/pulls", fakeResponse{
		status: http.StatusOK,
		body:   body,
	})

	result, err := fake.provider().Call(context.Background(), pullsListSpec())
	if err != nil {
		t.Fatalf("list exactly at cap: %v", err)
	}
	pulls, ok := result.Body["pullRequests"].([]map[string]any)
	if !ok || len(pulls) != 1 || pulls[0]["number"] != float64(7) {
		t.Fatalf("pulls = %+v, want one projected PR", result.Body["pullRequests"])
	}
}

func TestGitHubIssueCommentPost(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodPost, "/repos/octocat/hello/issues/7/comments", fakeResponse{
		status: http.StatusCreated,
		body:   `{"id":99}`,
	})
	result, err := fake.provider().Call(context.Background(), CallSpec{
		Action:         ActionGitHubIssueCommentPost,
		Args:           map[string]any{"owner": "octocat", "repo": "hello", "number": 7, "body": "done"},
		IdempotencyKey: "run-1#github.issue_comment.post#0",
		Credential:     testToken,
	})
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if result.Status != http.StatusCreated || result.Body["id"] != float64(99) {
		t.Errorf("result = %+v", result)
	}
	req := fake.recorded()[0]
	if req.Body["body"] != "done" {
		t.Errorf("payload = %+v", req.Body)
	}
}

func TestGitHubUnknownActionAndBadArgs(t *testing.T) {
	fake := newFakeGitHub(t)
	provider := fake.provider()

	_, err := provider.Call(context.Background(), CallSpec{Action: "github.repo.delete"})
	if !errors.Is(err, ErrUnknownAction) || !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown action err = %v, want ErrUnknownAction wrapping domain.ErrInvalid", err)
	}

	_, err = provider.Call(context.Background(), CallSpec{
		Action: ActionGitHubPullRequestRead,
		Args:   map[string]any{"owner": "octocat", "repo": "hello", "number": "seven"},
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad number err = %v, want domain.ErrInvalid", err)
	}

	_, err = provider.Call(context.Background(), CallSpec{
		Action: ActionGitHubPullRequestRead,
		Args:   map[string]any{"repo": "hello", "number": 7},
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("missing owner err = %v, want domain.ErrInvalid", err)
	}

	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0", n)
	}
}

func TestGitHubNetworkErrorIsRetryableAndRedacted(t *testing.T) {
	fake := newFakeGitHub(t)
	provider := fake.provider()
	fake.server.Close() // force a transport failure

	_, err := provider.Call(context.Background(), mergeSpec())
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error %T is not *UpstreamError", err)
	}
	if ue.Class != ClassNetwork {
		t.Errorf("class = %q, want network", ue.Class)
	}
	if !Retryable(err) {
		t.Error("network failures must be retryable")
	}
	assertNoCredential(t, err)
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	gh := NewGitHub(nil, "")

	if err := reg.Register(domain.ConnectorSourceGitHub, gh); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := reg.Register(domain.ConnectorSourceGitHub, gh); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate register err = %v, want domain.ErrAlreadyExists", err)
	}
	if err := reg.Register("bogus", gh); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad kind err = %v, want domain.ErrInvalid", err)
	}
	if err := reg.Register(domain.ConnectorSourceSlack, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("nil provider err = %v, want domain.ErrInvalid", err)
	}

	got, err := reg.Get(domain.ConnectorSourceGitHub)
	if err != nil || got != Provider(gh) {
		t.Errorf("get = %v, %v", got, err)
	}
	if _, err := reg.Get(domain.ConnectorSourceSlack); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing provider err = %v, want domain.ErrNotFound", err)
	}
}

func TestSanitizeUpstreamMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			// The literal credential is replaced first, then the
			// token-shape pattern consumes the "token <value>" echo too.
			name: "literal credential removed",
			msg:  "bad token " + testToken + " rejected",
			want: "bad [redacted] rejected",
		},
		{
			name: "bearer echo removed without knowing the token",
			msg:  "header Bearer some-opaque-value rejected",
			want: "header [redacted] rejected",
		},
		{
			name: "github pat shape removed",
			msg:  "github_pat_11AAAA0000bbbbCCCC leaked",
			want: "[redacted] leaked",
		},
		{
			name: "control characters collapse",
			msg:  "line1\nline2",
			want: "line1 line2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeUpstreamMessage(tt.msg, testToken)
			if got != tt.want {
				t.Errorf("sanitize(%q) = %q, want %q", tt.msg, got, tt.want)
			}
			if strings.Contains(got, testToken) {
				t.Errorf("credential survived sanitization: %q", got)
			}
		})
	}

	long := strings.Repeat("x", 500)
	if got := sanitizeUpstreamMessage(long, ""); len(got) > maxSanitizedLen+3 {
		t.Errorf("sanitized length %d exceeds cap", len(got))
	}
}
