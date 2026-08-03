package backends

import (
	"fmt"
	"os"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/chat"
	"github.com/olesho/harness-wrapper/pkg/oneshot"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Role input policy: which harness prompts an agent may auto-answer, delivered
// by the supervisor as JSON in LOOM_ROLE_INPUT_POLICY and applied here as the
// harness-wrapper TurnConfig fields that actually resolve a prompt.
//
// Why loom does not simply adopt harness-wrapper's unattended default:
// pkg/oneshot's turnConfig ships InputPolicy{"trust_prompt": answer "proceed"}
// plus OnInputRequest = AutoAcceptAnswer, and AutoAcceptAnswer answers ANY
// prompt with its affirmative option, falling back to the FIRST option when it
// finds none. claude-code renders both the harmless folder-trust dialog and the
// `--dangerously-skip-permissions` acceptance screen under the same prompt
// kind, so adopting that default verbatim auto-accepts a skip-all-permissions
// launch — undoing the role safety knobs (allowed_tools / denied_tools /
// read_only) that were just made real. The role has to name the kinds it will
// auto-accept, and everything it did not name is denied.
//
// What the leaf does today WITHOUT any of this, which is the baseline this
// change improves on: invokeClaudeRunTurn passes neither InputPolicy nor
// OnInputRequest, so chat.Conversation.tryResolveInput resolves nothing and the
// request is SURFACED on Events(). Nothing in loom consumes that event, so a
// prompt raised before the first turn fails Send with chat.ErrInputPending
// (waitReadyForSend short-circuits on a surfaced request) and a prompt raised
// mid-turn stalls the turn until the context deadline — maybeIdleComplete
// refuses to complete a turn while a request is awaiting the client. So the
// status quo is a hard failure or a wedge, never an auto-accept. This change is
// therefore a wedge fix first and a guard second: it gives deny a real answer
// so the harness can move on, and makes allow an opt-in that a role must state.

// envRoleInputPolicy is the variable the supervisor exports the JSON-encoded
// role policy in. Absent means deny everything — the same state a nil policy
// means — so an older daemon that never sets it produces the restrictive
// behavior rather than the permissive one.
const envRoleInputPolicy = "LOOM_ROLE_INPUT_POLICY"

// resolveRoleInputPolicy reads the role's input policy off the environment.
// Every failure returns nil, which DispositionFor reads as deny-everything; the
// failure is reported on stderr so a malformed policy is diagnosable instead of
// looking like a role that deliberately denies.
func resolveRoleInputPolicy() *domain.RoleInputPolicy {
	policy, err := domain.DecodeRoleInputPolicy(os.Getenv(envRoleInputPolicy))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[loom] invalid %s (%v); denying every harness prompt\n", envRoleInputPolicy, err)
		return nil
	}
	return policy
}

// inputPolicyTurnFields builds the two harness-wrapper TurnConfig fields that
// decide an interactive prompt, from the role's policy.
//
// The returned chat.InputPolicy says "ask" for everything on purpose. In
// pkg/chat, an "ask" disposition makes policyOption return nil, which routes
// every request to OnInputRequest — so the single callback below owns the whole
// decision instead of it being split between a declarative map that cannot see
// the request's options and a callback that can. That split matters here:
// chat's own DispositionAnswer needs a fixed option ID chosen before the prompt
// exists, and DispositionDeny resolves silently, with no way to log that a role
// asking for "ask" got a deny instead.
//
// Passing the policy explicitly rather than leaving it nil is also the point at
// which loom states, in the config it hands the harness, that no prompt is
// pre-approved. Leaving it nil would behave identically today and read as an
// oversight the next time someone adds a default.
func inputPolicyTurnFields(policy *domain.RoleInputPolicy) (*chat.InputPolicy, func(chat.InputRequest) (chat.InputAnswer, bool)) {
	return &chat.InputPolicy{Default: chat.DispositionAsk}, func(req chat.InputRequest) (chat.InputAnswer, bool) {
		// Tell the daemon a prompt is outstanding for as long as this callback
		// owns it. pkg/chat invokes OnInputRequest synchronously and treats the
		// return value as the resolution, so the callback's duration IS the
		// window in which loom is holding an unanswered prompt — exactly the
		// window the supervisor's output-timeout watchdog must not read as a
		// hang, because the harness emits no PTY output while it is parked.
		//
		// Every disposition answers immediately today, so the window is
		// microseconds and the pair of IPC edges is nearly free. It is wired
		// here regardless because this is where an honored "ask" will block: the
		// moment this callback waits on a human, the daemon already knows not to
		// kill the agent, and knows to stop waiting if nobody ever answers.
		defer cli.BeginDaemonInputWait()()
		return answerInputRequest(policy, req)
	}
}

