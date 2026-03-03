package agenterr

import (
	"regexp"
	"strconv"
	"time"
)

// OpenCode-specific error patterns. OpenCode supports multiple providers,
// so patterns are broader.
var openCodePatterns = []struct {
	re    *regexp.Regexp
	class ErrorClass
	msg   string
}{
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
}

var openCodeRetryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

func classifyOpenCode(logTail string, exitCode int) *classifyResult {
	if logTail == "" {
		return nil
	}

	for _, p := range openCodePatterns {
		if p.re.MatchString(logTail) {
			r := &classifyResult{
				Class:   p.class,
				Message: p.msg,
			}
			if p.class == RateLimited {
				if m := openCodeRetryAfterRe.FindStringSubmatch(logTail); len(m) > 1 {
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
