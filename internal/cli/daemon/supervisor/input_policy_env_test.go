package supervisor

import (
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func roleEnvMap(t *testing.T, rc cfgpkg.RoleConfig) map[string]string {
	t.Helper()
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}, RoleConfig: rc}
	out := make(map[string]string)
	for _, entry := range appendRoleEnv(nil, ap, defaultMaxRunDurationSeconds) {
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			out[entry[:idx]] = entry[idx+1:]
		}
	}
	return out
}

// The supervisor→leaf hop. The policy is structured, so unlike the tool lists
// it rides as JSON in one variable; what matters is that it survives intact and
// that a role WITHOUT one exports nothing at all rather than an empty value the
// leaf would have to interpret.
func TestAppendRoleEnv_InputPolicyRoundTripsThroughEnv(t *testing.T) {
	policy := &domain.RoleInputPolicy{
		Default: domain.RoleInputDeny,
		Kinds:   map[string]string{"trust_prompt": domain.RoleInputAllow, "confirm": domain.RoleInputAsk},
	}

	raw, ok := roleEnvMap(t, cfgpkg.RoleConfig{InputPolicy: policy})["LOOM_ROLE_INPUT_POLICY"]
	if !ok {
		t.Fatal("LOOM_ROLE_INPUT_POLICY not exported for a role that has a policy")
	}

	decoded, err := domain.DecodeRoleInputPolicy(raw)
	if err != nil {
		t.Fatalf("DecodeRoleInputPolicy(%q): %v", raw, err)
	}
	for kind, want := range map[string]string{
		"trust_prompt": domain.RoleInputAllow,
		"confirm":      domain.RoleInputAsk,
		"unnamed":      domain.RoleInputDeny,
	} {
		if got := decoded.DispositionFor(kind); got != want {
			t.Errorf("after the env hop DispositionFor(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestAppendRoleEnv_NoPolicyExportsNothing(t *testing.T) {
	if v, ok := roleEnvMap(t, cfgpkg.RoleConfig{})["LOOM_ROLE_INPUT_POLICY"]; ok {
		t.Fatalf("LOOM_ROLE_INPUT_POLICY = %q for a role with no policy; absent and deny-everything must stay the same state", v)
	}
}

// A present-but-empty policy is still exported, because it is a thing the
// operator wrote — and it must decode to something that denies everything, the
// same as no policy at all.
func TestAppendRoleEnv_EmptyPolicyStillDenies(t *testing.T) {
	raw := roleEnvMap(t, cfgpkg.RoleConfig{InputPolicy: &domain.RoleInputPolicy{}})["LOOM_ROLE_INPUT_POLICY"]
	decoded, err := domain.DecodeRoleInputPolicy(raw)
	if err != nil {
		t.Fatalf("DecodeRoleInputPolicy(%q): %v", raw, err)
	}
	if got := decoded.DispositionFor("trust_prompt"); got != domain.RoleInputDeny {
		t.Fatalf("an empty exported policy resolved to %q, want %q", got, domain.RoleInputDeny)
	}
}

// The overlay replaces the base policy wholesale. Merging the Kinds maps would
// let a base entry the overlay deliberately dropped survive, and the surviving
// entry could be the permissive one.
func TestMergeRoleConfig_InputPolicyOverlayReplacesWholesale(t *testing.T) {
	base := cfgpkg.RoleConfig{InputPolicy: &domain.RoleInputPolicy{
		Kinds: map[string]string{"trust_prompt": domain.RoleInputAllow},
	}}
	overlay := cfgpkg.RoleConfig{InputPolicy: &domain.RoleInputPolicy{
		Kinds: map[string]string{"confirm": domain.RoleInputAllow},
	}}

	merged := MergeRoleConfig(base, overlay)
	if got := merged.InputPolicy.DispositionFor("trust_prompt"); got != domain.RoleInputDeny {
		t.Errorf("base's allow survived the overlay: DispositionFor(trust_prompt) = %q, want %q", got, domain.RoleInputDeny)
	}
	if got := merged.InputPolicy.DispositionFor("confirm"); got != domain.RoleInputAllow {
		t.Errorf("overlay's allow lost: DispositionFor(confirm) = %q, want %q", got, domain.RoleInputAllow)
	}

	// A nil overlay leaves the base alone (same shape as the other knobs).
	kept := MergeRoleConfig(base, cfgpkg.RoleConfig{})
	if got := kept.InputPolicy.DispositionFor("trust_prompt"); got != domain.RoleInputAllow {
		t.Errorf("a nil overlay must not clear the base policy, got %q", got)
	}
}
