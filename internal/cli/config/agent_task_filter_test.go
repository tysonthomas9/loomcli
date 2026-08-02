package config

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// task_filter went missing from this struct while the domain type, the store,
// and `agentdef show` all carried it — so the value survived every hop except
// the one that mattered, and three symptoms fell out of the single gap: the
// claim gate could not see it, MergeRoleConstraints could not overlay it, and
// (because Equal feeds diffAgents and the whole struct feeds the reconcile
// hash) a task_filter-only edit reconciled as "no change". These tests pin the
// two hops that made the gap silent.

func TestAgentEntry_EqualIncludesTaskFilter(t *testing.T) {
	base := AgentEntry{Worktree: "designer", Role: "reviewer"}

	withFilter := base
	withFilter.TaskFilter = "any"

	if base.Equal(withFilter) {
		t.Error("an entry that gained a task_filter must not compare equal")
	}
	if withFilter.Equal(base) {
		t.Error("Equal must be symmetric for a task_filter difference")
	}

	same := base
	same.TaskFilter = "any"
	if !withFilter.Equal(same) {
		t.Error("identical task_filters should compare equal")
	}
}

func TestAgentEntryFromDomain_CarriesTaskFilter(t *testing.T) {
	entry := agentEntryFromDomain(&domain.Agent{
		Name: "designer", RoleName: "reviewer", TaskFilter: "any",
	})
	if entry.TaskFilter != "any" {
		t.Fatalf("entry.TaskFilter = %q, want %q", entry.TaskFilter, "any")
	}
}
