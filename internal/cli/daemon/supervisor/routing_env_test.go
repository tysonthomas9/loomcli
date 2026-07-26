package supervisor

import (
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// The env is how a role's routing constraints reach the spawned agent, which
// re-reads them via cli.RoleConfigFromEnv. A missing writer means the child
// silently runs without the constraint.
func TestAppendRoutingEnv_Labels(t *testing.T) {
	ap := &AgentProcess{
		Entry: cfgpkg.AgentEntry{Role: "plan-critic"},
		RoleConfig: cfgpkg.RoleConfig{
			Labels:        []string{"plan", "urgent"},
			ExcludeLabels: []string{"criticized"},
		},
	}
	env := appendRoutingEnv(nil, ap)
	want := map[string]string{
		"LOOM_ROLE_LABELS":         "plan,urgent",
		"LOOM_ROLE_EXCLUDE_LABELS": "criticized",
	}
	for k, v := range want {
		if !hasEnv(env, k+"="+v) {
			t.Errorf("env missing %s=%s\ngot: %v", k, v, env)
		}
	}
}

// A role without label constraints must not emit the vars at all — an empty
// value would otherwise be split into a single empty label.
func TestAppendRoutingEnv_LabelsOmitted(t *testing.T) {
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Role: "worker"}}
	for _, e := range appendRoutingEnv(nil, ap) {
		if strings.HasPrefix(e, "LOOM_ROLE_LABELS=") || strings.HasPrefix(e, "LOOM_ROLE_EXCLUDE_LABELS=") {
			t.Errorf("unexpected %q for a role with no label constraints", e)
		}
	}
}

func hasEnv(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}
