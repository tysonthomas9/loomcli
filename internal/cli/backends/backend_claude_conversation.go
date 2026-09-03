package backends

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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

	turnID, err := conv.Send(ctx, prompt)
	if err != nil {
		return chat.Turn{}, wrapInvocationError(fmt.Errorf("send prompt: %w", err), "")
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
			return ev.Turn, true, conversationTurnError(ev.Turn)
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
func conversationTurnError(turn chat.Turn) error {
	reason := strings.TrimSpace(turn.Reason)
	if reason == "" {
		reason = "claude turn errored"
	}
	if ie := terminalTurnInvocationError(reason, turn.Text); ie != nil {
		return ie
	}
	return &InvocationError{Err: errors.New(reason), OutputTail: turn.Text, ExitCode: 1}
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
