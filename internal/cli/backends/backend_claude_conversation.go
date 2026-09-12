package backends

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/chat/memstore"
	"github.com/olesho/harness-wrapper/pkg/oneshot"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// The conversation executor: role `executor: conversation` runs the worker
// turn through a held chat.Conversation instead of the one-shot RunTurn.
//
// Same binary, same args, same input policy — the difference is who owns the
// conversation. RunTurn owns it internally, which forces every interactive
// prompt to resolve inside the OnInputRequest callback (where a human wait
// BLOCKS the event pump for its whole duration). Here loom owns it, so an
// "ask" surfaces on Events() the way pkg/chat intends, the human wait runs in
// its own goroutine, and the answer is delivered with Conversation.Answer
// while the pump keeps breathing. It is also the substrate follow-up turns
// need: the loop below sends one prompt today, and a bounded reprompt is a
// policy change inside it, not an architecture change.
//
// Resume rides Options.Resume (the adapter's own session-resume contract)
// with the same persisted harness session id the RunTurn path uses, so a
// conversation interrupted by a daemon restart reopens where it stopped.

// envRoleExecutor is exported by the supervisor from the role definition.
const envRoleExecutor = "LOOM_ROLE_EXECUTOR"

// RoleExecutorConversation is the executor value that selects this leaf.
const RoleExecutorConversation = "conversation"

// roleExecutor reads the executor the supervisor exported for this run.
// Anything other than the conversation vocabulary — absent, "turn", or a
// value this build does not know — selects the one-shot path, which is the
// conservative reading of an unknown executor.
func roleExecutor() string {
	return strings.TrimSpace(os.Getenv(envRoleExecutor))
}

// conversationTurnTimeout bounds a single assistant turn inside the
// conversation, mirroring the outer context deadline discipline of the
// one-shot path. Zero (unset) means the run's own context is the only bound.
func conversationTurnTimeout() time.Duration {
	// The daemon's run-duration cap (#316) is the real ceiling; a per-turn
	// bound here would double-configure it. Kept as a function so the knob
	// has one obvious home when a per-turn policy is wanted.
	//
	// DO NOT make this non-zero without wiring the marker in first.
	// runConversationTurn hands the expiry to wrapInvocationError(ctx.Err(), ""),
	// which produces a bare "context deadline exceeded" — the residual pattern
	// table then classifies loom's own clean stop as a network "connection
	// timeout", the exact bug fixed on the one-shot path. The fix to copy is
	// runTurnDeadlineInvocationError (invocation_error.go), applied only when
	// errors.Is on the DERIVED context reports context.DeadlineExceeded. While
	// this returns 0 no derived context exists and the path cannot reproduce it.
	return 0
}

// runClaudeConversation is the executor=conversation counterpart of
// defaultClaudeNonInteractiveInvoker's RunTurn call.
func runClaudeConversation(ctx context.Context, workDir, prompt, agentName, resumeID string, collector *usage.Collector) error {
	store := memstore.New()
	policy := resolveRoleInputPolicy()

	opts := chat.Options{
		Harness:    "claude-code",
		BinaryPath: "claude",
		Args:       buildClaudeRunTurnArgs(""), // resume rides Options.Resume, never the args
		Resume:     resumeID,
		WorkingDir: workDir,
		Env:        buildClaudeEnv(workDir, agentName),
		Model:      resolveAgentModel(),
		Store:      store,
		// Deny-by-default is the same posture as the one-shot path: the
		// policy defers every kind to the resolver, the resolver answers
		// allow/deny promptly, and ask is NOT answered here — it surfaces on
		// Events() where the human wait belongs.
		InputPolicy:    &chat.InputPolicy{Default: chat.DispositionAsk},
		OnInputRequest: conversationInputResolver(policy),
		// Headless run: a wedged codex update menu has no one to click it.
		// Inert for claude; stated for the day this leaf goes multi-harness.
		AutoSkipCodexUpdateNotice: true,
	}

	conv, err := chat.Open(ctx, opts)
	if err != nil {
		return wrapInvocationError(fmt.Errorf("open conversation: %w", err), "")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conv.Close(closeCtx)
	}()

	release, err := conv.AcquireControl(ctx)
	if err != nil {
		return wrapInvocationError(fmt.Errorf("acquire conversation control: %w", err), "")
	}
	defer release()

	turn, err := runConversationTurn(ctx, conv, prompt)

	// Session id and usage are read the same way as the one-shot path —
	// best-effort, never failing the run — so resume and accounting behave
	// identically under either executor.
	if sid := conversationHarnessSessionID(store, conv); sid != "" {
		SetLastCapturedSessionID(sid)
		if lockErr := cli.UpdateLockClaudeSessionID(workDir, sid); lockErr != nil {
			fmt.Fprintf(os.Stderr, "[loom] conversation: could not persist session id for resume: %v\n", lockErr)
		}
		accumulateHarnessUsage(collector, "claude", sid, workDir)
	}

	if err != nil {
		return err
	}
	displayConversationTurn(turn)
	return nil
}

