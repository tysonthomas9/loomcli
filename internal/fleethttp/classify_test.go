package fleethttp

import (
	"net/http"
	"testing"
	"time"
)

// The table is the single source both clients read; these pin the entries a
// caller's behavior depends on. 429 is the one this work exists for.
func TestClassifyStatus(t *testing.T) {
	for status, want := range map[int]Class{
		http.StatusTooManyRequests:     ClassRateLimited,
		http.StatusConflict:            ClassConflict,
		http.StatusNotFound:            ClassNotFound,
		http.StatusBadRequest:          ClassValidation,
		http.StatusUnprocessableEntity: ClassValidation,
		http.StatusGone:                ClassGone,
		http.StatusServiceUnavailable:  ClassUnavailable,
		http.StatusGatewayTimeout:      ClassTimeout,
		http.StatusInternalServerError: ClassInternal,
	} {
		if got := ClassifyStatus(status); got != want {
			t.Errorf("ClassifyStatus(%d) = %s, want %s", status, got, want)
		}
	}

	// An unmapped status must stay the caller's business rather than being
	// silently folded into some neighboring class.
	if got := ClassifyStatus(http.StatusTeapot); got != ClassUnknown {
		t.Errorf("ClassifyStatus(418) = %s, want unknown", got)
	}
}

// A rate limit and a conflict must never collapse into each other: that
// collapse is the defect (a 429 read as "someone else won the race").
func TestClassifyStatus_RateLimitIsNotConflict(t *testing.T) {
	if ClassifyStatus(http.StatusTooManyRequests) == ClassifyStatus(http.StatusConflict) {
		t.Fatal("429 and 409 classify the same — the conflict/backpressure conflation is back")
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"absent", "", 0},
		{"delay seconds", "30", 30 * time.Second},
		{"whitespace tolerated", "  45 ", 45 * time.Second},
		{"zero means no hint", "0", 0},
		{"negative is not a hint", "-5", 0},
		{"garbage", "soon", 0},
		{"http-date in the future", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		// A date already past means "retry now", which the caller's own
		// backoff owns — not a negative duration that would read as a hint.
		{"http-date in the past", now.Add(-time.Hour).Format(http.TimeFormat), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.value != "" {
				h.Set("Retry-After", tc.value)
			}
			if got := RetryAfter(h, now); got != tc.want {
				t.Fatalf("RetryAfter(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
