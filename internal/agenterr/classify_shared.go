package agenterr

import "regexp"

// sharedRateLimitPatterns matches prose-style rate-limit wording that
// the major agent backends emit when a session- or usage-level limit
// is hit. Each per-backend classifier appends these patterns at the
// end of its own slice so backend-specific matches (e.g. 429, codex's
// `tokens per min`) still win when present, with the prose patterns
// catching the cases where the provider gives no machine-readable
// signal.
//
// User-evidence strings these patterns must classify as RateLimited:
//   - "You've hit your session limit · resets 6:40pm (Europe/Warsaw)"
//   - "You've hit your usage limit. Upgrade to Pro or try again at May 21st, 2026 1:32 AM."
//   - {"type":"error","message":"You've hit your usage limit."}
//   - "Error: try again at 6:40pm"
//
// `try again at` is intentionally narrowed to require a following
// digit so phrases like "try again at the menu" don't false-positive.
// `\bquota\b` is deliberately omitted — `quota` is already shadowed by
// the BillingError patterns in every backend.
var sharedRateLimitPatterns = []errorPattern{
	{regexp.MustCompile(`(?i)session.?limit`), RateLimited, "session limit hit"},
	{regexp.MustCompile(`(?i)usage.?limit`), RateLimited, "usage limit hit"},
	{regexp.MustCompile(`(?i)resets at|resets \d{1,2}:\d{2}`), RateLimited, "rate-limited (timed reset)"},
	{regexp.MustCompile(`(?i)try again at\s+\d`), RateLimited, "rate-limited (try again at <time>)"},
}
