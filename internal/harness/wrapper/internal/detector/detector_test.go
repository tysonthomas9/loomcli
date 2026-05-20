package detector

import (
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"H1: numeric seconds spelled out", "Retry after 30 seconds.", 30 * time.Second},
		{"H2: numeric minutes spelled out", "Try again in 2 minutes.", 2 * time.Minute},
		{"H3: non-numeric phrasing", "try again in a moment", 0},
		{"H4: empty string", "", 0},
		{"H5: compact unit", "retry after 5s", 5 * time.Second},
		{"H6: no numeric hint", "please try again later", 0},
		{"hours unit", "try again in 1 hour", time.Hour},
		{"mid-message minutes", "the upstream said try again in 10 minutes please", 10 * time.Minute},
		{"zero rejected", "try again in 0 seconds", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseRetryAfter(tc.in)
			if got != tc.want {
				t.Errorf("ParseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
