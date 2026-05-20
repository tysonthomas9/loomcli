package claude

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
			name:        "Cl1: golden 529 from user's transcript",
			in:          "API Error: 529 Overloaded. This is a server-side issue, usually temporary — try again in a moment.",
			wantOK:      true,
			wantCode:    529,
			msgContains: "Overloaded",
		},
		{
			name:      "Cl2: 429 with numeric retry-after",
			in:        "API Error: 429 Too Many Requests. Retry after 30 seconds.",
			wantOK:    true,
			wantCode:  429,
			wantRetry: 30 * time.Second,
		},
		{
			name:      "Cl3: 503 with minutes unit",
			in:        "API Error: 503 Service Unavailable. Try again in 2 minutes.",
			wantOK:    true,
			wantCode:  503,
			wantRetry: 2 * time.Minute,
		},
		{
			name:        "Cl4: 500 no retry hint",
			in:          "API Error: 500 Internal Server Error.",
			wantOK:      true,
			wantCode:    500,
			msgContains: "Internal Server Error",
		},
		{
			name:        "Cl5: 502 no trailing punctuation",
			in:          "API Error: 502 Bad Gateway",
			wantOK:      true,
			wantCode:    502,
			msgContains: "Bad Gateway",
		},
		{
			name:     "Cl6: leading whitespace tolerated",
			in:       "  API Error: 529 Overloaded",
			wantOK:   true,
			wantCode: 529,
		},
		{
			name:   "Cl7: mid-line user prompt rejected",
			in:     "What does API Error: 500 mean?",
			wantOK: false,
		},
		{
			name:     "Cl8: matches non-final line",
			in:       "previous output\nAPI Error: 529 Overloaded\nmore output",
			wantOK:   true,
			wantCode: 529,
		},
		{
			name:   "Cl9: 4-digit code rejected",
			in:     "API Error: 9999 unrecognized",
			wantOK: false,
		},
		{
			name:     "Cl10: lowercase variant",
			in:       "api error: 529 overloaded",
			wantOK:   true,
			wantCode: 529,
		},
		{
			name:   "Cl11: empty string",
			in:     "",
			wantOK: false,
		},
		{
			name:        "Cl12: 401 Unauthorized still matches",
			in:          "API Error: 401 Unauthorized.",
			wantOK:      true,
			wantCode:    401,
			msgContains: "Unauthorized",
		},
		{
			name:     "Cl13: repeated lines return first hit (not loop)",
			in:       "API Error: 529 Overloaded.\nAPI Error: 529 Overloaded.\nAPI Error: 529 Overloaded.",
			wantOK:   true,
			wantCode: 529,
		},
		{
			name:        "Cl14: transport error (no code) with tree-character prefix",
			in:          "  ⎿  API Error: The socket connection was closed unexpectedly. For more information, pass `verbose: true` in the second argument to fetch()",
			wantOK:      true,
			wantCode:    0,
			msgContains: "socket connection was closed unexpectedly",
		},
		{
			name:     "Cl15: tree-character prefix with code",
			in:       "  ⎿  API Error: 529 Overloaded.",
			wantOK:   true,
			wantCode: 529,
		},
		{
			name:        "Cl16: bare uncoded form",
			in:          "API Error: Connection refused.",
			wantOK:      true,
			wantCode:    0,
			msgContains: "Connection refused",
		},
		{
			name:      "Cl17: uncoded with retry hint",
			in:        "   API Error: Connection reset. Please retry in 30 seconds.",
			wantOK:    true,
			wantCode:  0,
			wantRetry: 30 * time.Second,
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
