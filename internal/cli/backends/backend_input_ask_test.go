package backends

import (
	"testing"

	"github.com/olesho/harness-wrapper/pkg/chat"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// scriptedHuman swaps the daemon round trip for a canned decision and
// restores the real one afterwards.
func scriptedHuman(t *testing.T, answer cli.IPCInputAnswer, ok bool) *cli.HumanAnswerRequest {
	t.Helper()
	var captured cli.HumanAnswerRequest
	old := awaitHumanAnswer
	awaitHumanAnswer = func(req cli.HumanAnswerRequest) (cli.IPCInputAnswer, bool) {
		captured = req
		return answer, ok
	}
	t.Cleanup(func() { awaitHumanAnswer = old })
	return &captured
}

func askPolicy() *domain.RoleInputPolicy {
	return &domain.RoleInputPolicy{Kinds: map[string]string{"trust_prompt": domain.RoleInputAsk}}
}

func trustReq() chat.InputRequest {
	return chat.InputRequest{
		Kind:   "trust_prompt",
		Prompt: "Do you trust the files in this folder?",
		Options: []chat.InputOption{
			{ID: "1", Label: "Yes, proceed"},
			{ID: "2", Label: "No, exit", Alias: "deny"},
		},
	}
}

// "ask" with a human attached: the person's option is the answer — including
// the affirmative one the policy alone would never press.
func TestAsk_HumanOptionAnswerIsHonored(t *testing.T) {
	captured := scriptedHuman(t, cli.IPCInputAnswer{OptionID: "1"}, true)

	ans, ok := answerInputRequest(askPolicy(), trustReq())
	if !ok || ans.OptionID != "1" {
		t.Fatalf("answer = (%+v, %v), want the human's option 1", ans, ok)
	}
	if captured.Kind != "trust_prompt" || len(captured.Options) != 2 {
		t.Fatalf("the human saw %+v, want the full prompt with both options", *captured)
	}
}

func TestAsk_HumanTextAnswerIsHonored(t *testing.T) {
	scriptedHuman(t, cli.IPCInputAnswer{Text: "use the staging bucket"}, true)

	ans, ok := answerInputRequest(askPolicy(), trustReq())
	if !ok || ans.Text != "use the staging bucket" {
		t.Fatalf("answer = (%+v, %v), want the human's text", ans, ok)
	}
}

// A human decline and a nobody-answered timeout both land on deny — the
// request's own negative option, never a guess.
func TestAsk_DeclineAndTimeoutBothDeny(t *testing.T) {
	for name, script := range map[string]struct {
		answer cli.IPCInputAnswer
		ok     bool
	}{
		"human declined":  {cli.IPCInputAnswer{Decline: true}, true},
		"nobody answered": {cli.IPCInputAnswer{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			scriptedHuman(t, script.answer, script.ok)
			ans, ok := answerInputRequest(askPolicy(), trustReq())
			if !ok || ans.OptionID != "2" {
				t.Fatalf("answer = (%+v, %v), want the deny-aliased option 2", ans, ok)
			}
		})
	}
}

// An answer naming an option the prompt no longer offers must deny, not press
// the nearest button: the prompt may have changed between the human's read and
// their click.
func TestAsk_StaleOptionDenies(t *testing.T) {
	scriptedHuman(t, cli.IPCInputAnswer{OptionID: "9"}, true)

	ans, ok := answerInputRequest(askPolicy(), trustReq())
	if !ok || ans.OptionID != "2" {
		t.Fatalf("answer = (%+v, %v), want deny for a stale option id", ans, ok)
	}
}
