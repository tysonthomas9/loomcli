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
	Evidence   Evidence
}

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

// BillingWallMarker is the stable log marker for a harness parked on a billing
// / credit wall — the case harness-wrapper does NOT model, so no turn reason
// ever names it.
//
// It is the one wall that is neither blameless nor retryable: a spent quota
// window lifts on its own and an expired login is one command away, but an
// account with no credits cannot run a turn again until a human pays. Mapping
// it to ErrBilling is what makes agentpolicy stop the supervisor fatally
// instead of spending the restart budget on turns that cannot succeed.
//
// It currently has NO in-tree emitter. The screen-scrape detector that used to
// raise it was removed (0 true positives in 15 days, and its false positives
// were fatal); the marker and its classification arm are kept deliberately, as
// a stable log-text contract for logs already written and for the day
// harness-wrapper names a billing reason of its own. Changing the string
// therefore still requires updating whatever emits it next.
const BillingWallMarker = "loom: harness billing or credit wall reached"

var billingWallRe = regexp.MustCompile(regexp.QuoteMeta(BillingWallMarker))

// RunTurnDeadlineMarker is the stable marker the inner loom subprocess emits
// when the turn was ended by loom's OWN per-turn deadline — the ceiling
// derived from the role's max_run_duration and exported as
// LOOM_RUN_TURN_TIMEOUT_SECONDS, applied to a context derived inside
// invokeClaudeRunTurn.
//
// It exists for the same reason the five markers above do: the side that KNOWS
// the answer says so, categorically. Without it the expiry arrives here as a
// bare "context deadline exceeded", the residual table's timeout pattern
// matches it, and the verdict reads "connection timeout" — pointing an
// operator at the network when the actual lever is the role's time budget.
// That is not a wording nit: Timeout is quarantine-eligible, so every expiry
// also recorded a no-progress kill against a ticket that had done nothing
// wrong.
//
// The emitter only sets it when errors.Is(derivedCtx.Err(),
// context.DeadlineExceeded) is true on the context IT created, so a daemon
// shutdown (context.Canceled) and an upstream deadline from someone else's
// context can never wear it.
//
// Stable string contract: changing it requires updating the emitter
// (internal/cli/backends.runTurnDeadlineInvocationError).
const RunTurnDeadlineMarker = "loom: run-turn deadline exceeded"

// runTurnDeadlineRe is the precompiled matcher used by classifyHarnessMarkers.
var runTurnDeadlineRe = regexp.MustCompile(regexp.QuoteMeta(RunTurnDeadlineMarker))

// ClassifyFromLog reads the tail of an agent log file and classifies the error.
// It never returns nil — an Unknown classification is returned if nothing matches.
func ClassifyFromLog(logPath string, exitCode int, backend string) *AgentError {
	return ClassifyFromLogAt(logPath, 0, exitCode, backend)
}

// ClassifyFromLogAt is ClassifyFromLog scoped to a single run. Agent logs are
// append-only and per-role, so they span days and many runs; classifying the
// raw tail can read a PREVIOUS run's output as this run's verdict — a stale
// "Not logged in" banner becoming a fresh AuthFailure, which stops the agent
// fatally for a login that is in fact fine. offset is the byte position of the
// current run's first line (see supervisor.AgentProcess.LogFileStartOffset);
// nothing before it is considered.
//
// offset 0 is exactly ClassifyFromLog. An offset past the end of the file means
// the log was rotated or truncated under us, so the run's own bytes are gone:
// it falls back to whole-file behavior rather than classifying nothing.
func ClassifyFromLogAt(logPath string, offset int64, exitCode int, backend string) *AgentError {
	logTail, _ := readLogTailAt(logPath, 100, offset)
	return classifyFromText(logTail, exitCode, backend)
}

// ClassifyFromOutput classifies an error from raw output text (e.g. captured
// stream-json lines) instead of reading from a log file. Same classification
// logic as ClassifyFromLog. Never returns nil.
func ClassifyFromOutput(output string, exitCode int, backend string) *AgentError {
	return classifyFromText(output, exitCode, backend)
}

