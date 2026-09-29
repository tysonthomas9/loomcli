package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRateLimitErrorUnwrapsToSentinel(t *testing.T) {
	t.Parallel()
	err := error(&RateLimitError{RetryAfter: "30", Detail: "fleetdb: GET /issues: HTTP 429"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want errors.Is ErrRateLimited", err)
	}
	// The whole point of a separate sentinel: it must never read as a
	// conflict, which is where the Store's 4xx catch-all used to put it.
	if errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v must not satisfy ErrConflict", err)
	}
	if got := err.Error(); !strings.Contains(got, "/issues") || !strings.Contains(got, "30") {
		t.Fatalf("Error() = %q, want the detail and the hint", got)
	}
}

func TestRateLimitErrorSurvivesWrapping(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("create agent: %w", &RateLimitError{RetryAfter: "5"})
	after, ok := RateLimitRetryAfter(wrapped)
	if !ok || after != "5" {
		t.Fatalf("RateLimitRetryAfter = (%q, %v), want (\"5\", true)", after, ok)
	}
}

func TestRateLimitRetryAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		err       error
		wantAfter string
		wantOK    bool
	}{
		{"typed with hint", &RateLimitError{RetryAfter: "30"}, "30", true},
		{"typed without hint", &RateLimitError{}, "", true},
		{"http-date verbatim", &RateLimitError{RetryAfter: "Wed, 21 Oct 2026 07:28:00 GMT"}, "Wed, 21 Oct 2026 07:28:00 GMT", true},
		// The class is known even when only the sentinel traveled.
		{"bare sentinel", ErrRateLimited, "", true},
		{"wrapped sentinel", fmt.Errorf("x: %w", ErrRateLimited), "", true},
		{"unrelated", ErrConflict, "", false},
		{"nil", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			after, ok := RateLimitRetryAfter(tc.err)
			if ok != tc.wantOK || after != tc.wantAfter {
				t.Fatalf("RateLimitRetryAfter = (%q, %v), want (%q, %v)", after, ok, tc.wantAfter, tc.wantOK)
			}
		})
	}
}

func TestRateLimitErrorEmptyIsStillReadable(t *testing.T) {
	t.Parallel()
	if got := (&RateLimitError{}).Error(); got != ErrRateLimited.Error() {
		t.Fatalf("Error() = %q, want %q", got, ErrRateLimited.Error())
	}
}
