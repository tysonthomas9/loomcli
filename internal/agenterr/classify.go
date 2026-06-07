package agenterr

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

// classifyResult is the internal result from a classification step.
type classifyResult struct {
	Class      ErrorClass
	Message    string
	RetryAfter time.Duration
}

// errorPattern defines a single regex→ErrorClass mapping. It now powers the
// single residual table (loom-specific distinctions the wrapper does not
// model) rather than five near-identical per-backend tables.
type errorPattern struct {
	re    *regexp.Regexp
	class ErrorClass
	msg   string
}

// retryAfterRe extracts a Retry-After header value from log/output text.
// The format is provider-independent.
var retryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

// BackendUnavailableMarker is the stable log marker the inner backend
// subprocess emits when the configured CLI is not on PATH. classifyFromText
// recognizes it before anything else so the supervisor gets a categorical
// BackendUnavailable instead of falling through to Unknown on the
// exec-not-found exit (fixes LOOM-4).
//
// Stable string contract: changing it requires updating any emitter
// (today: internal/cli/backends.binaryNotFoundInvocationError).
const BackendUnavailableMarker = "loom: backend binary not on PATH"

// backendUnavailableRe is the precompiled matcher used by classifyFromText.
var backendUnavailableRe = regexp.MustCompile(regexp.QuoteMeta(BackendUnavailableMarker))

// AgentLaunchFailedMarker is the stable marker emitted when the harness
// wrapper cannot launch the backend process at all — a PTY allocation/read
// failure, which surfaces the OS-level ENOEXEC "exec format error" when the
// backend binary is momentarily not a valid executable (e.g. mid self-update).
// classifyFromText recognizes it before any classifier so the supervisor
// records a retryable SpawnFailure carrying the real reason, instead of falling
// through to Unknown / "unclassified error (exit code 1)" — the generic message
// that previously hid this cause from the operator and UI.
//
// Stable string contract: changing it requires updating the emitter
// (internal/cli/backends.agentLaunchFailedInvocationError).
const AgentLaunchFailedMarker = "loom: agent process failed to launch"

// agentLaunchFailedRe is the precompiled matcher used by classifyFromText.
var agentLaunchFailedRe = regexp.MustCompile(regexp.QuoteMeta(AgentLaunchFailedMarker))

// rateLimitHintRe distinguishes a retryable throttle from a fatal
// budget/billing block within the wrapper's coarse blocked_by_cost status.
// Anything matching here is treated as RateLimited; everything else under
// blocked_by_cost defaults to the conservative fatal BillingError so a
// genuine budget exhaustion is never retried forever.
var rateLimitHintRe = regexp.MustCompile(`(?i)rate.?limit|usage.?limit|session.?limit|too many requests|hit your (?:session |usage )?limit|limit resets|resets\b|try again at|throttl|\b429\b|resource.?exhausted|tokens per min`)

// timeoutHintRe recognizes timeout-worded errors. The wrapper lumps these into
// retry_later, but loom keeps a distinct Timeout class (its own backoff), so we
// re-derive it from the text rather than collapsing every retry_later into
// Transient.
var timeoutHintRe = regexp.MustCompile(`(?i)\btimeout\b|etimedout|connection.?timed?.?out|timed?.?out|deadline.?exceeded`)

// residualPatterns is the single, backend-agnostic fallback table. It encodes
// the distinctions loom acts on that the harness-wrapper classifier does not
// model — auth, billing, model-not-found, context overflow, timeout — plus the
// bare numeric / timing / prose signals the wrapper's anchored matchers miss
// (e.g. a bare 429 from an unknown harness, "try again at <time>"). It is only
// consulted when the wrapper returns no actionable classification, so any
// overlap with wrapper-owned cost/transport patterns is dead-but-safe.
//
// Ordered RateLimited-first, Transient-last, mirroring the precedence of the
// former per-backend tables.
var residualPatterns = []errorPattern{
	{regexp.MustCompile(`(?i)\b429\b|too many requests|tokens per min|overloaded_error|resource.?exhausted|resource_exhausted|rate.?limit|usage.?limit|session.?limit|resets at|resets \d{1,2}:\d{2}|try again at\s+\d`), RateLimited, "rate limit exceeded"},
	{regexp.MustCompile(`(?i)\b401\b|unauthorized|unauthenticated|permission.?denied|forbidden|invalid.?api.?key|incorrect.?api.?key|invalid.*key|authentication.?failed|ANTHROPIC_API_KEY|OPENAI_API_KEY|GEMINI_API_KEY|GOOGLE_API_KEY|CURSOR_API_KEY`), AuthFailure, "authentication failed"},
	{regexp.MustCompile(`(?i)\b402\b|payment.?required|insufficient.?(?:credits|quota)|insufficient_quota|exceeded.*quota|quota.?exceeded|\bquota\b|\bcredits\b|\bbilling\b`), BillingError, "billing error"},
	{regexp.MustCompile(`(?i)model.?not.?found|model.*not found|model.*does not exist|model.*not.*exist|model_not_found|unsupported.?model|unknown.?model|invalid.?model|selected model.*may not exist|selected model.*may not have access to it|\b404\b.*model`), ModelNotFound, "model not found"},
	{regexp.MustCompile(`(?i)context.?length|context.?window|context_length_exceeded|maximum context length|max.?tokens|max.*tokens|token.?limit|prompt.?too.?long|too.?long`), ContextOverflow, "context length exceeded"},
	{regexp.MustCompile(`(?i)\btimeout\b|etimedout|connection.?timed?.?out|timed?.?out|deadline.?exceeded`), Timeout, "connection timeout"},
	{regexp.MustCompile(`(?i)\b50[023]\b|\b529\b|server.?error|server_error|internal.?server.?error|internal.?error|service.?unavailable|backend.?error|overloaded`), Transient, "server error"},
}

