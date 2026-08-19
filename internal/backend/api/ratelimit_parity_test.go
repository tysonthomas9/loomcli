package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// D-50/D-19, the serve-mediated client's half. Its old 429 arm produced
// ErrUnavailable with a "rate limited: " message prefix — a class the web UI
// renders as 503, and a convention no caller could branch on. The class now
// comes from the shared table, so the two clients cannot drift again.
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
// This client classifies 2xx-with-success=false from the body string, so the
// early status check is what keeps a wordy 429 from taking that path.
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
