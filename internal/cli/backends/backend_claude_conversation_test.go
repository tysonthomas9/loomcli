package backends

import (
	"errors"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/chat"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func surfacedTrustReq() chat.InputRequest {
	return chat.InputRequest{
		ID:     "in-1",
		Kind:   "trust_prompt",
		Prompt: "Do you trust the files in this folder?",
		Options: []chat.InputOption{
			{ID: "1", Label: "Yes, proceed"},
			{ID: "2", Label: "No, exit", Alias: "deny"},
		},
	}
}

// The pump-side resolver must answer allow/deny promptly and surface ask —
// never block, never guess.
func TestConversationInputResolver(t *testing.T) {
	req := surfacedTrustReq()

	t.Run("allow answers the affirmative option", func(t *testing.T) {
		policy := &domain.RoleInputPolicy{Kinds: map[string]string{"trust_prompt": domain.RoleInputAllow}}
		ans, ok := conversationInputResolver(policy)(req)
		if !ok || ans.OptionID != "1" {
			t.Fatalf("answer = (%+v, %v), want the affirmative option", ans, ok)
		}
	})

	t.Run("ask surfaces instead of answering", func(t *testing.T) {
		policy := &domain.RoleInputPolicy{Kinds: map[string]string{"trust_prompt": domain.RoleInputAsk}}
		if ans, ok := conversationInputResolver(policy)(req); ok {
			t.Fatalf("ask must surface on Events, got an inline answer %+v", ans)
		}
	})

	t.Run("nil policy denies via the negative option", func(t *testing.T) {
		ans, ok := conversationInputResolver(nil)(req)
		if !ok || ans.OptionID != "2" {
			t.Fatalf("answer = (%+v, %v), want the deny-aliased option", ans, ok)
		}
	})
}

// chooseSurfacedAnswer is the human-decision mapping for surfaced prompts:
// honor a live option or text, deny on decline/timeout/stale, and report
// undeliverable only when the prompt has no negative option at all.
func TestChooseSurfacedAnswer(t *testing.T) {
	req := surfacedTrustReq()

	cases := []struct {
		name        string
		answer      cli.IPCInputAnswer
		ok          bool
		want        chat.InputAnswer
		deliverable bool
	}{
		{"human option", cli.IPCInputAnswer{OptionID: "1"}, true, chat.InputAnswer{OptionID: "1"}, true},
		{"human text", cli.IPCInputAnswer{Text: "use staging"}, true, chat.InputAnswer{Text: "use staging"}, true},
		{"human decline", cli.IPCInputAnswer{Decline: true}, true, chat.InputAnswer{OptionID: "2"}, true},
		{"nobody answered", cli.IPCInputAnswer{}, false, chat.InputAnswer{OptionID: "2"}, true},
		{"stale option id", cli.IPCInputAnswer{OptionID: "9"}, true, chat.InputAnswer{OptionID: "2"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, deliverable := chooseSurfacedAnswer(req, tc.answer, tc.ok)
			if deliverable != tc.deliverable || got.OptionID != tc.want.OptionID || got.Text != tc.want.Text {
				t.Fatalf("chooseSurfacedAnswer = (%+v, %v), want (%+v, %v)", got, deliverable, tc.want, tc.deliverable)
			}
		})
	}

	t.Run("no negative option means nothing deliverable", func(t *testing.T) {
		bare := chat.InputRequest{Kind: "trust_prompt", Options: []chat.InputOption{{ID: "1", Label: "Yes"}}}
		if got, deliverable := chooseSurfacedAnswer(bare, cli.IPCInputAnswer{}, false); deliverable {
			t.Fatalf("want undeliverable for a prompt with no way to say no, got %+v", got)
		}
	})
}

// An errored turn carries the harness's own verdict out as the same typed
// invocation error the one-shot path produces — auth and quota must not read
// as generic failures that burn the restart budget.
func TestConversationTurnError_CarriesTerminalVerdicts(t *testing.T) {
	authErr := conversationTurnError(chat.Turn{Reason: chat.ReasonAuthRequired})
	var ie *InvocationError
	if !errors.As(authErr, &ie) {
		t.Fatalf("auth reason must map to an InvocationError, got %T", authErr)
	}
	if !errors.As(conversationTurnError(chat.Turn{Reason: "something broke"}), &ie) {
		t.Fatalf("generic reason must still be an InvocationError")
	}
}

// The executor fork reads the supervisor-exported env; anything but the
// conversation vocabulary must select the one-shot path.
func TestRoleExecutor_ReadsEnv(t *testing.T) {
	t.Setenv(envRoleExecutor, "")
	if got := roleExecutor(); got != "" {
		t.Fatalf("empty env → %q, want empty (one-shot)", got)
	}
	t.Setenv(envRoleExecutor, " conversation ")
	if got := roleExecutor(); got != RoleExecutorConversation {
		t.Fatalf("got %q, want %q", got, RoleExecutorConversation)
	}
}
