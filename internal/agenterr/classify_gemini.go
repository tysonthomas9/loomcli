package agenterr

import "regexp"

// Gemini-specific error patterns, ordered most-specific first. The
// shared prose-style rate-limit patterns are appended at the end so
// gemini-specific signals (429, resource_exhausted) win when present.
var geminiPatterns = append([]errorPattern{
	{regexp.MustCompile(`(?i)rate.?limit|too many requests|resource_exhausted`), RateLimited, "rate limit exceeded"},
	{regexp.MustCompile(`(?i)\b429\b`), RateLimited, "rate limit exceeded (429)"},
	{regexp.MustCompile(`(?i)invalid.?api.?key|authentication.?failed`), AuthFailure, "invalid API key"},
	{regexp.MustCompile(`(?i)\b401\b|unauthorized|unauthenticated|permission.?denied`), AuthFailure, "authentication failed"},
	{regexp.MustCompile(`(?i)GEMINI_API_KEY|GOOGLE_API_KEY`), AuthFailure, "GEMINI_API_KEY or GOOGLE_API_KEY not set or invalid"},
	{regexp.MustCompile(`(?i)\b402\b|billing|payment|required|quota.?exceeded|insufficient.?credits`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)model.?not.?found|unsupported.?model|invalid.?model|unknown.?model`), ModelNotFound, "model not found"},
	{regexp.MustCompile(`(?i)\b404\b.*model`), ModelNotFound, "model not found (404)"},
	{regexp.MustCompile(`(?i)context.?length|token.?limit|max.?tokens|prompt.?too.?long`), ContextOverflow, "context length exceeded"},
	{regexp.MustCompile(`(?i)timeout|ETIMEDOUT|ECONNRESET|timed?.?out|deadline.?exceeded`), Timeout, "connection timeout"},
	{regexp.MustCompile(`(?i)internal.?error|service.?unavailable|backend.?error`), Transient, "server error"},
	{regexp.MustCompile(`(?i)\b50[023]\b`), Transient, "server error"},
}, sharedRateLimitPatterns...)

func classifyGemini(logTail string) *classifyResult {
	return classifyWithPatterns(logTail, geminiPatterns)
}
