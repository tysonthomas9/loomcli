package agentdef

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func changedSet(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(n string) bool { return set[n] }
}

// Re-scoping an agent to an epic previously required remove + re-add (losing
// its hooks); these pin the identity patch the update command now builds.
func TestAgentUpdateIdentityPatch_ParentSetAndClear(t *testing.T) {
	agentUpdateParent = "EPIC-7"
	t.Cleanup(func() { agentUpdateParent = "" })
	patch, touched, err := agentUpdateIdentityPatch(changedSet("parent"))
	if err != nil || !touched || patch.Parent == nil || *patch.Parent != "EPIC-7" {
		t.Fatalf("patch = %+v touched=%v err=%v, want Parent=EPIC-7", patch, touched, err)
	}

	agentUpdateParent = ""
	patch, touched, err = agentUpdateIdentityPatch(changedSet("parent"))
	if err != nil || !touched || patch.Parent == nil || *patch.Parent != "" {
		t.Fatalf("explicit --parent \"\" must clear the scope; patch = %+v err=%v", patch, err)
	}

	patch, touched, err = agentUpdateIdentityPatch(changedSet())
	if err != nil || touched || patch.Parent != nil {
		t.Fatalf("no flags must build an empty patch; got %+v touched=%v err=%v", patch, touched, err)
	}
}

func TestAgentUpdateIdentityPatch_ModeValidation(t *testing.T) {
	agentUpdateMode = "batch"
	t.Cleanup(func() { agentUpdateMode = "" })
	if _, _, err := agentUpdateIdentityPatch(changedSet("mode")); err == nil || !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("invalid mode must be rejected, got err=%v", err)
	}
	agentUpdateMode = string(domain.AgentModeEphemeral)
	patch, _, err := agentUpdateIdentityPatch(changedSet("mode"))
	if err != nil || patch.Mode == nil || *patch.Mode != domain.AgentModeEphemeral {
		t.Fatalf("patch = %+v err=%v, want Mode=ephemeral", patch, err)
	}
}

func TestAgentUpdateHooksPatch_IdentityOnlyLeavesHooksUntouched(t *testing.T) {
	hooks, err := agentUpdateHooksPatch(true /* identityChanged */)
	if err != nil || hooks != nil {
		t.Fatalf("identity-only update must not touch hooks; hooks=%v err=%v", hooks, err)
	}
	if _, err := agentUpdateHooksPatch(false); err == nil {
		t.Fatal("no flags at all must still be a 'nothing to update' error")
	}
}

// The task filter decides what an agent claims — and for a custom-role agent,
// WHETHER it claims at all. add grew --task-filter long before update did, so
// the only CLI way to change it was remove + re-add (losing hooks). These pin
// the patch: aliases canonicalize, garbage is rejected, empty clears.
func TestAgentUpdateIdentityPatch_TaskFilter(t *testing.T) {
	agentUpdateFilter = "needs_design"
	t.Cleanup(func() { agentUpdateFilter = "" })
	patch, touched, err := agentUpdateIdentityPatch(changedSet("task-filter"))
	if err != nil || !touched || patch.TaskFilter == nil || *patch.TaskFilter != "needs_plan" {
		t.Fatalf("patch = %+v touched=%v err=%v, want canonical TaskFilter=needs_plan", patch, touched, err)
	}

	agentUpdateFilter = "sometimes"
	if _, _, err := agentUpdateIdentityPatch(changedSet("task-filter")); err == nil ||
		!strings.Contains(err.Error(), "invalid task filter") {
		t.Fatalf("garbage filter must be rejected, got err=%v", err)
	}

	agentUpdateFilter = ""
	patch, touched, err = agentUpdateIdentityPatch(changedSet("task-filter"))
	if err != nil || !touched || patch.TaskFilter == nil || *patch.TaskFilter != "" {
		t.Fatalf("explicit --task-filter \"\" must clear back to the role's filter; patch = %+v err=%v", patch, err)
	}
}
