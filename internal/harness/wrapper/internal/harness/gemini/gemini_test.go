package gemini

import (
	"strings"
	"testing"
	"time"
)

func TestMatchAPIError(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantOK      bool
		wantCode    int
		wantRetry   time.Duration
		msgContains string
	}{
		{
			name:        "Ge1: explicit (Status: 429)",
			in:          "[API Error: Quota exceeded for quota metric 'Generate Content API requests per minute' (Status: 429)]",
			wantOK:      true,
			wantCode:    429,
			msgContains: "Quota exceeded",
		},
		{
			name:        "Ge2: 5xx form",
			in:          "[API Error: Internal error occurred. (Status: 500)]",
			wantOK:      true,
			wantCode:    500,
			msgContains: "Internal error",
		},
		{
			name:        "Ge3: fallback Code=429 from canonical 429 phrase",
			in:          "[API Error: Rate limit reached. Please wait and try again later.]",
			wantOK:      true,
			wantCode:    429,
			msgContains: "Rate limit reached",
		},
		{
			name:        "Ge4: bracket wrapper with no parseable code",
			in:          "[API Error: An unknown error occurred.]",
			wantOK:      true,
			wantCode:    0,
			msgContains: "unknown error",
		},
		{
			name: "Ge5: mid-prose bracket mention — documented trade-off " +
				"(matches with Code=0 because there is no (Status: NNN) suffix; consumers ignore Code=0 hits)",
			in:          "User said [API Error: 500 in their docs]",
			wantOK:      true,
			wantCode:    0,
			msgContains: "500 in their docs",
		},
		{
			name:   "Ge6: no bracket form anywhere",
			in:     "regular output, nothing API-like",
			wantOK: false,
		},
		{
			name:      "Ge7: RetryAfter parsed without explicit (Status:)",
			in:        "[API Error: timeout. Retry after 5 seconds.]",
			wantOK:    true,
			wantCode:  0,
			wantRetry: 5 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, ok := MatchAPIError(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (hit=%+v)", ok, tc.wantOK, hit)
			}
			if !ok {
				return
			}
			if hit.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", hit.Code, tc.wantCode)
			}
			if hit.RetryAfter != tc.wantRetry {
				t.Errorf("RetryAfter = %v, want %v", hit.RetryAfter, tc.wantRetry)
			}
			if tc.msgContains != "" && !strings.Contains(hit.Message, tc.msgContains) {
				t.Errorf("Message = %q, want substring %q", hit.Message, tc.msgContains)
			}
		})
	}
}
