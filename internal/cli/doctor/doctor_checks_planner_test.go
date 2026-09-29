package doctor

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestCheckPlannerHostSubmit_MissingHooksFails(t *testing.T) {
	// Drive the check indirectly via the same predicates doctor uses — full
	// LoadDaemonConfig needs a workspace runtime; unit-test the fail message
	// contract here so remediations stay identical to spawn admit errors.
	missing := backends.PlanHostSubmitRemediation("planner-a")
	if !strings.Contains(missing, "on-complete-write-design") {
		t.Fatalf("remediation = %q, want agentdef write-design flag", missing)
	}
	if !strings.Contains(missing, "on-complete-set-status review") {
		t.Fatalf("remediation = %q, want set-status review", missing)
	}

	hooks := backends.DefaultPlanHostHooks()
	entry := cfgpkg.AgentEntry{Worktree: "p", Role: "plan", Hooks: hooks}
	if !backends.HasHostDesignSubmit(entry.Hooks) || !backends.HasHostStatusReview(entry.Hooks) {
		t.Fatal("default plan hooks must pass doctor predicates")
	}
}
