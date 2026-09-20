package role

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// The v0.8.4 split is silent: a policy written when the acceptance screen still
// arrived as `trust_prompt` parses and validates exactly as before, and only
// stops working at launch. These cases pin when the operator is told.
func TestInputPolicyBypassWarning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		policy *domain.RoleInputPolicy
		warn   bool
		why    string
	}{
		{
			name:   "nil policy says nothing",
			policy: nil,
			warn:   false,
			why:    "no policy is the deny-everything default; this warning is about a policy that looks configured",
		},
		{
			name: "the pre-v0.8.4 shape is the case this exists for",
			policy: &domain.RoleInputPolicy{
				Default: domain.RoleInputDeny,
				Kinds: map[string]string{
					"trust_prompt":    domain.RoleInputAllow,
					"approval_prompt": domain.RoleInputAllow,
				},
			},
			warn: true,
			why:  "allows folder trust, denies the bypass screen by silence — claude exits at launch",
		},
		{
			name: "both allowed is the fixed shape",
			policy: &domain.RoleInputPolicy{
				Default: domain.RoleInputDeny,
				Kinds: map[string]string{
					"trust_prompt":      domain.RoleInputAllow,
					"bypass_acceptance": domain.RoleInputAllow,
				},
			},
			warn: false,
		},
		{
			name: "default=allow already covers the bypass screen",
			policy: &domain.RoleInputPolicy{
				Default: domain.RoleInputAllow,
			},
			warn: false,
			why:  "DispositionFor resolves bypass_acceptance through the default, so there is nothing to fix",
		},
		{
			name: "trust_prompt not allowed is not this warning's business",
			policy: &domain.RoleInputPolicy{
				Default: domain.RoleInputDeny,
				Kinds:   map[string]string{"approval_prompt": domain.RoleInputAllow},
			},
			warn: false,
			why:  "a role that denies folder trust hangs for a different reason; do not bury that under this advice",
		},
		{
			name: "an explicit bypass deny is a decision, and is still warned",
			policy: &domain.RoleInputPolicy{
				Default: domain.RoleInputDeny,
				Kinds: map[string]string{
					"trust_prompt":      domain.RoleInputAllow,
					"bypass_acceptance": domain.RoleInputDeny,
				},
			},
			warn: true,
			why:  "explicit or silent, the consequence at launch is identical: claude answers No, exit",
		},
		{
			name: "ask is not allow",
			policy: &domain.RoleInputPolicy{
				Default: domain.RoleInputDeny,
				Kinds: map[string]string{
					"trust_prompt":      domain.RoleInputAllow,
					"bypass_acceptance": domain.RoleInputAsk,
				},
			},
			warn: true,
			why:  "nothing in a supervised run answers an ask, so it resolves as a refusal in practice",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := inputPolicyBypassWarning(tc.policy)
			if tc.warn && got == "" {
				t.Fatalf("expected a warning (%s), got none", tc.why)
			}
			if !tc.warn && got != "" {
				t.Fatalf("expected no warning (%s), got:\n%s", tc.why, got)
			}
			if !tc.warn {
				return
			}
			// The warning has to name the fix, or it is just noise at a
			// terminal the operator will scroll past.
			for _, want := range []string{"bypass_acceptance=allow", "v0.8.4"} {
				if !strings.Contains(got, want) {
					t.Errorf("warning does not mention %q:\n%s", want, got)
				}
			}
		})
	}
}

// The warning must survive the spelling an operator actually types, since that
// is the path `loom role set` takes.
func TestInputPolicyBypassWarningFromParsedSpec(t *testing.T) {
	t.Parallel()
	policy, err := parseInputPolicySpec([]string{"default=deny", "trust_prompt=allow", "approval_prompt=allow"})
	if err != nil {
		t.Fatalf("parseInputPolicySpec: %v", err)
	}
	if got := inputPolicyBypassWarning(policy); got == "" {
		t.Fatal("the exact pre-v0.8.4 spec parsed from the CLI produced no warning")
	}

	fixed, err := parseInputPolicySpec([]string{"default=deny", "trust_prompt=allow", "approval_prompt=allow", "bypass_acceptance=allow"})
	if err != nil {
		t.Fatalf("parseInputPolicySpec: %v", err)
	}
	if got := inputPolicyBypassWarning(fixed); got != "" {
		t.Fatalf("the corrected spec still warns:\n%s", got)
	}
}
