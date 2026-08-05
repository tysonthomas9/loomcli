package fleetdb

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// D-50/D-19, the Store client's half. A 429 has no case in the status switch,
// so it used to fall into the "any other 4xx" catch-all and arrive as
// domain.ErrConflict — indistinguishable from losing a claim race, and
// presented by the web UI as a client conflict on a routine throttle.
func TestClassifyHTTPError_RateLimited(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "30")

	err := classifyHTTPError("GET", "/issues", http.StatusTooManyRequests,
		[]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`), h)

	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if errors.Is(err, domain.ErrConflict) {
		t.Fatalf("429 still reads as a conflict: %v", err)
	}
	after, ok := domain.RateLimitRetryAfter(err)
	if !ok || after != 30*time.Second {
		t.Fatalf("RateLimitRetryAfter = (%v, %v), want (30s, true)", after, ok)
	}
	// The transport detail must survive for logs.
	if got := err.Error(); !strings.Contains(got, "/issues") {
		t.Errorf("error %q should name the request", got)
	}
}

func TestClassifyHTTPError_RateLimitedWithoutHint(t *testing.T) {
	err := classifyHTTPError("GET", "/issues", http.StatusTooManyRequests, nil, http.Header{})
	after, ok := domain.RateLimitRetryAfter(err)
	if !ok {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
	if after != 0 {
		t.Fatalf("RetryAfter = %v, want 0 with no header", after)
	}
}

// Other 4xx statuses keep the catch-all they always had: this change carves
// 429 out of it, it does not reshape the rest.
func TestClassifyHTTPError_OtherFourXXStillConflict(t *testing.T) {
	err := classifyHTTPError("GET", "/issues", http.StatusTeapot, nil, http.Header{})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want the 4xx catch-all ErrConflict", err)
	}
	if errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("a non-429 must not read as rate limited: %v", err)
	}
}