// ClassifyMarker classifies text that carries one of the explicit, categorical
// harness markers, and ONLY those. ok=false means no marker was present — it
// never falls through to pattern matching, which is the whole point: callers
// use it where a categorical statement may override a verdict they already
// hold, and a guess must not.
func ClassifyMarker(text string) (*AgentError, bool) {
	result := classifyHarnessMarkers(text)
	if result == nil {
		return nil, false
	}
	return &AgentError{
		Class:      result.Class,
		Message:    result.Message,
		RetryAfter: result.RetryAfter,
		RawOutput:  text,
		Timestamp:  time.Now(),
	}, true
}

// ClassifyMarkerFromLog is ClassifyMarker over the tail of an agent log file,
// using the same bounded read as ClassifyFromLog. It exists because the
// supervisor has no access to the unexported tail reader.
func ClassifyMarkerFromLog(logPath string) (*AgentError, bool) {
	return ClassifyMarkerFromLogAt(logPath, 0)
}

// ClassifyMarkerFromLogAt is ClassifyMarkerFromLog scoped to a single run, the
// way ClassifyFromLogAt scopes ClassifyFromLog — and it matters MORE here, not
// less. A marker is categorical: it overrides every other verdict and stops the
// agent fatally. A previous run's "Not logged in · Run /login" left in the
// append-only log therefore condemns every later run on that agent, no matter
// how healthy, until someone rotates the file by hand. offset is the byte
// position of the current run's first line
// (supervisor.AgentProcess.LogFileStartOffset); 0 restores whole-file behavior.
func ClassifyMarkerFromLogAt(logPath string, offset int64) (*AgentError, bool) {
	logTail, _ := readLogTailAt(logPath, 100, offset)
	return ClassifyMarker(logTail)
}

