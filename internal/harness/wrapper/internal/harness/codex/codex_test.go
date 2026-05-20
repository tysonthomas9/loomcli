package codex

import (
	"strings"
	"testing"
)

func TestMatchAPIError(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantOK      bool
		wantCode    int
		msgContains string
	}{
		{
			name:        "Cx1: explicit retry-limit with 429",
			in:          "■ exceeded retry limit, last status: 429 Too Many Requests",
			wantOK:      true,
			wantCode:    429,
			msgContains: "retry limit",
		},
		{
			name:        "Cx2: capacity phrase → 503",
			in:          "ERROR: Selected model is at capacity. Please try a different model.",
			wantOK:      true,
			wantCode:    503,
			msgContains: "capacity",
		},
		{
			name:        "Cx3: high-demand phrase → 500",
			in:          "■ We're currently experiencing high demand, which may cause temporary errors.",
			wantOK:      true,
			wantCode:    500,
			msgContains: "high demand",
		},
		{
			name:        "Cx4: usage-limit phrase → 429",
			in:          "■ Usage limit reached. Try again at 14:00 UTC.",
			wantOK:      true,
			wantCode:    429,
			msgContains: "Usage limit",
		},
		{
			name:        "Cx5: quota phrase → 429",
			in:          "■ Quota exceeded. Check your plan and billing details.",
			wantOK:      true,
			wantCode:    429,
			msgContains: "Quota",
		},
		{
			name:        "Cx6: stream disconnected → code 0",
			in:          "■ stream disconnected before completion: connection reset",
			wantOK:      true,
			wantCode:    0,
			msgContains: "stream disconnected",
		},
		{
			name:   "Cx7: benign tool output",
			in:     "■ regular tool output",
			wantOK: false,
		},
		{
			name:   "Cx8: ERROR prefix alone is not a signal",
			in:     "ERROR: filesystem permission denied",
			wantOK: false,
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
			if tc.msgContains != "" && !strings.Contains(strings.ToLower(hit.Message), strings.ToLower(tc.msgContains)) {
				t.Errorf("Message = %q, want substring %q", hit.Message, tc.msgContains)
			}
		})
	}
}
