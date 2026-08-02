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
	Class      Outcome
	Message    string
	RetryAfter time.Duration
}

// errorPattern defines a single regex→class mapping over the wrapper's
// canonical harness-output taxonomy. It powers the residual table (signals
// the wrapper's anchored matchers miss) rather than five near-identical
// per-backend tables.
type errorPattern struct {
	re    *regexp.Regexp
	class wrapper.ErrorClass
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

// AuthRequiredMarker and UsageLimitedMarker are the stable log markers the
// inner backend subprocess emits when the harness itself declared the turn
// terminal for one of the two BLAMELESS reasons: the login has expired, or the
// quota window is exhausted.
//
// These exist because the harness now tells us categorically. Before, a
// logged-out or quota-walled turn arrived here as prose in a log tail, and the
// residual regex table had to guess from wording that varies by harness and
// changes without notice. A miss classified an expired login as Unknown, which
// means "bounded restart then block": the agent burned its restart budget
// re-running a turn that could not possibly succeed, and the real cause never
// reached the operator. Reading the harness's own verdict removes the guess.
//
// The two classes they map to are the ones the policy already treats as not
// the agent's fault — AuthFailure stops fatally with an operator-actionable
// message, RateLimited retries UNCOUNTED with the rate-limit backoff — and
// neither is quarantine-eligible, so a quota window cannot push a task toward
// quarantine.
//
// Stable string contract: changing either requires updating the emitter
// (internal/cli/backends.terminalTurnInvocationError).
const (
	AuthRequiredMarker = "loom: harness login expired or re-authentication required"
	UsageLimitedMarker = "loom: harness usage or session limit reached"
)

var (
	authRequiredRe = regexp.MustCompile(regexp.QuoteMeta(AuthRequiredMarker))
	usageLimitedRe = regexp.MustCompile(regexp.QuoteMeta(UsageLimitedMarker))
)

// timeoutHintRe recognizes timeout-worded errors. The wrapper refines its
// retry hits by the matched phrase; loom additionally upgrades a Transient
// whose surrounding text names a timeout, preserving the distinct Timeout
// class (its own backoff bucket) exactly as before the wrapper owned the
// fine taxonomy.
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
	{regexp.MustCompile(`(?i)\b429\b|too many requests|tokens per min|overloaded_error|resource.?exhausted|resource_exhausted|rate.?limit|usage.?limit|session.?limit|resets at|resets \d{1,2}:\d{2}|try again at\s+\d`), wrapper.ErrRateLimited, "rate limit exceeded"},
	{regexp.MustCompile(`(?i)\b401\b|unauthorized|unauthenticated|permission.?denied|forbidden|invalid.?api.?key|incorrect.?api.?key|invalid.*key|authentication.?failed|ANTHROPIC_API_KEY|OPENAI_API_KEY|GEMINI_API_KEY|GOOGLE_API_KEY|CURSOR_API_KEY`), wrapper.ErrAuth, "authentication failed"},
	{regexp.MustCompile(`(?i)\b402\b|payment.?required|insufficient.?(?:credits|quota)|insufficient_quota|exceeded.*quota|quota.?exceeded|\bquota\b|\bcredits\b|\bbilling\b`), wrapper.ErrBilling, "billing error"},
	{regexp.MustCompile(`(?i)model requires a newer version|requires a newer version of (?:codex|claude)|upgrade to the latest (?:app or )?cli`), wrapper.ErrModelNotFound, "backend CLI is incompatible with the selected model"},
	{regexp.MustCompile(`(?i)model.?not.?found|model.*not found|model.*does not exist|model.*not.*exist|model_not_found|unsupported.?model|unknown.?model|invalid.?model|selected model.*may not exist|selected model.*may not have access to it|\b404\b.*model`), wrapper.ErrModelNotFound, "model not found"},
	{regexp.MustCompile(`(?i)context.?length|context.?window|context_length_exceeded|maximum context length|max.?tokens|max.*tokens|token.?limit|prompt.?too.?long|too.?long`), wrapper.ErrContextOverflow, "context length exceeded"},
	{regexp.MustCompile(`(?i)\btimeout\b|etimedout|connection.?timed?.?out|timed?.?out|deadline.?exceeded`), wrapper.ErrTimeout, "connection timeout"},
	{regexp.MustCompile(`(?i)\b50[023]\b|\b529\b|server.?error|server_error|internal.?server.?error|internal.?error|service.?unavailable|backend.?error|overloaded`), wrapper.ErrTransient, "server error"},
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
				Class:   OutcomeFromHarness(p.class),
				Message: p.msg,
			}
			if p.class == wrapper.ErrRateLimited {
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
		result = &classifyResult{Class: OutcomeFromDomain(BackendUnavailableOutcome), Message: "backend binary not on PATH"}
	}

	// A wrapper launch failure (PTY/exec) means the backend process never
	// started — typically the backend binary was mid-update and momentarily
	// unexecutable ("exec format error"). Treat it as a retryable SpawnFailure
	// and keep the reason so it surfaces instead of a generic Unknown.
	if result == nil && agentLaunchFailedRe.MatchString(text) {
		result = &classifyResult{
			Class:   OutcomeFromDomain(SpawnFailureOutcome),
			Message: "agent process failed to launch (backend binary may be updating or incompatible)",
		}
	}

	// The harness's own terminal verdict outranks any pattern matching: it is
	// a categorical statement, not an inference from wording. Both are
	// blameless — see the marker docs — so getting here rather than falling
	// through to the residual table is what keeps a quota window from
	// consuming an agent's restart budget.
	if result == nil && authRequiredRe.MatchString(text) {
		result = &classifyResult{
			Class:   OutcomeFromHarness(wrapper.ErrAuth),
			Message: "harness login expired or re-authentication required — renew the harness login",
		}
	}
	if result == nil && usageLimitedRe.MatchString(text) {
		result = &classifyResult{
			Class:   OutcomeFromHarness(wrapper.ErrRateLimited),
			Message: "harness usage or session limit reached — retry after the quota window resets",
		}
		// A usage wall often states when it lifts. Honor it if present; the
		// rate-limit backoff has its own default otherwise.
		if d := parseRetryAfter(text); d > 0 {
			result.RetryAfter = d
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
			Class:   OutcomeFromHarness(classifyByExitCode(exitCode)),
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

// fromClassification adapts a harness-wrapper Classification. The wrapper now
// owns the fine taxonomy (Classification.Class), so this collapses to a thin
// mapping: take the wrapper's class verbatim, fold the binary-not-found signal
// into loom's BackendUnavailable domain outcome, and keep two loom-side
// behaviors — (a) an ErrUnknown/ErrNone result is treated as "nothing
// actionable" so the residual table and exit-code fallback can refine it
// (e.g. a 403 "forbidden" → ErrAuth via the residual), and (b) a Transient
// whose surrounding text names a timeout is upgraded to loom's distinct
// Timeout backoff bucket, exactly as before.
func fromClassification(c wrapper.Classification, text string) *classifyResult {
	if c.Status == wrapper.StatusBinaryNotFound {
		return &classifyResult{Class: OutcomeFromDomain(BackendUnavailableOutcome), Message: "backend binary not on PATH"}
	}
	switch c.Class {
	case wrapper.ErrNone, wrapper.ErrUnknown:
		// Nothing actionable (idle / waiting_for_input / unmapped API code) —
		// residual/exit-code decide.
		return nil
	case wrapper.ErrTransient:
		if timeoutHintRe.MatchString(text) {
			return &classifyResult{Class: OutcomeFromHarness(wrapper.ErrTimeout), Message: reasonOr(c.Reason, "connection timeout"), RetryAfter: c.RetryAfter}
		}
		return &classifyResult{Class: OutcomeFromHarness(wrapper.ErrTransient), Message: reasonOr(c.Reason, "transient error"), RetryAfter: c.RetryAfter}
	case wrapper.ErrRateLimited:
		return &classifyResult{Class: OutcomeFromHarness(wrapper.ErrRateLimited), Message: reasonOr(c.Reason, "rate limit exceeded"), RetryAfter: retryAfterFrom(c, text)}
	default:
		return &classifyResult{Class: OutcomeFromHarness(c.Class), Message: reasonOr(c.Reason, c.Class.String()), RetryAfter: c.RetryAfter}
	}
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
func classifyByExitCode(exitCode int) wrapper.ErrorClass {
	switch exitCode {
	case 137: // 128+9 = SIGKILL (OOM killer or watchdog)
		return wrapper.ErrTimeout
	case 143: // 128+15 = SIGTERM (graceful shutdown)
		return wrapper.ErrTransient
	default:
		return wrapper.ErrUnknown
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
