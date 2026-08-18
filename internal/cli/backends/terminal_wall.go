package backends

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// Leaf-side detection of a TERMINAL WALL: a banner the harness paints in place
// of work — an exhausted credit balance, an expired login, a spent usage window
// — and then sits on, forever.
//
// harness-wrapper models only two of these, and only at a turn's terminal
// point (chat.ReasonAuthRequired / chat.ReasonUsageLimited). A billing banner
// produces no turn transition at all, so both turn loops wait unbounded: the
// one-shot RunTurn has no deadline of its own and conversationTurnTimeout
// deliberately returns 0. The run then dies at the ~2700s output-timeout
// watchdog and is classified `timeout` or `no_work` — neither of which tells
// the operator that the ACCOUNT, not the agent, is what stopped.
//
// Two properties make this safe to act on:
//
//   - The phrase alone is never enough. An agent's own output can quote
//     "credit balance is too low" verbatim (an agent working on this very
//     change does). detectTerminalWall matches on text and is deliberately
//     permissive; wallWatcher is what makes the verdict trustworthy.
//   - QUIESCENCE is the safety property. A working agent writes continuously,
//     so the only way a wall phrase survives the settle window as TRAILING
//     content is that the harness really is parked on it.

type wallKind int

const (
	wallNone wallKind = iota
	wallBilling
	wallAuth
	wallUsageLimit
)

func (k wallKind) String() string {
	switch k {
	case wallBilling:
		return "billing"
	case wallAuth:
		return "auth"
	case wallUsageLimit:
		return "usage_limit"
	default:
		return "none"
	}
}

// Wall banners, in the order they are consulted. Interior whitespace runs are
// tolerated rather than requiring exact spacing, because these phrases arrive
// through a re-wrapping TUI.
//
// The auth set mirrors pkg/chat's claudeLoggedOutRE / claudeOnboardingRE
// intent; the usage-limit set mirrors its usage-limit wall, captured to end of
// line by the caller so a "· resets 10:20pm" tail rides along into the reason
// (that tail is what feeds agenterr's parseRetryAfter).
var (
	billingWallREs = []*regexp.Regexp{
		regexp.MustCompile(`(?i)credit\s+balance\s+is\s+too\s+low`),
		regexp.MustCompile(`(?i)out\s+of\s+credits`),
		regexp.MustCompile(`(?i)insufficient\s+credits`),
		regexp.MustCompile(`(?i)payment\s+required`),
		regexp.MustCompile(`(?i)\b402\b`),
		regexp.MustCompile(`(?i)upgrade\s+your\s+plan\s+(?:to\s+continue|to\s+keep)`),
		regexp.MustCompile(`(?i)billing\s+(?:issue|problem|error)`),
	}
	authWallREs = []*regexp.Regexp{
		regexp.MustCompile(`(?i)please\s+run\s+/login`),
		regexp.MustCompile(`(?i)not\s+logged\s+in`),
		regexp.MustCompile(`(?i)session\s+expired`),
		regexp.MustCompile(`(?i)sign\s+in\s+to\s+(?:claude|continue)`),
		regexp.MustCompile(`(?i)invalid\s+api\s+key`),
	}
	usageLimitWallREs = []*regexp.Regexp{
		regexp.MustCompile(`(?i)you(?:’|')?ve\s+hit\s+your\s+(?:session|usage)\s+limit`),
		regexp.MustCompile(`(?i)you\s+have\s+hit\s+your\s+(?:session|usage)\s+limit`),
	}
)

// detectTerminalWall reports which wall (if any) the text shows, plus the
// banner line it matched — used verbatim as the failure reason.
//
// Order is billing → auth → usage-limit, so a banner naming both credits and a
// limit reads as BILLING: that is the fatal, human-actionable one, and calling
// it a rate limit would hand it a blameless retry that can never succeed.
func detectTerminalWall(text string) (wallKind, string) {
	for _, probe := range []struct {
		kind wallKind
		res  []*regexp.Regexp
	}{
		{wallBilling, billingWallREs},
		{wallAuth, authWallREs},
		{wallUsageLimit, usageLimitWallREs},
	} {
		for _, re := range probe.res {
			if loc := re.FindStringIndex(text); loc != nil {
				return probe.kind, bannerLine(text, loc[0], loc[1])
			}
		}
	}
	return wallNone, ""
}

// bannerLine expands a match to the whole line that contains it, so the reason
// carries the banner as the harness painted it — including any trailing
// "· resets …" clause.
func bannerLine(text string, start, end int) string {
	lo := strings.LastIndexByte(text[:start], '\n') + 1
	hi := strings.IndexByte(text[end:], '\n')
	if hi < 0 {
		hi = len(text)
	} else {
		hi += end
	}
	return strings.TrimSpace(text[lo:hi])
}

var (
	ansiCSIRe   = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
	ansiOSCRe   = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	ansiOtherRe = regexp.MustCompile("\x1b[@-Z\\\\-_]")
)

// stripANSI renders a raw PTY byte stream down to the text a human would read:
// CSI sequences, OSC strings, other two-byte escapes and carriage returns are
// removed. Without it a banner interleaved with cursor moves and color codes
// never matches.
func stripANSI(b []byte) string {
	s := string(b)
	s = ansiOSCRe.ReplaceAllString(s, "")
	s = ansiCSIRe.ReplaceAllString(s, "")
	s = ansiOtherRe.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, "\r", "")
}

