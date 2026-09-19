package fleetdb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// The Store client's 429 arm. A 429 had no case in the status switch, so it
// fell into the "any other 4xx" catch-all and arrived as domain.ErrConflict —
// indistinguishable from losing a claim race, and presented by the web UI as a
// client conflict on a routine throttle.
func TestClassifyHTTPError_RateLimited(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("Retry-After", "30")

	err := classifyHTTPError(http.MethodGet, "/issues", http.StatusTooManyRequests,
		[]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`), h)

	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want errors.Is ErrRateLimited", err)
	}
	if errors.Is(err, domain.ErrConflict) {
		t.Fatalf("429 still reads as a conflict: %v", err)
	}
	after, ok := domain.RateLimitRetryAfter(err)
	if !ok || after != "30" {
		t.Fatalf("RateLimitRetryAfter = (%q, %v), want (\"30\", true)", after, ok)
	}
	// The transport detail must survive for logs.
	if got := err.Error(); !strings.Contains(got, "/issues") {
		t.Errorf("error %q should name the request", got)
	}
}

// An HTTP-date Retry-After reaches the caller unchanged: the Store does not
// parse it, so the one consumer that can (an HTTP client) still sees both
// forms RFC 9110 allows.
func TestClassifyHTTPError_RateLimitedHTTPDateKeptVerbatim(t *testing.T) {
	t.Parallel()
	const date = "Wed, 21 Oct 2026 07:28:00 GMT"
	h := http.Header{}
	h.Set("Retry-After", date)

	err := classifyHTTPError(http.MethodGet, "/agents", http.StatusTooManyRequests, nil, h)
	after, ok := domain.RateLimitRetryAfter(err)
	if !ok {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
	if after != date {
		t.Fatalf("RetryAfter = %q, want %q", after, date)
	}
}

func TestClassifyHTTPError_RateLimitedWithoutHint(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(http.MethodGet, "/issues", http.StatusTooManyRequests, nil, http.Header{})
	after, ok := domain.RateLimitRetryAfter(err)
	if !ok {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
	if after != "" {
		t.Fatalf("RetryAfter = %q, want empty with no header", after)
	}
}

// A nil header is the zero value at several call sites; reading it must not
// panic, and must read as "the server said nothing".
func TestClassifyHTTPError_RateLimitedNilHeader(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(http.MethodGet, "/issues", http.StatusTooManyRequests, nil, nil)
	if after, ok := domain.RateLimitRetryAfter(err); !ok || after != "" {
		t.Fatalf("RateLimitRetryAfter = (%q, %v), want (\"\", true)", after, ok)
	}
}

// Other 4xx statuses keep the catch-all they always had: this change carves
// 429 out of it, it does not reshape the rest.
func TestClassifyHTTPError_OtherFourXXStillConflict(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(http.MethodGet, "/issues", http.StatusTeapot, nil, http.Header{})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want the 4xx catch-all ErrConflict", err)
	}
	if errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("a non-429 must not read as rate limited: %v", err)
	}
}

// End-to-end through the real HTTP path, so the response header actually has
// to be threaded from the transport into the classifier — the part a unit test
// of classifyHTTPError alone cannot catch. Agent CRUD is the caller that gets
// this wrong today: classifyStoreError sits directly downstream of it.
func TestAgentStore_RateLimitedResponseCarriesRetryAfter(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`))
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Agents().List(t.Context(), "WS")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want errors.Is ErrRateLimited", err)
	}
	if errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a throttled agent list still reads as a conflict: %v", err)
	}
	if after, ok := domain.RateLimitRetryAfter(err); !ok || after != "12" {
		t.Fatalf("RateLimitRetryAfter = (%q, %v), want (\"12\", true)", after, ok)
	}
}
