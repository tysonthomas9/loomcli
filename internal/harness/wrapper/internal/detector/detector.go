// Package detector provides generic pattern primitives for harness
// classifiers. Patterns are matched against the recent harness output
// (typically the last ~64KB), with ANSI escapes already stripped.
//
// The package is intentionally minimal: it does not own state, it just
// runs string matches. Real classifiers compose these primitives with
// state from the wrapper (idle thresholds, quiet windows).
package detector

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Patterns groups the per-harness fingerprints a classifier consults.
// All slices are matched as case-insensitive substrings against
// already-stripped, lower-cased recent output, except Prompt which is
// matched against the trailing line of the original (case-preserved)
// stripped output.
type Patterns struct {
	// Cost matches messages indicating the harness has hit a budget,
	// quota, or rate limit and cannot proceed without operator
	// intervention.
	Cost []string

	// Retry matches messages indicating a transient failure that the
	// engine should retry after a backoff.
	Retry []string

	// Prompt matches trailing-line fingerprints (e.g. "(y/N)") that
	// indicate the harness is waiting for keyboard input.
	Prompt []string

	// APIError parses high-confidence upstream API error markers out of
	// the harness's stripped output. Each harness formats API errors
	// differently (Claude: "API Error: <code> ...", Gemini: "[API
	// Error: ... (Status: <code>)]", Codex: phrase-based), so the
	// matcher is supplied per-harness rather than a single shared
	// regex. nil means "no API-error detection for this harness".
	APIError APIErrorMatcher
}

// APIErrorHit is what an APIErrorMatcher returns when it recognizes an
// upstream API error in the harness's output.
type APIErrorHit struct {
	// Code is the HTTP status code parsed from the message. Zero when
	// the harness's output did not include a numeric code (e.g.
	// transport-layer failures like "socket connection closed
	// unexpectedly") or when the matcher recognized a phrase whose
	// code is implicit.
	Code int

	// Message is the human-readable detail extracted from the matched
	// line, used to populate the wrapper event's Reason.
	Message string

	// RetryAfter is the wait duration the harness suggested in the
	// error text (e.g. "Retry after 30 seconds"). Zero when the
	// message contained no parseable hint.
	RetryAfter time.Duration
}

// APIErrorMatcher inspects already-ANSI-stripped recent output and
// reports whether it contains a recognized upstream API error.
type APIErrorMatcher func(stripped string) (APIErrorHit, bool)

// retryAfterRE captures "in N seconds", "in N minutes", "after N
// seconds", "after Ns", etc. The unit group is optional; a bare number
// is not matched because the surrounding text ("retry after 5") is
// ambiguous without a unit, and matchers should prefer no hint over a
// guessed one.
var retryAfterRE = regexp.MustCompile(`(?i)(?:try\s+again|retry)[^.\n]*?(?:in|after)\s+(\d+)\s*(s\b|sec|second|m\b|min|minute|h\b|hr|hour)s?`)

// ParseRetryAfter scans an API-error message for a numeric retry hint
// and returns it as a time.Duration. Returns zero when no hint was
// found or when the unit could not be recognized.
//
// Recognized phrasings include "try again in 30 seconds", "retry after
// 2 minutes", "try again in 5s". Non-numeric phrasings like "try again
// in a moment" return zero — better to surface "no hint" than guess.
func ParseRetryAfter(msg string) time.Duration {
	m := retryAfterRE.FindStringSubmatch(msg)
	if len(m) < 3 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "s", "sec", "second":
		return time.Duration(n) * time.Second
	case "m", "min", "minute":
		return time.Duration(n) * time.Minute
	case "h", "hr", "hour":
		return time.Duration(n) * time.Hour
	}
	return 0
}

// MatchAny returns the first pattern in patterns that appears as a
// substring of haystack, or "" if none match. Caller is expected to
// pre-lowercase haystack.
func MatchAny(haystack string, patterns []string) string {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.Contains(haystack, p) {
			return p
		}
	}
	return ""
}

// MatchPromptSuffix returns the first pattern in patterns that the
// trailing non-empty line of haystack ends with (case-insensitive),
// or "" if none match. Trailing whitespace on the last line is
// ignored so prompts ending with a space ("Continue? ") still match.
func MatchPromptSuffix(haystack string, patterns []string) string {
	tail := lastNonEmptyLine(haystack)
	if tail == "" {
		return ""
	}
	tailLower := strings.ToLower(strings.TrimRight(tail, " \t"))
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.HasSuffix(tailLower, strings.ToLower(p)) {
			return p
		}
	}
	return ""
}

func lastNonEmptyLine(s string) string {
	end := len(s)
	for end > 0 {
		// Trim a trailing newline / space block.
		for end > 0 && (s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == ' ' || s[end-1] == '\t') {
			end--
		}
		start := end
		for start > 0 && s[start-1] != '\n' {
			start--
		}
		line := s[start:end]
		if line != "" {
			return line
		}
		if start == 0 {
			return ""
		}
		end = start - 1
	}
	return ""
}