const (
	// wallTailBytes bounds the retained output. Only TRAILING content can be a
	// wall the harness is parked on, so the window need only be large enough to
	// hold a full screen redraw with its escape sequences.
	wallTailBytes = 16 << 10

	// wallCheckInterval is how often the watcher re-evaluates quiescence.
	wallCheckInterval = 5 * time.Second

	// defaultWallSettleSeconds is how long the wall must be the last thing the
	// harness said before the verdict is believed.
	defaultWallSettleSeconds = 45

	// envWallSettleSeconds overrides that window. 0 disables the detector
	// entirely, restoring the exact pre-change behavior.
	envWallSettleSeconds = "LOOM_TERMINAL_WALL_SETTLE_SECONDS"
)

// wallSettleWindow reads the settle window from the environment. A zero value
// is an explicit OFF SWITCH — no goroutine is started and the turn loops behave
// exactly as they did before this detector existed. Negative or unparseable
// values fall back to the default rather than erroring the run: a typo in an
// env var must not cost an agent its turn.
func wallSettleWindow() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envWallSettleSeconds))
	if raw == "" {
		return defaultWallSettleSeconds * time.Second
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return defaultWallSettleSeconds * time.Second
	}
	return time.Duration(secs) * time.Second
}

// wallWatcher decides, from a stream of harness output, whether the harness has
// gone quiet ON a wall banner. It never blocks or calls back into its caller;
// the only outward effect is canceling the turn context once, when it fires.
type wallWatcher struct {
	mu         sync.Mutex
	tail       []byte
	lastChange time.Time
	kind       wallKind
	line       string
	fired      bool
	settle     time.Duration
	now        func() time.Time
	cancel     context.CancelFunc

	lastGen uint64
	haveGen bool
}

func newWallWatcher(settle time.Duration, cancel context.CancelFunc) *wallWatcher {
	w := &wallWatcher{settle: settle, cancel: cancel, now: time.Now}
	w.lastChange = w.now()
	return w
}

// observe appends freshly written output and stamps the activity clock.
func (w *wallWatcher) observe(p []byte) {
	if w == nil || len(p) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tail = append(w.tail, p...)
	if len(w.tail) > wallTailBytes {
		w.tail = w.tail[len(w.tail)-wallTailBytes:]
	}
	w.lastChange = w.now()
}

// observeScreen feeds the rendered screen surface instead of a byte stream.
// The tail is replaced wholesale (the screen IS the tail), and the activity
// clock is stamped only when the generation moved — a repaint of identical
// content is not the harness doing work.
func (w *wallWatcher) observeScreen(text string, generation uint64) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tail = []byte(text)
	if len(w.tail) > wallTailBytes {
		w.tail = w.tail[len(w.tail)-wallTailBytes:]
	}
	if !w.haveGen || generation != w.lastGen {
		w.lastGen = generation
		w.haveGen = true
		w.lastChange = w.now()
	}
}

// check fires at most once: the tail must have been unchanged for the settle
// window AND still end on a wall banner. Returns whether this call fired.
func (w *wallWatcher) check() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fired || w.settle <= 0 {
		return false
	}
	if w.now().Sub(w.lastChange) < w.settle {
		return false
	}
	kind, line := detectTerminalWall(stripANSI(w.tail))
	if kind == wallNone {
		return false
	}
	w.kind, w.line, w.fired = kind, line, true
	if w.cancel != nil {
		w.cancel()
	}
	return true
}

// run polls check until ctx ends. It exits on the caller's deferred cancel, so
// it cannot outlive the turn it watches.
func (w *wallWatcher) run(ctx context.Context) {
	if w == nil {
		return
	}
	t := time.NewTicker(wallCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.check()
		}
	}
}

// result reads the verdict back after the turn returned.
func (w *wallWatcher) result() (wallKind, string, bool) {
	if w == nil {
		return wallNone, "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.kind, w.line, w.fired
}

// wallInvocationError is terminalTurnInvocationError's sibling for the case the
// harness did NOT name a reason: loom detected the wall itself, from the output
// the harness went quiet on. Same shape — marker + reason as Err, marker
// prepended to the evidence — so the outer classifier reads one contract.
//
// Returns nil for wallNone so callers can guard with a single nil check.
func wallInvocationError(kind wallKind, line, outputTail string) *InvocationError {
	var marker string
	switch kind {
	case wallBilling:
		marker = agenterr.BillingWallMarker
	case wallAuth:
		marker = agenterr.AuthRequiredMarker
	case wallUsageLimit:
		marker = agenterr.UsageLimitedMarker
	default:
		return nil
	}

	reason := strings.TrimSpace(line)
	if reason == "" {
		reason = "harness stopped on a " + kind.String() + " wall"
	}
	combined := marker + ": " + reason
	evidence := strings.TrimSpace(outputTail)
	if evidence == "" {
		evidence = combined
	} else if !strings.Contains(evidence, combined) {
		evidence = combined + "\n" + evidence
	}
	return &InvocationError{
		Err:        errors.New(combined),
		OutputTail: evidence,
		// The marker text is the signal the outer classifier reads; the exit
		// code only has to be non-zero. 1 keeps it uniform with the ordinary
		// errored-turn path.
		ExitCode: 1,
	}
}
