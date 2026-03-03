package agenterr

import (
	"regexp"
	"strconv"
	"time"
)

// Codex (OpenAI) specific error patterns, ordered most-specific first.
var codexPatterns = []struct {
	re    *regexp.Regexp
	class ErrorClass
	msg   string
}{
	{regexp.MustCompile(`(?i)rate.?limit|too many requests|tokens per min`), RateLimited, "rate limit exceeded"},
	{regexp.MustCompile(`(?i)\b429\b`), RateLimited, "rate limit exceeded (429)"},
	{regexp.MustCompile(`(?i)invalid.?api.?key|incorrect.?api.?key`), AuthFailure, "invalid API key"},
	{regexp.MustCompile(`(?i)\b401\b|unauthorized`), AuthFailure, "authentication failed (401)"},
	{regexp.MustCompile(`(?i)OPENAI_API_KEY`), AuthFailure, "OPENAI_API_KEY not set or invalid"},
	{regexp.MustCompile(`(?i)\b402\b|insufficient_quota|exceeded.*quota`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)billing`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)model.*not found|does not exist|model_not_found`), ModelNotFound, "model not found"},
	{regexp.MustCompile(`(?i)invalid.*model`), ModelNotFound, "invalid model"},
	{regexp.MustCompile(`(?i)context_length_exceeded|maximum context length|max.*tokens`), ContextOverflow, "context length exceeded"},
	{regexp.MustCompile(`(?i)timeout|ETIMEDOUT|ECONNRESET|timed?.?out`), Timeout, "connection timeout"},
	{regexp.MustCompile(`(?i)server_error|internal.?error|overloaded`), Transient, "server error"},
	{regexp.MustCompile(`(?i)\b50[023]\b`), Transient, "server error"},
}

var codexRetryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

func classifyCodex(logTail string, exitCode int) *classifyResult {
	if logTail == "" {
		return nil
	}

	for _, p := range codexPatterns {
		if p.re.MatchString(logTail) {
			r := &classifyResult{
				Class:   p.class,
				Message: p.msg,
			}
			if p.class == RateLimited {
				if m := codexRetryAfterRe.FindStringSubmatch(logTail); len(m) > 1 {
					if secs, err := strconv.Atoi(m[1]); err == nil {
						r.RetryAfter = time.Duration(secs) * time.Second
					}
				}
			}
			return r
		}
	}

	return nil
}
