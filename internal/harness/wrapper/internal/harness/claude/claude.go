// Package claude holds the classifier patterns for the Claude Code CLI
// harness. Patterns are intentionally conservative: false positives
// here turn an active run into a stuck-looking one.
package claude

import (
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/internal/detector"
)

// apiErrorRE matches Claude Code's two API-error rendering shapes:
//
//   - HTTP errors with a 3-digit code: "API Error: 529 Overloaded..."
//   - Transport errors without a code: "API Error: The socket
//     connection was closed unexpectedly..."
//
// Both may appear with an optional leading tree-character decoration
// like "⎿  " (claudecode's tool-result drawing). The line-start anchor
// (with allowed leading whitespace + one optional decoration glyph)
// keeps the matcher from firing on in-prose mentions like "what does
// API Error: 500 mean?".
var apiErrorRE = regexp.MustCompile(`(?im)^[^\S\r\n]*(?:[⎿│├└╰─◯⏺]\s*)?API Error:\s*(?:(\d{3})\b\s+)?(.*)$`)

// MatchAPIError implements detector.APIErrorMatcher for Claude Code.
// On match, returns the parsed HTTP code (zero for the transport-error
// variant) and the trailing message with any whitespace trimmed.
//
// If the line starts with digits that aren't a valid 3-digit HTTP code
// (e.g. "API Error: 9999 unrecognized"), the match is rejected — that
// shape is almost certainly noise rather than a real upstream error.
func MatchAPIError(stripped string) (detector.APIErrorHit, bool) {
	m := apiErrorRE.FindStringSubmatch(stripped)
	if m == nil {
		return detector.APIErrorHit{}, false
	}
	hit := detector.APIErrorHit{
		Message: strings.TrimSpace(m[2]),
	}
	if m[1] != "" {
		// regex \d{3} guarantees Atoi succeeds.
		code := 0
		for _, r := range m[1] {
			code = code*10 + int(r-'0')
		}
		hit.Code = code
	} else if len(hit.Message) > 0 && hit.Message[0] >= '0' && hit.Message[0] <= '9' {
		// Looks like a malformed numeric code, not a real transport
		// error. Reject rather than misclassify.
		return detector.APIErrorHit{}, false
	}
	hit.RetryAfter = detector.ParseRetryAfter(hit.Message)
	return hit, true
}

// Patterns is the Claude harness fingerprint set consumed by the
// wrapper's harness adapter. Matching happens on stripped, lower-cased
// recent output (Cost/Retry) and on the trailing line of stripped
// output (Prompt). APIError is a regex matcher for "API Error: ..."
// lines; see MatchAPIError.
var Patterns = detector.Patterns{
	APIError: MatchAPIError,
	Cost: []string{
		"you've hit your limit",
		"you have hit your limit",
		"limit resets",
		"resets at",
		"usage limit",
		"rate limit",
		"rate-limit",
		"quota exceeded",
	},
	Retry: []string{
		"please try again",
		"transient error",
		"temporary failure",
		"network error",
		"upstream error",
	},
	Prompt: []string{
		"(y/n)",
		"(y/n/a)",
		"(yes/no)",
		"continue?",
		"continue? [y/n]",
		"approve?",
		"do you want to continue?",
	},
}