// runConversationTurn sends one prompt and drives the event loop to the
// turn's terminal state. It is deliberately shaped as "one bounded turn" so a
// follow-up policy (reprompt on an unfinished verdict, a critic round inside
// the same session) is a loop around this call.
func runConversationTurn(ctx context.Context, conv *chat.Conversation, prompt string) (chat.Turn, error) {
	if t := conversationTurnTimeout(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}

	// The turn's own cancel scope, so anything this call starts unwinds when
	// it returns.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	ctx = turnCtx

	turnID, err := sendConversationPrompt(ctx, conv, prompt)
	if err != nil {
		return chat.Turn{}, err
	}

	events := conv.Events()
	for {
		select {
		case <-ctx.Done():
			return chat.Turn{}, wrapInvocationError(ctx.Err(), "")
		case ev, ok := <-events:
			if !ok {
				return chat.Turn{}, wrapInvocationError(errors.New("conversation ended before the turn completed"), "")
			}
			if turn, done, err := handleConversationEvent(ctx, conv, ev, turnID); done {
				return turn, err
			}
		}
	}
}

// sendConversationPrompt sends the prompt and maps a send-time failure onto the
// invocation-error taxonomy.
//
// The auth arm is UNREACHABLE on harness-wrapper@v0.7.7: Send catches its own
// ErrAuthRequired at pkg/chat/send.go:45-56 and emits a terminal assistant turn
// instead of returning the sentinel, so the auth verdict always arrives on the
// errored-turn path in runConversationTurn. It exists so a future wrapper that
// DOES propagate the sentinel degrades to a marked AuthFailure carrying the
// screen, rather than silently to Unknown — and it is pinned by
// TestConversationSend_AuthSurfacesAsErroredTurn, which fails first if a bump
// ever flips that behavior.
func sendConversationPrompt(ctx context.Context, conv *chat.Conversation, prompt string) (string, error) {
	turnID, err := conv.Send(ctx, prompt)
	if err == nil {
		return turnID, nil
	}
	if ie := authSentinelInvocationError(err, conv); ie != nil {
		return "", ie
	}
	return "", wrapInvocationError(fmt.Errorf("send prompt: %w", err), "")
}

// handleConversationEvent applies one event. done=false means the turn is
// still running and the pump should keep breathing.
func handleConversationEvent(ctx context.Context, conv *chat.Conversation, ev chat.ConversationEvent, turnID string) (chat.Turn, bool, error) {
	switch ev.Type {
	case chat.EventInputRequest:
		if ev.Input != nil {
			// The human wait runs beside the pump, not inside it.
			go answerSurfacedRequest(ctx, conv, *ev.Input)
		}
	case chat.EventTurn:
		if ev.Turn.Role != chat.RoleAssistant {
			return chat.Turn{}, false, nil
		}
		switch ev.Turn.State {
		case chat.TurnStateComplete:
			if ev.Turn.ID == turnID || turnID == "" {
				return ev.Turn, true, nil
			}
		case chat.TurnStateErrored:
			return ev.Turn, true, conversationTurnError(conv, ev.Turn)
		}
	}
	return chat.Turn{}, false, nil
}

