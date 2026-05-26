package agenterr

import "regexp"

// OpenCode-specific error patterns. OpenCode supports multiple providers,
// so patterns are broader. The shared prose-style rate-limit patterns
// are appended at the end so backend-specific signals (429) win when
// present.
var openCodePatterns = append([]errorPattern{
	{regexp.MustCompile(`(?i)rate.?limit|too many requests`), RateLimited, "rate limit exceeded"},
	{regexp.MustCompile(`(?i)\b429\b`), RateLimited, "rate limit exceeded (429)"},
	{regexp.MustCompile(`(?i)\b401\b|unauthorized|invalid.*key|invalid.?api.?key`), AuthFailure, "authentication failed"},
	{regexp.MustCompile(`(?i)\b402\b|billing|payment|quota|credits`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)model.?not.?found|model.*not.*exist`), ModelNotFound, "model not found"},
	{regexp.MustCompile(`(?i)\b404\b.*model`), ModelNotFound, "model not found (404)"},
	{regexp.MustCompile(`(?i)context.?length|token.?limit|too.?long`), ContextOverflow, "context length exceeded"},
	{regexp.MustCompile(`(?i)timeout|ETIMEDOUT|ECONNRESET|timed?.?out`), Timeout, "connection timeout"},
	{regexp.MustCompile(`(?i)server.?error|internal.?error`), Transient, "server error"},
	{regexp.MustCompile(`(?i)\b50[023]\b`), Transient, "server error"},
}, sharedRateLimitPatterns...)

func classifyOpenCode(logTail string) *classifyResult {
	return classifyWithPatterns(logTail, openCodePatterns)
}
