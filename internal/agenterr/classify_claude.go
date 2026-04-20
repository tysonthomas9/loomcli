package agenterr

import "regexp"

// Claude-specific error patterns, ordered most-specific first.
var claudePatterns = []errorPattern{
	{regexp.MustCompile(`(?i)rate.?limit|too many requests`), RateLimited, "rate limit exceeded"},
	{regexp.MustCompile(`(?i)\b429\b`), RateLimited, "rate limit exceeded (429)"},
	{regexp.MustCompile(`(?i)overloaded_error`), RateLimited, "API overloaded (529)"},
	{regexp.MustCompile(`(?i)invalid.?api.?key|authentication.?failed`), AuthFailure, "invalid API key"},
	{regexp.MustCompile(`(?i)\b401\b|unauthorized`), AuthFailure, "authentication failed (401)"},
	{regexp.MustCompile(`(?i)ANTHROPIC_API_KEY`), AuthFailure, "ANTHROPIC_API_KEY not set or invalid"},
	{regexp.MustCompile(`(?i)\b402\b|payment.?required|insufficient.?credits|quota.?exceeded`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)billing`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)model.?not.?found|model.*does not exist|invalid.?model|issue with the selected model|selected model.*may not exist|selected model.*may not have access to it`), ModelNotFound, "model not found"},
	{regexp.MustCompile(`(?i)\b404\b.*model`), ModelNotFound, "model not found (404)"},
	{regexp.MustCompile(`(?i)context.?length.?exceeded|max.?tokens|token.?limit|context.?window`), ContextOverflow, "context length exceeded"},
	{regexp.MustCompile(`(?i)timeout|ETIMEDOUT|ECONNRESET|connection.?timed?.?out`), Timeout, "connection timeout"},
	{regexp.MustCompile(`(?i)\b529\b|internal.?server.?error|service.?unavailable`), Transient, "server error"},
	{regexp.MustCompile(`(?i)\b50[023]\b`), Transient, "server error"},
}

func classifyClaude(logTail string) *classifyResult {
	return classifyWithPatterns(logTail, claudePatterns)
}
