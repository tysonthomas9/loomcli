// Package gemini holds the classifier patterns for Google's Gemini CLI
// (@google/gemini-cli).
//
// Patterns are seeded conservatively from observed Gemini API error
// surfaces ("RESOURCE_EXHAUSTED", quota / rate-limit phrasings) and the
// generic prompt shapes the Ink-based TUI uses for tool approval.
// They should be tightened once a recorded corpus is in place.
package gemini

import (
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/detector"
)

// apiErrorRE matches Gemini CLI's square-bracket-wrapped format from
// errorParsing.ts (packages/core/src/utils/errorParsing.ts on
// google-gemini/gemini-cli). Examples:
//
//   - "[API Error: Quota exceeded ... (Status: 429)]"  — JSON-form with code
//   - "[API Error: Rate limit reached. Please wait and try again later.]"
//   - "[API Error: An unknown error occurred.]"
//
// Group 1 → message, group 2 (optional) → 3-digit status code. The
// regex is greedy on the leading `.*?` only up to a possible
// "(Status: NNN)" suffix so the message captures the prose without
// the trailing code.
var apiErrorRE = regexp.MustCompile(`\[API Error:\s*(.*?)(?:\s*\(Status:\s*(\d{3})\))?\]`)

// MatchAPIError implements detector.APIErrorMatcher for Gemini CLI.
// When the rendered status is absent but the message contains the
// canonical 429-rate-limit phrase, Code is inferred as 429.
func MatchAPIError(stripped string) (detector.APIErrorHit, bool) {
	m := apiErrorRE.FindStringSubmatch(stripped)
	if m == nil {
		return detector.APIErrorHit{}, false
	}
	hit := detector.APIErrorHit{
		Message: strings.TrimSpace(m[1]),
	}
	if m[2] != "" {
		code := 0
		for _, r := range m[2] {
			code = code*10 + int(r-'0')
		}
		hit.Code = code
	} else if strings.Contains(strings.ToLower(hit.Message), "please wait and try again later") {
		// Canonical Gemini rate-limit text; upstream omits a numeric
		// status when this message is appended.
		hit.Code = 429
	}
	hit.RetryAfter = detector.ParseRetryAfter(hit.Message)
	return hit, true
}

// Patterns is the Gemini harness fingerprint set consumed by the
// wrapper's harness adapter.
var Patterns = detector.Patterns{
	APIError: MatchAPIError,
	Cost: []string{
		"quota exceeded",
		"resource has been exhausted",
		"resource_exhausted",
		"rate limit",
		"rate-limit",
		"rate limit exceeded",
		"usage limit",
		"you have exceeded",
		"free tier",
	},
	Retry: []string{
		"please try again",
		"transient error",
		"temporary failure",
		"network error",
		"upstream error",
		"deadline exceeded",
		"unavailable",
	},
	Prompt: []string{
		"(y/n)",
		"(y/n/a)",
		"(yes/no)",
		"continue?",
		"apply this change?",
		"do you want to continue?",
		"allow?",
	},
}
