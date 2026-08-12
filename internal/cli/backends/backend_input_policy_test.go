package backends

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/chat"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// claudeTrustPrompt is claude-code's folder-trust dialog as pkg/chat presents
// it. The option labels are the ones AffirmativeOption matches on ("proceed"),
// which is exactly why a blanket auto-accept is dangerous: claude-code renders
// the `--dangerously-skip-permissions` acceptance screen under this SAME kind,
// with an equally affirmative-looking option.
func claudeTrustPrompt() chat.InputRequest {
	return chat.InputRequest{
		ID:   "req-1",
		Kind: "trust_prompt",
		Options: []chat.InputOption{
			{ID: "1", Alias: "proceed", Label: "Yes, proceed"},
			{ID: "2", Alias: "deny", Label: "No, exit"},
		},
	}
}

// The headline guarantee of this whole change. harness-wrapper's unattended
// default (oneshot.AutoAcceptAnswer) answers this prompt with "Yes, proceed",
// and under that default a skip-all-permissions screen is accepted with nobody
// deciding to. loom must NOT do that unless the role explicitly named the kind.
func TestAnswerInputRequest_TrustPromptIsNotAutoAcceptedWithoutAnExplicitAllow(t *testing.T) {
	req := claudeTrustPrompt()

	notAllowed := []struct {
		name   string
		policy *domain.RoleInputPolicy
	}{
		{name: "no policy at all", policy: nil},
		{name: "zero-value policy", policy: &domain.RoleInputPolicy{}},
		{name: "explicit deny default", policy: &domain.RoleInputPolicy{Default: domain.RoleInputDeny}},
		{name: "empty entry for the kind", policy: &domain.RoleInputPolicy{Kinds: map[string]string{"trust_prompt": ""}}},
		{name: "explicit deny for the kind", policy: &domain.RoleInputPolicy{Kinds: map[string]string{"trust_prompt": domain.RoleInputDeny}}},
		{name: "ask for the kind", policy: &domain.RoleInputPolicy{Kinds: map[string]string{"trust_prompt": domain.RoleInputAsk}}},
		{
			name:   "a DIFFERENT kind allowed",
			policy: &domain.RoleInputPolicy{Kinds: map[string]string{"confirm": domain.RoleInputAllow}},
		},
	}

	for _, tt := range notAllowed {
		t.Run(tt.name, func(t *testing.T) {
			ans, ok := withStderr(t, func() (chat.InputAnswer, bool) {
				return answerInputRequest(tt.policy, req)
			})
			if !ok {
				t.Fatalf("want the negative option answered, got a decline")
			}
			if ans.OptionID == "1" {
				t.Fatalf("AUTO-ACCEPTED the trust prompt (option %q) with policy %+v — this is the skip-all-permissions bypass", ans.OptionID, tt.policy)
			}
			if ans.OptionID != "2" {
				t.Fatalf("answer = %q, want the deny-aliased option %q", ans.OptionID, "2")
			}
		})
	}

	// ...and it IS accepted when — and only when — the role says so.
	for _, allowing := range []*domain.RoleInputPolicy{
		{Kinds: map[string]string{"trust_prompt": domain.RoleInputAllow}},
		{Default: domain.RoleInputAllow},
	} {
		ans, ok := answerInputRequest(allowing, req)
		if !ok || ans.OptionID != "1" {
			t.Fatalf("policy %+v: answer = (%+v, %v), want the affirmative option %q", allowing, ans, ok, "1")
		}
	}
}

// Each disposition against a request that HAS a matching option and one that
// does not. The no-matching-option arm is where a lazy implementation reaches
// for "just press the first thing", which is the behavior being kept out.
func TestAnswerInputRequest_DispositionsWithAndWithoutAMatchingOption(t *testing.T) {
	both := []chat.InputOption{
		{ID: "y", Alias: "proceed", Label: "Yes"},
		{ID: "n", Alias: "deny", Label: "No"},
	}
	// A menu with neither an affirmative nor a negative option — e.g. a
	// harness picker loom has never seen.
	neither := []chat.InputOption{
		{ID: "a", Label: "Option A"},
		{ID: "b", Label: "Option B"},
	}

	tests := []struct {
		name        string
		disposition string
		options     []chat.InputOption
		wantOption  string
		wantOK      bool
	}{
		{name: "allow with an affirmative", disposition: domain.RoleInputAllow, options: both, wantOption: "y", wantOK: true},
		{name: "allow without an affirmative", disposition: domain.RoleInputAllow, options: neither, wantOK: false},
		{name: "deny with a negative", disposition: domain.RoleInputDeny, options: both, wantOption: "n", wantOK: true},
		{name: "deny without a negative", disposition: domain.RoleInputDeny, options: neither, wantOK: false},
		{name: "ask with a negative", disposition: domain.RoleInputAsk, options: both, wantOption: "n", wantOK: true},
		{name: "ask without a negative", disposition: domain.RoleInputAsk, options: neither, wantOK: false},
		{name: "free-text prompt cannot be answered", disposition: domain.RoleInputDeny, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &domain.RoleInputPolicy{Kinds: map[string]string{"k": tt.disposition}}
			req := chat.InputRequest{Kind: "k", Options: tt.options}
			ans, ok := withStderr(t, func() (chat.InputAnswer, bool) {
				return answerInputRequest(policy, req)
			})
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (answer %+v)", ok, tt.wantOK, ans)
			}
			if ok && ans.OptionID != tt.wantOption {
				t.Fatalf("answer = %q, want %q", ans.OptionID, tt.wantOption)
			}
			if !ok && ans.OptionID != "" {
				t.Fatalf("a decline must carry no option, got %q", ans.OptionID)
			}
		})
	}
}

