package config

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Roles load exclusively from the store, so this mapping is the only path by
// which a persisted label constraint reaches routing.
func TestRoleConfigFromDomain_CarriesLabelRouting(t *testing.T) {
	got := roleConfigFromDomain(&domain.Role{
		Name:          "plan-critic",
		Labels:        []string{"plan"},
		ExcludeLabels: []string{"criticized"},
	})
	if len(got.Labels) != 1 || got.Labels[0] != "plan" {
		t.Errorf("Labels = %v, want [plan]", got.Labels)
	}
	if len(got.ExcludeLabels) != 1 || got.ExcludeLabels[0] != "criticized" {
		t.Errorf("ExcludeLabels = %v, want [criticized]", got.ExcludeLabels)
	}
}

// The mapping must copy, not alias, so a later mutation of the config cannot
// reach back into the store's object.
func TestRoleConfigFromDomain_CopiesLabelSlices(t *testing.T) {
	src := &domain.Role{Labels: []string{"plan"}, ExcludeLabels: []string{"criticized"}}
	got := roleConfigFromDomain(src)
	got.Labels[0] = "mutated"
	got.ExcludeLabels[0] = "mutated"
	if src.Labels[0] != "plan" || src.ExcludeLabels[0] != "criticized" {
		t.Errorf("mutating the config reached the domain role: %v / %v", src.Labels, src.ExcludeLabels)
	}
}