// answerInputRequest applies the role's disposition for one prompt.
//
// Declining (ok=false) is the fallback everywhere an answer cannot be
// constructed, and it is worth being precise about what it costs: pkg/chat
// treats an unresolved request as surfaced to the client, and loom has no
// client attached to Events(). A surfaced request blocks turn completion
// (maybeIdleComplete bails while one is pending) and fails the next Send with
// ErrInputPending, so the run stalls to the context deadline. That is a worse
// outcome than answering, which is exactly why deny prefers a real negative
// answer and only declines when the prompt offers no way to say no. It is
// never worse than today's behavior, where every prompt takes this path.
func answerInputRequest(policy *domain.RoleInputPolicy, req chat.InputRequest) (chat.InputAnswer, bool) {
	switch disposition := policy.DispositionFor(req.Kind); disposition {
	case domain.RoleInputAllow:
		// oneshot.AffirmativeOption, not a local matcher: the affirmative
		// vocabulary belongs to harness-wrapper and drifts with the harnesses.
		// Deliberately NOT oneshot.AutoAcceptAnswer — its fallback to the first
		// option is what turns "no obvious yes" into "press something", and
		// pressing something on an unrecognized permission screen is the defect
		// this whole field exists to prevent. An allowed kind with no
		// affirmative option declines instead, and says so.
		if opt := oneshot.AffirmativeOption(req); opt != nil {
			return chat.InputAnswer{OptionID: opt.ID}, true
		}
		fmt.Fprintf(os.Stderr,
			"[loom] input_policy: kind %q is allowed but the prompt offers no affirmative option; declining rather than guessing\n",
			req.Kind)
		return chat.InputAnswer{}, false

	case domain.RoleInputAsk:
		// "ask" hands the prompt to a person: publish it on the daemon's
		// pending-input registry and wait for `loom daemon answer` (or the web
		// UI) to resolve it. The surrounding BeginDaemonInputWait edges keep
		// the watchdog off for the duration, and the wait bounds itself below
		// the daemon's ceiling so an unanswered prompt ends in a clean deny
		// here rather than the watchdog's kill. Every no-answer path — no
		// daemon attached, timeout, superseded — degrades to deny and says so
		// on stderr, in the same voice read_only uses when it degrades.
		if answer, ok := awaitHumanAnswer(cli.HumanAnswerRequest{
			Kind:    req.Kind,
			Prompt:  req.Prompt,
			Options: ipcOptionsFromChat(req.Options),
		}); ok {
			if answer.Decline {
				fmt.Fprintf(os.Stderr, "[loom] input_policy: a human declined the %q prompt\n", req.Kind)
				return denyInputRequest(req)
			}
			if answer.OptionID != "" {
				if opt := optionByID(req, answer.OptionID); opt != nil {
					return chat.InputAnswer{OptionID: opt.ID}, true
				}
				fmt.Fprintf(os.Stderr,
					"[loom] input_policy: the human answer named option %q but the prompt no longer offers it; denying\n",
					answer.OptionID)
				return denyInputRequest(req)
			}
			if answer.Text != "" {
				return chat.InputAnswer{Text: answer.Text}, true
			}
		}
		return denyInputRequest(req)

	default:
		// Deny, and everything that is not a recognized disposition. The
		// default arm is deny rather than a lookup failure on purpose: a
		// disposition this build does not know about (a newer role definition,
		// a hand-edited value that slipped past validation) must land on the
		// restrictive side, never fall through to allow.
		return denyInputRequest(req)
	}
}

// awaitHumanAnswer is the seam between the policy decision and the daemon
// round trip, a variable so tests can stand in a scripted human.
var awaitHumanAnswer = cli.AwaitHumanAnswer

// ipcOptionsFromChat converts the harness's typed options to the wire shape
// the daemon shows an operator.
func ipcOptionsFromChat(opts []chat.InputOption) []cli.IPCInputOption {
	out := make([]cli.IPCInputOption, len(opts))
	for i, o := range opts {
		out[i] = cli.IPCInputOption{ID: o.ID, Label: o.Label}
	}
	return out
}

// optionByID returns the request option with the given ID, nil when absent.
func optionByID(req chat.InputRequest, id string) *chat.InputOption {
	for i := range req.Options {
		if req.Options[i].ID == id {
			return &req.Options[i]
		}
	}
	return nil
}

// denyInputRequest answers the prompt negatively when it offers a way to say
// no, and declines otherwise (see answerInputRequest for what declining costs).
func denyInputRequest(req chat.InputRequest) (chat.InputAnswer, bool) {
	if opt := negativeOption(req); opt != nil {
		return chat.InputAnswer{OptionID: opt.ID}, true
	}
	return chat.InputAnswer{}, false
}

// negativeOption picks the "no/decline/cancel" option from a request.
//
// The Alias == "deny" arm first because that is harness-wrapper's own contract:
// chat.DispositionDeny resolves a request by looking up exactly that alias, so
// any harness adapter that models a refusal has already tagged it. The label
// scan is the fallback for adapters that have not, and is kept narrow — the
// broad "contains" matching that is safe for an affirmative (guessing yes when
// the role said yes) is not safe in reverse, because a false positive here
// presses a button the role did not choose.
func negativeOption(req chat.InputRequest) *chat.InputOption {
	for i := range req.Options {
		if req.Options[i].Alias == "deny" {
			return &req.Options[i]
		}
	}
	for i := range req.Options {
		o := &req.Options[i]
		switch strings.ToLower(strings.TrimSpace(o.Label)) {
		case "no", "deny", "decline", "cancel", "reject":
			return o
		}
	}
	return nil
}