// "ask" has no human wired to it yet. It must deny AND say so with the kind
// named — a silent degrade would let an operator believe a prompt reached
// someone, and treating it as allow would be the permissive reading of an
// unfinished feature.
func TestAnswerInputRequest_AskDeniesAndLogsTheKind(t *testing.T) {
	policy := &domain.RoleInputPolicy{Kinds: map[string]string{"bypass_permissions": domain.RoleInputAsk}}
	req := chat.InputRequest{
		Kind:    "bypass_permissions",
		Options: []chat.InputOption{{ID: "y", Alias: "proceed", Label: "Yes"}, {ID: "n", Alias: "deny", Label: "No"}},
	}

	var ans chat.InputAnswer
	var ok bool
	logged := captureStderr(t, func() { ans, ok = answerInputRequest(policy, req) })

	if !ok || ans.OptionID != "n" {
		t.Fatalf("ask must behave as deny: answer = (%+v, %v), want the negative option", ans, ok)
	}
	if !strings.Contains(logged, "bypass_permissions") {
		t.Errorf("log %q must name the kind that was degraded", logged)
	}
	if !strings.Contains(logged, "ask") || !strings.Contains(strings.ToLower(logged), "deny") {
		t.Errorf("log %q must say that an \"ask\" was denied", logged)
	}
}

// An unknown disposition (a newer role definition, a hand-edited value) must
// land on deny, never fall through to allow.
func TestAnswerInputRequest_UnknownDispositionDenies(t *testing.T) {
	policy := &domain.RoleInputPolicy{Kinds: map[string]string{"trust_prompt": "yolo"}}
	ans, ok := answerInputRequest(policy, claudeTrustPrompt())
	if !ok || ans.OptionID != "2" {
		t.Fatalf("answer = (%+v, %v), want the deny-aliased option", ans, ok)
	}
}

func TestNegativeOption_PrefersTheHarnessAliasOverALabelGuess(t *testing.T) {
	req := chat.InputRequest{Options: []chat.InputOption{
		{ID: "1", Label: "No"},
		{ID: "2", Alias: "deny", Label: "Exit and do not trust"},
	}}
	if got := negativeOption(req); got == nil || got.ID != "2" {
		t.Fatalf("negativeOption = %+v, want the alias-tagged option", got)
	}
	// Nothing negative-looking: no guess.
	if got := negativeOption(chat.InputRequest{Options: []chat.InputOption{{ID: "a", Label: "Continue anyway"}}}); got != nil {
		t.Fatalf("negativeOption = %+v, want nil rather than a guess", got)
	}
}

