package agenterr

import (
	"regexp"
	"strconv"
	"time"
)

// Gemini-specific error patterns, ordered most-specific first.
var geminiPatterns = []struct {
	re    *regexp.Regexp
	class ErrorClass
	msg   string
}{
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
}

var geminiRetryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

func classifyGemini(logTail string) *classifyResult {
	if logTail == "" {
		return nil
	}

	for _, p := range geminiPatterns {
		if p.re.MatchString(logTail) {
			r := &classifyResult{
				Class:   p.class,
				Message: p.msg,
			}
			if p.class == RateLimited {
				if m := geminiRetryAfterRe.FindStringSubmatch(logTail); len(m) > 1 {
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