// classifyWithPatterns runs an ordered pattern table against text, extracting a
// Retry-After hint for rate-limit matches.
func classifyWithPatterns(text string, patterns []errorPattern) *classifyResult {
	if text == "" {
		return nil
	}
	for _, p := range patterns {
		if p.re.MatchString(text) {
			r := &classifyResult{
				Class:   p.class,
				Message: p.msg,
			}
			if p.class == RateLimited {
				r.RetryAfter = parseRetryAfter(text)
			}
			return r
		}
	}
	return nil
}

// ClassifyFromLog reads the tail of an agent log file and classifies the error.
// It never returns nil — an Unknown classification is returned if nothing matches.
func ClassifyFromLog(logPath string, exitCode int, backend string) *AgentError {
	logTail, _ := readLogTail(logPath, 100)
	return classifyFromText(logTail, exitCode, backend)
}

// ClassifyFromOutput classifies an error from raw output text (e.g. captured
// stream-json lines) instead of reading from a log file. Same classification
// logic as ClassifyFromLog. Never returns nil.
func ClassifyFromOutput(output string, exitCode int, backend string) *AgentError {
	return classifyFromText(output, exitCode, backend)
}

// classifyFromText is the shared classification implementation. It is a thin
// adapter over the harness-wrapper classifier (the single source of truth for
// cost / rate-limit / transport / API-error fingerprints), with a small
// loom-specific residual for the distinctions the wrapper does not model.
func classifyFromText(text string, exitCode int, backend string) *AgentError {
	now := time.Now()

	var result *classifyResult

	// 1. Cross-cutting wrapper signal: the loom-side translator prepends this
	//    marker when the backend CLI is missing. It outranks everything else.
	if backendUnavailableRe.MatchString(text) {
		result = &classifyResult{Class: BackendUnavailable, Message: "backend binary not on PATH"}
	}

	// A wrapper launch failure (PTY/exec) means the backend process never
	// started — typically the backend binary was mid-update and momentarily
	// unexecutable ("exec format error"). Treat it as a retryable SpawnFailure
	// and keep the reason so it surfaces instead of a generic Unknown.
	if result == nil && agentLaunchFailedRe.MatchString(text) {
		result = &classifyResult{
			Class:   SpawnFailure,
			Message: "agent process failed to launch (backend binary may be updating or incompatible)",
		}
	}

	// 2. Primary: harness-wrapper owns the cost/rate-limit/transport/API-error
	//    patterns. Run its classifier as a one-shot over the captured text and
	//    map the structured Classification onto our ErrorClass.
	if result == nil && text != "" {
		result = fromClassification(wrapper.ClassifyOutput(backend, text), text)
	}

	// 3. Residual: loom-specific distinctions the wrapper does not model.
	if result == nil {
		result = classifyWithPatterns(text, residualPatterns)
	}

	// 4. Exit-code fallback.
	if result == nil {
		result = &classifyResult{
			Class:   classifyByExitCode(exitCode),
			Message: classifyByExitCodeMessage(exitCode),
		}
	}

	return &AgentError{
		Class:      result.Class,
		ExitCode:   exitCode,
		Message:    result.Message,
		RawOutput:  text,
		Backend:    backend,
		RetryAfter: result.RetryAfter,
		Timestamp:  now,
	}
}

// fromClassification maps a harness-wrapper Classification onto loom's
// ErrorClass. It returns nil when the wrapper reported nothing actionable (or
// an API-error code we don't special-case), leaving the residual table and
// exit-code fallback to decide. text is the raw output, used to split the
// wrapper's coarse blocked_by_cost status and to recover a Retry-After hint.
func fromClassification(c wrapper.Classification, text string) *classifyResult {
	switch c.Status {
	case wrapper.StatusAPIError:
		return apiErrorResult(c, text)
	case wrapper.StatusRetryLater:
		// Transient / transport / "try again later" — retryable. Preserve
		// loom's distinct Timeout class when the wrapper's retry_later is
		// actually a timeout (e.g. gemini "deadline exceeded").
		if timeoutHintRe.MatchString(text) {
			return &classifyResult{Class: Timeout, Message: reasonOr(c.Reason, "connection timeout"), RetryAfter: c.RetryAfter}
		}
		return &classifyResult{Class: Transient, Message: reasonOr(c.Reason, "transient error"), RetryAfter: c.RetryAfter}
	case wrapper.StatusBlockedByCost:
		return blockedByCostResult(c, text)
	case wrapper.StatusBinaryNotFound:
		return &classifyResult{Class: BackendUnavailable, Message: "backend binary not on PATH"}
	default:
		// idle / failed / unknown / waiting_for_input / stale / interrupted /
		// empty — nothing actionable here.
		return nil
	}
}