// classifyHarnessMarkers matches the explicit, categorical markers that
// outrank every pattern-based inference: the loom-side backend-missing
// marker, a wrapper launch failure, loom's own per-turn deadline expiry, and
// the harness's own terminal verdict (auth required / usage limited).
// Returns nil when none apply.
func classifyHarnessMarkers(text string) *classifyResult {
	// 1. Cross-cutting wrapper signal: the loom-side translator prepends this
	//    marker when the backend CLI is missing. It outranks everything else.
	if backendUnavailableRe.MatchString(text) {
		return &classifyResult{
			Class:    OutcomeFromDomain(BackendUnavailableOutcome),
			Message:  "backend binary not on PATH",
			Evidence: markerEvidence("BackendUnavailableMarker", backendUnavailableRe, BackendUnavailableMarker, text),
		}
	}

	// A wrapper launch failure (PTY/exec) means the backend process never
	// started — typically the backend binary was mid-update and momentarily
	// unexecutable ("exec format error"). Treat it as a retryable SpawnFailure
	// and keep the reason so it surfaces instead of a generic Unknown.
	if agentLaunchFailedRe.MatchString(text) {
		return &classifyResult{
			Class:    OutcomeFromDomain(SpawnFailureOutcome),
			Message:  "agent process failed to launch (backend binary may be updating or incompatible)",
			Evidence: markerEvidence("AgentLaunchFailedMarker", agentLaunchFailedRe, AgentLaunchFailedMarker, text),
		}
	}

	// loom's OWN per-turn deadline. This arm MUST stay above the residual
	// pattern table: the same text carries a bare "context deadline exceeded"
	// from the wrapper, and `deadline.?exceeded` down there would render it as
	// "connection timeout". The marker is a categorical statement from the
	// side that owns the timer, not an inference from wording.
	if runTurnDeadlineRe.MatchString(text) {
		return &classifyResult{
			Class:   OutcomeFromDomain(RunTurnDeadlineOutcome),
			Message: "the turn exceeded loom's per-turn deadline (the role's max_run_duration minus 120s) — raise the role's max_run_duration or split the task",
			// The evidence source is the marker itself, so the surfaced
			// verdict reads evidence_rule=RunTurnDeadlineMarker rather than
			// the residual timeout pattern this arm exists to outrank. The
			// Detail tail carries the resolved deadline the emitter names.
			Evidence: markerEvidence("RunTurnDeadlineMarker", runTurnDeadlineRe, RunTurnDeadlineMarker, text),
		}
	}

	// The harness's own terminal verdict outranks any pattern matching: it is
	// a categorical statement, not an inference from wording. Both are
	// blameless — see the marker docs — so returning here rather than falling
	// through to the residual table is what keeps a quota window from
	// consuming an agent's restart budget.
	// A billing wall outranks the auth arm below: a screen showing both an
	// empty credit balance and a stale logged-out banner is a billing problem,
	// and renewing the login would not change that.
	if billingWallRe.MatchString(text) {
		return &classifyResult{
			Class:   OutcomeFromHarness(wrapper.ErrBilling),
			Message: "harness billing or credit wall reached — the account cannot run turns until billing is resolved",
			// This arm shipped without evidence because it shipped without an
			// emitter: nothing could raise the marker, so nothing was ever
			// recorded and the omission was invisible. It has one now
			// (backends.terminalTurnInvocationError, off chat.CodeBillingWall),
			// and a billing verdict is FATAL — it stops the supervisor and can
			// gate the credential scope. ADR-0002's whole argument for keeping a
			// fatal disposition is that a single occurrence has to be judgeable
			// without a cluster, which takes a record saying which rule fired
			// and on what text. Detail carries the harness's own tag.
			Evidence: markerEvidence("BillingWallMarker", billingWallRe, BillingWallMarker, text),
		}
	}
	if authRequiredRe.MatchString(text) {
		return &classifyResult{
			// The Message stays byte-for-byte as it was: it is operator-facing
			// copy. The harness's own reason tail, which used to be discarded
			// here, now rides in Evidence.Detail instead of being lost.
			Class:    OutcomeFromHarness(wrapper.ErrAuth),
			Message:  "harness login expired or re-authentication required — renew the harness login",
			Evidence: markerEvidence("AuthRequiredMarker", authRequiredRe, AuthRequiredMarker, text),
		}
	}
	if usageLimitedRe.MatchString(text) {
		result := &classifyResult{
			Class:    OutcomeFromHarness(wrapper.ErrRateLimited),
			Message:  "harness usage or session limit reached — retry after the quota window resets",
			Evidence: markerEvidence("UsageLimitedMarker", usageLimitedRe, UsageLimitedMarker, text),
		}
		// A usage wall often states when it lifts. Honor it if present; the
		// rate-limit backoff has its own default otherwise.
		if d := parseRetryAfter(text); d > 0 {
			result.RetryAfter = d
		}
		return result
	}
	return nil
}

// markerEvidence records a harness-marker hit: the marker's Go const name as
// the rule, a redacted window around it as the excerpt, and — the point of the
// exercise — the harness's OWN reason as Detail.
//
// terminalTurnInvocationError (internal/cli/backends/invocation_error.go:165)
// appends that reason after "<marker>: ", and this classifier used to throw it
// away and substitute a canned message. On the pinned wrapper the reason is the
// flat chat.ReasonAuthRequired constant for all five of its producers, so Detail
// will be identical every time; that is expected. Today the discriminating
// signal for an auth verdict is Screen, not Detail.
func markerEvidence(rule string, re *regexp.Regexp, marker, text string) Evidence {
	ev := newTextEvidence(EvidenceHarnessMarker, rule, re, text)
	ev.Detail = capString(redactEvidence(sanitizeText(markerReason(marker, text))), evidenceDetailCap)
	return ev
}

// markerReason returns the text following "<marker>: " up to the end of that
// line, or "" when the marker carried no reason tail.
func markerReason(marker, text string) string {
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	line, _, _ := strings.Cut(text[i+len(marker):], "\n")
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ":"))
}

