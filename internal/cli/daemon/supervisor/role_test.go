package supervisor

import (
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// TestMergeRoleConfig_MaxBudgetUSD guards the per-role budget override: a role's
// max_budget_usd must survive MergeRoleConfig so it reaches the spawned worker's
// LOOM_MAX_BUDGET_USD env (see appendRoleEnv in spawn.go), and a nil overlay must
// not clobber a budget already set on the base.
func TestMergeRoleConfig_MaxBudgetUSD(t *testing.T) {
	t.Run("overlay budget overrides base", func(t *testing.T) {
		budget := 30.0
		got := MergeRoleConfig(cfgpkg.RoleConfig{}, cfgpkg.RoleConfig{MaxBudgetUSD: &budget})
		if got.MaxBudgetUSD == nil {
			t.Fatal("MaxBudgetUSD = nil, want overlay value to propagate")
		}
		if *got.MaxBudgetUSD != budget {
			t.Errorf("MaxBudgetUSD = %v, want %v", *got.MaxBudgetUSD, budget)
		}
	})

	t.Run("nil overlay preserves base budget", func(t *testing.T) {
		baseBudget := 12.0
		got := MergeRoleConfig(cfgpkg.RoleConfig{MaxBudgetUSD: &baseBudget}, cfgpkg.RoleConfig{})
		if got.MaxBudgetUSD == nil {
			t.Fatal("MaxBudgetUSD = nil, want base value preserved")
		}
		if *got.MaxBudgetUSD != baseBudget {
			t.Errorf("MaxBudgetUSD = %v, want %v (base preserved)", *got.MaxBudgetUSD, baseBudget)
		}
	})
}

// TestMergeRoleConfig_Labels guards the Labels/ExcludeLabels overlay: a
// role-level Labels/ExcludeLabels must survive MergeRoleConfig (so it reaches
// appendRoutingEnv in spawn.go), and an empty overlay must not clobber a base
// that already has them set.
func TestMergeRoleConfig_Labels(t *testing.T) {
	t.Run("overlay labels override base", func(t *testing.T) {
		got := MergeRoleConfig(cfgpkg.RoleConfig{}, cfgpkg.RoleConfig{
			Labels:        []string{"plan-ready"},
			ExcludeLabels: []string{"plan-reviewed"},
		})
		if len(got.Labels) != 1 || got.Labels[0] != "plan-ready" {
			t.Errorf("Labels = %v, want [plan-ready]", got.Labels)
		}
		if len(got.ExcludeLabels) != 1 || got.ExcludeLabels[0] != "plan-reviewed" {
			t.Errorf("ExcludeLabels = %v, want [plan-reviewed]", got.ExcludeLabels)
		}
	})

	t.Run("empty overlay preserves base labels", func(t *testing.T) {
		base := cfgpkg.RoleConfig{
			Labels:        []string{"plan-ready"},
			ExcludeLabels: []string{"plan-reviewed"},
		}
		got := MergeRoleConfig(base, cfgpkg.RoleConfig{})
		if len(got.Labels) != 1 || got.Labels[0] != "plan-ready" {
			t.Errorf("Labels = %v, want [plan-ready] (base preserved)", got.Labels)
		}
		if len(got.ExcludeLabels) != 1 || got.ExcludeLabels[0] != "plan-reviewed" {
			t.Errorf("ExcludeLabels = %v, want [plan-reviewed] (base preserved)", got.ExcludeLabels)
		}
	})
}

func TestResolveRoleConfigStaticRejectsInteractiveKindRole(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"operator": {
				Kind:       string(domain.RoleKindInteractive),
				PromptFile: "operator.md",
			},
		},
	}

	_, err := ResolveRoleConfigStatic("operator", cfg, t.TempDir())
	if err == nil {
		t.Fatal("ResolveRoleConfigStatic error = nil, want interactive role error")
	}
	if !strings.Contains(err.Error(), "interactive role") || !strings.Contains(err.Error(), "daemon-supervised") {
		t.Fatalf("error = %q, want interactive daemon-supervised message", err.Error())
	}
}