// apiErrorResult dispatches a wrapper StatusAPIError on its upstream HTTP code.
// Unmapped codes (400/403/422/…) return nil so the residual can refine them
// from the text (e.g. a 403 "forbidden" → AuthFailure).
func apiErrorResult(c wrapper.Classification, text string) *classifyResult {
	switch {
	case c.HTTPCode == 401:
		return &classifyResult{Class: AuthFailure, Message: reasonOr(c.Reason, "authentication failed (401)")}
	case c.HTTPCode == 402:
		return &classifyResult{Class: BillingError, Message: reasonOr(c.Reason, "billing error (402)")}
	case c.HTTPCode == 404:
		return &classifyResult{Class: ModelNotFound, Message: reasonOr(c.Reason, "model not found (404)")}
	case c.HTTPCode == 429:
		return &classifyResult{Class: RateLimited, Message: reasonOr(c.Reason, "rate limit exceeded"), RetryAfter: retryAfterFrom(c, text)}
	case c.HTTPCode == 408 || (c.HTTPCode >= 500 && c.HTTPCode <= 599):
		return &classifyResult{Class: Transient, Message: reasonOr(c.Reason, "server error"), RetryAfter: c.RetryAfter}
	case c.HTTPCode == 0:
		// Transport-level error (socket closed, connection reset) — retryable.
		return &classifyResult{Class: Transient, Message: reasonOr(c.Reason, "transport error"), RetryAfter: c.RetryAfter}
	default:
		return nil
	}
}

// blockedByCostResult splits the wrapper's coarse blocked_by_cost: rate/usage/
// session-limit signals are retryable RateLimited; budget/credit/billing
// exhaustion (and the ambiguous remainder) are fatal BillingError.
func blockedByCostResult(c wrapper.Classification, text string) *classifyResult {
	if rateLimitHintRe.MatchString(text) {
		return &classifyResult{Class: RateLimited, Message: reasonOr(c.Reason, "rate limit exceeded"), RetryAfter: retryAfterFrom(c, text)}
	}
	return &classifyResult{Class: BillingError, Message: reasonOr(c.Reason, "billing or quota limit")}
}

// reasonOr returns the wrapper's reason when present, else a fallback.
func reasonOr(reason, fallback string) string {
	if r := strings.TrimSpace(reason); r != "" {
		return r
	}
	return fallback
}

// retryAfterFrom prefers the wrapper's parsed hint, falling back to a
// Retry-After token in the text.
func retryAfterFrom(c wrapper.Classification, text string) time.Duration {
	if c.RetryAfter > 0 {
		return c.RetryAfter
	}
	return parseRetryAfter(text)
}

// parseRetryAfter extracts a "retry-after: N" value (seconds) from text.
func parseRetryAfter(text string) time.Duration {
	if m := retryAfterRe.FindStringSubmatch(text); len(m) > 1 {
		if secs, err := strconv.Atoi(m[1]); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

// maxLogTailBytes is the maximum number of bytes to read from the end of a log file.
const maxLogTailBytes int64 = 64 * 1024

// readLogTail reads the last maxLines lines from a file, reading at most
// maxLogTailBytes from the end. Returns empty string on any error.
func readLogTail(path string, maxLines int) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}

	size := stat.Size()
	if size == 0 {
		return "", nil
	}

	readSize := maxLogTailBytes
	if size < readSize {
		readSize = size
	}

	offset := size - readSize

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}

	buf := make([]byte, int(readSize))
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}
	buf = buf[:n]

	// Take the last maxLines lines.
	lines := bytes.Split(buf, []byte("\n"))
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return string(bytes.Join(lines, []byte("\n"))), nil
}

// classifyByExitCode provides a generic fallback classification based on
// the process exit code when no pattern matches.
func classifyByExitCode(exitCode int) ErrorClass {
	switch exitCode {
	case 137: // 128+9 = SIGKILL (OOM killer or watchdog)
		return Timeout
	case 143: // 128+15 = SIGTERM (graceful shutdown)
		return Transient
	default:
		return Unknown
	}
}

func classifyByExitCodeMessage(exitCode int) string {
	switch exitCode {
	case 137:
		return "process killed by signal 9 (SIGKILL), exit code 137"
	case 143:
		return "process terminated by signal 15 (SIGTERM), exit code 143"
	default:
		return fmt.Sprintf("unclassified error (exit code %d)", exitCode)
	}
}
