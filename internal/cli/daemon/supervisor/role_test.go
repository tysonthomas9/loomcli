package supervisor

import (
	"reflect"
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestResolveRoleConfigStaticBuiltinPreservesLabelGates(t *testing.T) {
	wantLabels := []string{"planning"}
	wantExclude := []string{"architect"}
	cfg := &cfgpkg.DaemonConfig{Roles: map[string]cfgpkg.RoleConfig{
		"plan": {
			TaskFilter:    "needs_plan",
			Labels:        wantLabels,
			ExcludeLabels: wantExclude,
		},
	}}

	got, err := ResolveRoleConfigStatic("plan", cfg, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveRoleConfigStatic: %v", err)
	}
	if !reflect.DeepEqual(got.Labels, wantLabels) {
		t.Errorf("Labels = %v, want %v", got.Labels, wantLabels)
	}
	if !reflect.DeepEqual(got.ExcludeLabels, wantExclude) {
		t.Errorf("ExcludeLabels = %v, want %v", got.ExcludeLabels, wantExclude)
	}
}

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