// The env hop as the leaf sees it: absent and malformed both resolve to the
// deny-everything nil policy, so an agent spawned by an older daemon (or with a
// corrupted variable) auto-answers nothing rather than everything.
func TestResolveRoleInputPolicy_FailsClosedOnAbsentAndMalformedEnv(t *testing.T) {
	for _, tt := range []struct{ name, value string }{
		{name: "absent", value: ""},
		{name: "not json", value: "trust_prompt=allow"},
		{name: "truncated", value: `{"kinds":{"trust_prompt":`},
		{name: "outside the vocabulary", value: `{"kinds":{"trust_prompt":"yes"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envRoleInputPolicy, tt.value)
			var policy *domain.RoleInputPolicy
			captureStderr(t, func() { policy = resolveRoleInputPolicy() })
			if policy != nil {
				t.Fatalf("resolveRoleInputPolicy() = %+v, want nil", policy)
			}
			ans, ok := answerInputRequest(policy, claudeTrustPrompt())
			if !ok || ans.OptionID != "2" {
				t.Fatalf("a %s policy answered %+v, want the trust prompt denied", tt.name, ans)
			}
		})
	}
}

func TestResolveRoleInputPolicy_ReadsAWellFormedEnv(t *testing.T) {
	t.Setenv(envRoleInputPolicy, `{"default":"deny","kinds":{"trust_prompt":"allow"}}`)
	policy := resolveRoleInputPolicy()
	if policy == nil {
		t.Fatal("resolveRoleInputPolicy() = nil, want the decoded policy")
	}
	if got := policy.DispositionFor("trust_prompt"); got != domain.RoleInputAllow {
		t.Errorf("DispositionFor(trust_prompt) = %q, want allow", got)
	}
	if got := policy.DispositionFor("anything_else"); got != domain.RoleInputDeny {
		t.Errorf("DispositionFor(anything_else) = %q, want deny", got)
	}
}

// The chat.InputPolicy loom hands the harness must not pre-approve anything on
// its own: every request has to reach the callback, which is the only place the
// role's policy is consulted.
func TestInputPolicyTurnFields_PolicyDefersEveryKindToTheCallback(t *testing.T) {
	policy, callback := inputPolicyTurnFields(nil)
	if policy == nil {
		t.Fatal("want an explicit chat.InputPolicy, not nil")
	}
	if policy.Default != chat.DispositionAsk {
		t.Fatalf("chat policy Default = %q, want %q so policyOption defers to the callback", policy.Default, chat.DispositionAsk)
	}
	if len(policy.ByKind) != 0 {
		t.Fatalf("chat policy ByKind = %+v, want empty — a pre-approved kind bypasses the role policy", policy.ByKind)
	}
	if callback == nil {
		t.Fatal("want an OnInputRequest callback")
	}
}

// The join: invokeClaudeRunTurn must actually put the two fields on the
// TurnConfig. Without this the whole policy is inert, which is the defect the
// safety-knob work removed once already.
func TestInvokeClaudeRunTurn_CarriesTheInputPolicyFields(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv(envRoleInputPolicy, `{"kinds":{"trust_prompt":"allow"}}`)

	var captured claudeRunTurnConfig
	installClaudeRunTurnMock(t, func(_ context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		captured = cfg
		return completedClaudeTurn("done"), nil
	})

	if _, err := invokeClaudeRunTurn(context.Background(), "/work", "do it", "falcon", "", nil, nil); err != nil {
		t.Fatalf("invokeClaudeRunTurn: %v", err)
	}
	if captured.InputPolicy == nil {
		t.Fatal("TurnConfig.InputPolicy = nil, want the explicit defer-to-callback policy")
	}
	if captured.OnInputRequest == nil {
		t.Fatal("TurnConfig.OnInputRequest = nil, want the role-policy resolver")
	}
	// The callback must be wired to the ROLE's policy, not to a fresh default.
	ans, ok := captured.OnInputRequest(claudeTrustPrompt())
	if !ok || ans.OptionID != "1" {
		t.Fatalf("callback answered %+v (ok=%v), want the affirmative option the role allowed", ans, ok)
	}
}

// ...and with no role policy the same wiring denies the same prompt.
func TestInvokeClaudeRunTurn_NoRolePolicyDeniesTheTrustPrompt(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv(envRoleInputPolicy, "")

	var captured claudeRunTurnConfig
	installClaudeRunTurnMock(t, func(_ context.Context, cfg claudeRunTurnConfig) (claudeRunTurnResult, error) {
		captured = cfg
		return completedClaudeTurn("done"), nil
	})
	if _, err := invokeClaudeRunTurn(context.Background(), "/work", "do it", "falcon", "", nil, nil); err != nil {
		t.Fatalf("invokeClaudeRunTurn: %v", err)
	}
	ans, ok := captured.OnInputRequest(claudeTrustPrompt())
	if !ok || ans.OptionID != "2" {
		t.Fatalf("callback answered %+v (ok=%v), want the trust prompt denied", ans, ok)
	}
}

// --- stderr helpers ---

// captureStderr runs fn with os.Stderr replaced by a pipe and returns what was
// written. Not parallel-safe (it swaps a process global).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = orig
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	return buf.String()
}

// withStderr swallows the diagnostic output of a call whose return value is
// what the test cares about, so a passing run does not print advice lines.
func withStderr(t *testing.T, fn func() (chat.InputAnswer, bool)) (chat.InputAnswer, bool) {
	t.Helper()
	var ans chat.InputAnswer
	var ok bool
	captureStderr(t, func() { ans, ok = fn() })
	return ans, ok
}