// answerSurfacedRequest resolves one surfaced prompt: hand it to a human via
// the daemon (the same pending-input registry the one-shot path uses), and
// deliver the decision with Conversation.Answer. The role input policy has
// already run in conversationInputResolver — a request only surfaces when its
// disposition is ask — so no policy re-check happens here. Every no-answer path falls
// back to the request's own negative option; a prompt with no way to say no
// is left surfaced, and the daemon-side wait bound is what ends the stall.
func answerSurfacedRequest(ctx context.Context, conv *chat.Conversation, req chat.InputRequest) {
	done := cli.BeginDaemonInputWait()
	defer done()

	answer, ok := awaitHumanAnswer(cli.HumanAnswerRequest{
		Kind:    req.Kind,
		Prompt:  req.Prompt,
		Options: ipcOptionsFromChat(req.Options),
	})

	resolved, deliverable := chooseSurfacedAnswer(req, answer, ok)
	if !deliverable {
		fmt.Fprintf(os.Stderr, "[loom] conversation: no human answered the %q prompt and it offers no negative option; leaving it surfaced\n", req.Kind)
		return
	}
	if err := conv.Answer(ctx, req.ID, resolved); err != nil {
		fmt.Fprintf(os.Stderr, "[loom] conversation: could not deliver the answer for %q: %v\n", req.Kind, err)
	}
}

// chooseSurfacedAnswer turns the human's decision (or their absence) into the
// answer to deliver. A stale option id and every no-answer outcome fall back
// to the request's own negative option; deliverable=false means the request
// offers no way to say no, so nothing can be delivered honestly.
func chooseSurfacedAnswer(req chat.InputRequest, answer cli.IPCInputAnswer, ok bool) (chat.InputAnswer, bool) {
	if ok && !answer.Decline {
		if answer.OptionID != "" {
			if opt := optionByID(chat.InputRequest{Options: req.Options}, answer.OptionID); opt != nil {
				return chat.InputAnswer{OptionID: opt.ID}, true
			}
			fmt.Fprintf(os.Stderr, "[loom] conversation: the human answer named option %q but the prompt no longer offers it; denying\n", answer.OptionID)
		} else if answer.Text != "" {
			return chat.InputAnswer{Text: answer.Text}, true
		}
	}
	if opt := negativeOption(req); opt != nil {
		return chat.InputAnswer{OptionID: opt.ID}, true
	}
	return chat.InputAnswer{}, false
}

// conversationInputResolver answers allow/deny promptly on the pump and
// surfaces everything else. It is answerInputRequest minus the blocking ask
// branch: pkg/chat's OnInputRequest contract is "return promptly", and the
// ask wait belongs on Events(), which is exactly where ok=false sends it.
func conversationInputResolver(policy *domain.RoleInputPolicy) func(chat.InputRequest) (chat.InputAnswer, bool) {
	return func(req chat.InputRequest) (chat.InputAnswer, bool) {
		switch policy.DispositionFor(req.Kind) {
		case domain.RoleInputAllow:
			if opt := oneshot.AffirmativeOption(req); opt != nil {
				return chat.InputAnswer{OptionID: opt.ID}, true
			}
			fmt.Fprintf(os.Stderr,
				"[loom] input_policy: kind %q is allowed but the prompt offers no affirmative option; declining rather than guessing\n",
				req.Kind)
			return chat.InputAnswer{}, false
		case domain.RoleInputAsk:
			return chat.InputAnswer{}, false // surface on Events(); the human wait happens there
		default:
			return denyInputRequest(req)
		}
	}
}