// classifyFromText is the shared classification implementation, and it is now
// three steps: markers → the wrapper's finished-output classifier → exit code.
//
// The middle step used to be two. loom carried its own residual regex table
// for the signals the wrapper's anchored matchers missed, which meant the same
// harness-output patterns were maintained here, in harness-wrapper, and in
// meta-harness, with nothing holding the three copies together. The table now
// lives behind wrapper.ClassifyFinishedOutput — the POST-EXIT entry point,
// which runs the same per-harness classifier and only then consults the
// residual rows. What loom keeps is what loom owns: marker precedence, the
// domain outcomes, the exit-code fallback, evidence redaction, and policy.
func classifyFromText(text string, exitCode int, backend string) *AgentError {
	now := time.Now()

	// 1. Explicit markers and the harness's own terminal verdict. Categorical
	//    statements outrank every inference below.
	result := classifyHarnessMarkers(text)

	// 2. harness-wrapper owns every harness-output pattern: the per-harness
	//    cost/rate-limit/transport/API-error fingerprints AND the residual
	//    rows behind them. One call, one structured Classification.
	if result == nil && text != "" {
		result = fromClassification(wrapper.ClassifyFinishedOutput(backend, text), text)
	}

	// 3. Exit-code fallback.
	if result == nil {
		result = &classifyResult{
			Class:   OutcomeFromHarness(classifyByExitCode(exitCode)),
			Message: classifyByExitCodeMessage(exitCode),
			Evidence: Evidence{
				Source:   EvidenceExitCode,
				Rule:     exitCodeRule(exitCode),
				ExitCode: exitCode,
			},
		}
	}

	// Provenance is stamped once, here, so every step above only has to
	// describe what IT matched.
	evidence := result.Evidence
	evidence.ExitCode = exitCode
	evidence.ScannedBytes = len(text)
	// Screen description is for auth verdicts only — it answers "was this a
	// real login wall?", which is meaningless for a rate limit or a timeout.
	if result.Class.IsClass(wrapper.ErrAuth) {
		evidence.Screen = describeScreen(text)
	}

	return &AgentError{
		Class:      result.Class,
		ExitCode:   exitCode,
		Message:    result.Message,
		RawOutput:  text,
		Backend:    backend,
		RetryAfter: result.RetryAfter,
		Timestamp:  now,
		Evidence:   evidence,
	}
}

// exitCodeRule names the arm classifyByExitCode took, mirroring its switch.
func exitCodeRule(exitCode int) string {
	switch exitCode {
	case 137:
		return "exit.137_sigkill"
	case 143:
		return "exit.143_sigterm"
	default:
		return "exit.default"
	}
}

// fromClassification adapts a harness-wrapper Classification. The wrapper owns
// the fine taxonomy (Classification.Class) and, since the residual table moved
// there, the timeout refinement too — so this collapses to a mapping: take the
// wrapper's class verbatim, fold the binary-not-found signal into loom's
// BackendUnavailable domain outcome, and treat an ErrUnknown/ErrNone result as
// "nothing actionable" so the exit-code fallback can still speak.
//
// The messages below are unchanged fallbacks, not overrides: every residual
// row now arrives carrying its own Reason (the message it had when the row
// lived here), so reasonOr takes the wrapper's word and the defaults only
// apply where they always did.
func fromClassification(c wrapper.Classification, text string) *classifyResult {
	if c.Status == wrapper.StatusBinaryNotFound {
		return &classifyResult{
			Class:    OutcomeFromDomain(BackendUnavailableOutcome),
			Message:  "backend binary not on PATH",
			Evidence: classificationEvidence(c, text),
		}
	}
	switch c.Class {
	case wrapper.ErrNone, wrapper.ErrUnknown:
		// Nothing actionable (idle / waiting_for_input / unmapped API code, and
		// no residual row matched either) — the exit code decides.
		return nil
	case wrapper.ErrTransient:
		return &classifyResult{Class: OutcomeFromHarness(wrapper.ErrTransient), Message: reasonOr(c.Reason, "transient error"), RetryAfter: c.RetryAfter, Evidence: classificationEvidence(c, text)}
	case wrapper.ErrRateLimited:
		return &classifyResult{Class: OutcomeFromHarness(wrapper.ErrRateLimited), Message: reasonOr(c.Reason, "rate limit exceeded"), RetryAfter: c.RetryAfter, Evidence: classificationEvidence(c, text)}
	default:
		return &classifyResult{Class: OutcomeFromHarness(c.Class), Message: reasonOr(c.Reason, c.Class.String()), RetryAfter: c.RetryAfter, Evidence: classificationEvidence(c, text)}
	}
}

