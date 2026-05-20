package wrapper

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/detector"
)

// harnessAdapter turns a per-harness pattern set into a Classifier.
// Pattern matching always runs on stripped output so ANSI escapes do
// not interfere.
type harnessAdapter struct {
	patterns detector.Patterns
}

// Classify checks recent output against the harness's patterns.
//
// Order of checks:
//  1. APIError — fires regardless of idle/quiet state because high-
//     confidence anchored matchers don't need a quiescence gate. Sets
//     StatusAPIError (non-terminal: harness keeps running).
//  2. Cost / Retry — gated on Idle. Terminal: wrapper SIGTERMs harness.
//  3. Prompt — gated on Quiet. Non-terminal: harness stays at prompt.
func (h harnessAdapter) Classify(input ClassifierInput) Classification {
	stripped := stripANSIEscapes(input.RecentOutput)

	if h.patterns.APIError != nil {
		if hit, ok := h.patterns.APIError(stripped); ok {
			return Classification{
				Status:     StatusAPIError,
				Reason:     formatAPIErrorReason(hit),
				Terminal:   false,
				HTTPCode:   hit.Code,
				RetryAfter: hit.RetryAfter,
			}
		}
	}

	lower := strings.ToLower(stripped)

	if input.Idle {
		if hit := detector.MatchAny(lower, h.patterns.Cost); hit != "" {
			return Classification{
				Status:   StatusBlockedByCost,
				Reason:   hit,
				Terminal: true,
			}
		}
		if hit := detector.MatchAny(lower, h.patterns.Retry); hit != "" {
			return Classification{
				Status:   StatusRetryLater,
				Reason:   hit,
				Terminal: true,
			}
		}
	}

	if input.Quiet {
		if hit := detector.MatchPromptSuffix(stripped, h.patterns.Prompt); hit != "" {
			return Classification{
				Status:   StatusWaitingForInput,
				Reason:   "prompt detected: " + hit,
				Terminal: false,
			}
		}
	}

	return Classification{}
}

func formatAPIErrorReason(hit detector.APIErrorHit) string {
	if hit.Code == 0 {
		return "api error: " + hit.Message
	}
	return fmt.Sprintf("api error %d: %s", hit.Code, hit.Message)
}
