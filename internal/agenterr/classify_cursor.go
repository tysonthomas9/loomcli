package agenterr

import (
	"regexp"
	"strconv"
	"time"
)

// Cursor-specific error patterns, ordered most-specific first.
var cursorPatterns = []struct {
	re    *regexp.Regexp
	class ErrorClass
	msg   string
}{
	{regexp.MustCompile(`(?i)rate.?limit|too many requests`), RateLimited, "rate limit exceeded"},
	{regexp.MustCompile(`(?i)\b429\b`), RateLimited, "rate limit exceeded (429)"},
	{regexp.MustCompile(`(?i)invalid.?api.?key|authentication.?failed`), AuthFailure, "invalid API key"},
	{regexp.MustCompile(`(?i)\b401\b|unauthorized|forbidden`), AuthFailure, "authentication failed"},
	{regexp.MustCompile(`(?i)CURSOR_API_KEY`), AuthFailure, "CURSOR_API_KEY not set or invalid"},
	{regexp.MustCompile(`(?i)\b402\b|billing|payment|required|quota|credits`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)model.?not.?found|unsupported.?model|invalid.?model|selected model.*may not exist|may not have access to it`), ModelNotFound, "model not found"},
	{regexp.MustCompile(`(?i)\b404\b.*model`), ModelNotFound, "model not found (404)"},
	{regexp.MustCompile(`(?i)context.?length|token.?limit|max.?tokens|prompt.?too.?long`), ContextOverflow, "context length exceeded"},
	{regexp.MustCompile(`(?i)timeout|ETIMEDOUT|ECONNRESET|timed?.?out`), Timeout, "connection timeout"},
	{regexp.MustCompile(`(?i)internal.?error|service.?unavailable|server.?error|overloaded`), Transient, "server error"},
	{regexp.MustCompile(`(?i)\b50[023]\b`), Transient, "server error"},
}

var cursorRetryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

func classifyCursor(logTail string) *classifyResult {
	if logTail == "" {
		return nil
	}

	for _, p := range cursorPatterns {
		if p.re.MatchString(logTail) {
			r := &classifyResult{
				Class:   p.class,
				Message: p.msg,
			}
			if p.class == RateLimited {
				if m := cursorRetryAfterRe.FindStringSubmatch(logTail); len(m) > 1 {
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