// residualRulePrefix marks a Classification.Rule as a residual-table row
// rather than a per-harness matcher. The ids themselves
// (`residual.auth`, …) are a contract named by
// docs/adr/0002-authfailure-stays-terminal.md; the wrapper owns the rows now
// and keeps the ids, so the ADR's revisit trigger keeps reading what it always
// read.
const residualRulePrefix = "residual."

// classificationEvidence records a wrapper verdict, splitting on WHICH matcher
// produced it so the evidence source keeps meaning what it meant when the two
// tables lived in different repositories.
//
// A residual row is recorded exactly as the table recorded it when it lived
// here: source=residual_pattern, the row id as the rule, the matched text, and
// a redacted window around it. That is what TestClassifyEvidenceOverBroadResidualAuth
// asserts and what the ADR's third revisit trigger reads.
//
// Everything else stays source=wrapper_classifier with the wrapper's reason as
// Detail — including the Transient→Timeout refinement, which keeps the rule id
// it had when loom performed it (`wrapper/timeout_upgrade`). Its Match is
// deliberately NOT recorded: the refinement's evidence is the wrapper's reason,
// which Detail already carries, and adding a field to that record would change
// what an operator reading a verdict sees for no new information.
func classificationEvidence(c wrapper.Classification, text string) Evidence {
	if strings.HasPrefix(c.Rule, residualRulePrefix) {
		return residualEvidence(c, text)
	}
	rule := c.Rule
	if rule == "" {
		rule = string(c.Status) + "/" + c.Class.String()
	}
	return Evidence{
		Source:   EvidenceWrapper,
		Rule:     rule,
		Detail:   capString(redactEvidence(sanitizeText(c.Reason)), evidenceDetailCap),
		HTTPCode: c.HTTPCode,
	}
}

// residualEvidence rebuilds the Match/Excerpt pair for a residual hit from the
// matched text the wrapper reports. The regex no longer lives here, so the
// window is located by finding that text — which is the same span
// FindStringIndex returned, because the wrapper reports its FIRST match.
func residualEvidence(c wrapper.Classification, text string) Evidence {
	ev := Evidence{Source: EvidenceResidual, Rule: c.Rule}
	if c.Match == "" || text == "" {
		return ev
	}
	ev.Match = capString(redactEvidence(sanitizeText(c.Match)), evidenceMatchCap)
	// No window when the matched text is not in the blob we were handed:
	// better a record that says only what it knows than one built around a
	// position we had to guess.
	if i := strings.Index(text, c.Match); i >= 0 {
		ev.Excerpt = capString(redactEvidence(sanitizeText(excerptWindow(text, i, i+len(c.Match)))), evidenceExcerptCap)
	}
	return ev
}

// reasonOr returns the wrapper's reason when present, else a fallback.
func reasonOr(reason, fallback string) string {
	if r := strings.TrimSpace(reason); r != "" {
		return r
	}
	return fallback
}

// retryAfterRe extracts a Retry-After header value from log/output text. The
// wrapper parses its own for a classified result; this copy serves the ONE
// remaining loom-side reader, the usage-limited marker's reason tail, where
// the harness often states when the wall lifts.
var retryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

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
	return readLogTailAt(path, maxLines, 0)
}

// readLogTailAt is readLogTail restricted to the region at or after start.
// A start beyond the current size means the file shrank (rotation), so the
// whole file is read instead — the same answer readLogTail gives.
func readLogTailAt(path string, maxLines int, start int64) (string, error) {
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

	if start < 0 || start > size {
		start = 0
	}

	readSize := maxLogTailBytes
	if size-start < readSize {
		readSize = size - start
	}
	if readSize == 0 {
		return "", nil
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
