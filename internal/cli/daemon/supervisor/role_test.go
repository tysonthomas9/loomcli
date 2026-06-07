package supervisor

import (
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
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
