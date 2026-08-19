package fleet

import (
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// D-50/D-19: a 429 must reach callers as a typed rate-limit error carrying the
// server's pacing hint — never as a conflict (this client's old 4xx catch-all)
// and never as unavailable-with-a-message-prefix (the other client's old arm),
// because the web UI renders one of those as a 5xx server fault.
func TestClassifyHTTPError_RateLimited(t *testing.T) {
	err := classifyHTTPError("Ready", http.StatusTooManyRequests, apiResponse{
		Error:      "too many requests",
		RetryAfter: 30 * time.Second,
	})

	if !backend.IsKind(err, backend.KindRateLimited) {
		t.Fatalf("err = %v, want KindRateLimited", err)
	}
	if backend.IsKind(err, backend.KindConflict) || backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("429 still classifies as conflict/unavailable: %v", err)
	}
	after, ok := backend.RateLimitRetryAfter(err)
	if !ok || after != 30*time.Second {
		t.Fatalf("RateLimitRetryAfter = (%v, %v), want (30s, true)", after, ok)
	}
}

// A 429 with no Retry-After is still a rate limit; the hint is simply absent,
// and the caller's own backoff owns that case.
func TestClassifyHTTPError_RateLimitedWithoutHint(t *testing.T) {
	err := classifyHTTPError("Ready", http.StatusTooManyRequests, apiResponse{})
	after, ok := backend.RateLimitRetryAfter(err)
	if !ok {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
	if after != 0 {
		t.Fatalf("RetryAfter = %v, want 0 when the server gave no hint", after)
	}
}

// The status is authoritative for 429: no body wording may reclassify it.
// (The generic string matcher runs before the status switch in this client,
// which is exactly how a body message could have hijacked the class.)
func TestClassifyHTTPError_RateLimitBeatsBodyWording(t *testing.T) {
	for _, msg := range []string{
		"validation failed: too many requests",
		"issue not found",
		"already claimed",
	} {
		err := classifyHTTPError("Ready", http.StatusTooManyRequests, apiResponse{Error: msg})
		if !backend.IsKind(err, backend.KindRateLimited) {
			t.Errorf("body %q reclassified a 429: %v", msg, err)
		}
	}
}

// Non-429 statuses keep their existing classification — this change is
// additive, not a re-mapping of the rest of the table.
func TestClassifyHTTPError_OtherStatusesUnchanged(t *testing.T) {
	for status, kind := range map[int]backend.ErrorKind{
		http.StatusNotFound:            backend.KindNotFound,
		http.StatusConflict:            backend.KindConflict,
		http.StatusBadRequest:          backend.KindValidation,
		http.StatusServiceUnavailable:  backend.KindUnavailable,
		http.StatusInternalServerError: backend.KindInternal,
	} {
		err := classifyHTTPError("Ready", status, apiResponse{Error: "boom"})
		if !backend.IsKind(err, kind) {
			t.Errorf("HTTP %d = %v, want kind %s", status, err, kind)
		}
	}
}