// conversationTurnError maps an errored turn into the invocation-error
// taxonomy, carrying the harness's own terminal verdict when it named one —
// the same mapping the one-shot path applies to ErrTurnErrored.
//
// The conversation is taken as an argument for its SCREEN. On a terminal
// auth/usage turn the wrapper hands us a Reason and nothing else: every
// producer of chat.ReasonAuthRequired on the pinned v0.7.7 leaves Turn.Text
// empty (emitAuthRequiredTurn never sets it; authRelabel blanks it), so the
// classifier downstream would see the marker over an empty window and record
// Screen.Scanned=false on exactly the verdict that description exists for.
// Appending the live screen widens that window; it changes no class, no
// disposition and no restart decision.
func conversationTurnError(conv *chat.Conversation, turn chat.Turn) error {
	reason := strings.TrimSpace(turn.Reason)
	if reason == "" {
		reason = "claude turn errored"
	}
	if ie := terminalTurnInvocationError(reason, joinEvidence(turn.Text, screenEvidence(conv))); ie != nil {
		return ie
	}
	return &InvocationError{Err: errors.New(reason), OutputTail: turn.Text, ExitCode: 1}
}

// authSentinelInvocationError maps a propagated chat.ErrAuthRequired onto the
// same marked InvocationError the errored-turn path produces, carrying the
// screen as its evidence. Returns nil for every other error so the caller
// falls through to its ordinary wrapping with a single nil check.
func authSentinelInvocationError(err error, conv *chat.Conversation) error {
	if !errors.Is(err, chat.ErrAuthRequired) {
		return nil
	}
	if ie := terminalTurnInvocationError(chat.ReasonAuthRequired, screenEvidence(conv)); ie != nil {
		return ie
	}
	return nil
}

// conversationScreenTailCap bounds the screen text carried into an
// InvocationError. A rendered screen is a few KiB at most; the cap is there so
// a pathological emulator state cannot push an unbounded blob into an error
// that gets logged, stored in a state file and shipped as an event.
const conversationScreenTailCap = 4 << 10

// conversationScreenText reads the live screen behind a conversation. It is a
// package var because chat.Conversation's screen is unexported and only
// chat.Open can populate it, so a test that needs a specific screen behind
// conversationTurnError has no other seam. The nil-conversation guard lives
// here rather than in screenEvidence so a substituted reader owns the whole
// decision.
var conversationScreenText = func(conv *chat.Conversation) string {
	if conv == nil {
		return ""
	}
	return conv.ScreenSnapshot().Text
}

// screenEvidence returns the trailing, bounded screen text for a conversation,
// or "" when there is no conversation to read (the two direct-Turn call sites
// in the tests, and any future caller holding only a turn).
func screenEvidence(conv *chat.Conversation) string {
	return tailBytes(strings.TrimSpace(conversationScreenText(conv)), conversationScreenTailCap)
}

// joinEvidence concatenates the non-empty evidence fragments with a newline,
// preserving their order and skipping a fragment already contained in what
// came before it.
func joinEvidence(parts ...string) string {
	var kept []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(kept) > 0 && strings.Contains(strings.Join(kept, "\n"), p) {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, "\n")
}

// tailBytes keeps the last max bytes of s, cut forward to the next rune
// boundary so a truncated multi-byte glyph never reaches a log or an event.
func tailBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := s[len(s)-max:]
	for len(cut) > 0 && !utf8.ValidString(cut[:1]) {
		cut = cut[1:]
	}
	return cut
}

// conversationHarnessSessionID reads the harness-level session id off the
// chat store (the chat-level id is a different namespace).
func conversationHarnessSessionID(store chat.Store, conv *chat.Conversation) string {
	sess, err := store.GetSession(context.Background(), conv.SessionID())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(sess.HarnessSessionID)
}

// displayConversationTurn prints the assistant's final reply, mirroring
// displayClaudeTurn's role in the one-shot path.
func displayConversationTurn(turn chat.Turn) {
	text := strings.TrimSpace(turn.Text)
	if text == "" {
		return
	}
	fmt.Println(text)
}
