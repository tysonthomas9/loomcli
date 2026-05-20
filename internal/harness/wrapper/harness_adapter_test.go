package wrapper

import (
	"strings"
	"testing"
	"time"

	claudeharness "github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/harness/claude"
	codexharness "github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/harness/codex"
	geminiharness "github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/harness/gemini"
)

// TestHarnessAdapter_Classify covers the classifier-level matrix from
// the plan: fast api_error detection (regardless of idle/quiet state),
// structured field propagation, and regression coverage for existing
// Cost/Retry/Prompt paths.
func TestHarnessAdapter_Classify(t *testing.T) {
	claude := harnessAdapter{patterns: claudeharness.Patterns}
	gemini := harnessAdapter{patterns: geminiharness.Patterns}
	codex := harnessAdapter{patterns: codexharness.Patterns}

	cases := []struct {
		name       string
		adapter    harnessAdapter
		input      ClassifierInput
		wantStatus Status
		wantCode   int
		wantRetry  time.Duration
		reasonHas  string
	}{
		{
			name:       "A1: claude api_error 529 fires without idle gate",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "API Error: 529 Overloaded."},
			wantStatus: StatusAPIError,
			wantCode:   529,
		},
		{
			name:       "A2: claude api_error 429 carries RetryAfter",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "API Error: 429 Too Many Requests. Retry after 30 seconds."},
			wantStatus: StatusAPIError,
			wantCode:   429,
			wantRetry:  30 * time.Second,
		},
		{
			name:       "A2b: claude transport-error variant with tree-character prefix",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "  ⎿  API Error: The socket connection was closed unexpectedly."},
			wantStatus: StatusAPIError,
			wantCode:   0,
			reasonHas:  "socket connection was closed",
		},
		{
			name:       "A3: gemini bracket form with (Status: 429)",
			adapter:    gemini,
			input:      ClassifierInput{RecentOutput: "[API Error: rate limit (Status: 429)]"},
			wantStatus: StatusAPIError,
			wantCode:   429,
		},
		{
			name:       "A4: codex exceeded retry limit with explicit 503",
			adapter:    codex,
			input:      ClassifierInput{RecentOutput: "■ exceeded retry limit, last status: 503"},
			wantStatus: StatusAPIError,
			wantCode:   503,
		},
		{
			name:       "A5: regression — cost path on idle",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "you've hit your limit", Idle: true},
			wantStatus: StatusBlockedByCost,
		},
		{
			name:       "A6: regression — retry path on idle",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "please try again", Idle: true},
			wantStatus: StatusRetryLater,
		},
		{
			name:       "A7: regression — prompt detection on quiet trailing line",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "Some text\nContinue? [y/N]", Quiet: true},
			wantStatus: StatusWaitingForInput,
		},
		{
			name:       "A8: api_error wins over cost when both present",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "you've hit your limit\nAPI Error: 529 Overloaded.", Idle: true},
			wantStatus: StatusAPIError,
			wantCode:   529,
		},
		{
			name:       "A9: false-positive guard — mid-line API Error in prose",
			adapter:    claude,
			input:      ClassifierInput{RecentOutput: "chitchat about API Error: 500 mid-line", Idle: true},
			wantStatus: "",
		},
		{
			name:       "A10: gemini benign output stays unclassified",
			adapter:    gemini,
			input:      ClassifierInput{RecentOutput: "regular tool output without brackets", Idle: true},
			wantStatus: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.adapter.Classify(tc.input)
			if got.Status != tc.wantStatus {
				t.Fatalf("Status = %q, want %q (got %+v)", got.Status, tc.wantStatus, got)
			}
			if tc.wantStatus == "" {
				return
			}
			if got.HTTPCode != tc.wantCode {
				t.Errorf("HTTPCode = %d, want %d", got.HTTPCode, tc.wantCode)
			}
			if got.RetryAfter != tc.wantRetry {
				t.Errorf("RetryAfter = %v, want %v", got.RetryAfter, tc.wantRetry)
			}
			// Only api_error classifications are non-terminal at this
			// matcher entry point. Cost/Retry are terminal; prompts
			// are non-terminal but go through a different code path.
			wantTerminal := tc.wantStatus == StatusBlockedByCost || tc.wantStatus == StatusRetryLater
			if got.Terminal != wantTerminal {
				t.Errorf("Terminal = %v, want %v", got.Terminal, wantTerminal)
			}
			if tc.reasonHas != "" && !strings.Contains(got.Reason, tc.reasonHas) {
				t.Errorf("Reason = %q, want substring %q", got.Reason, tc.reasonHas)
			}
		})
	}
}
