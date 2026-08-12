package supervisor

import (
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// The executor selects a different leaf entirely; the overlay must carry it
// like every other role knob, and an empty overlay must not erase it.
func TestMergeRoleConfig_ExecutorOverlay(t *testing.T) {
	base := cfgpkg.RoleConfig{Executor: "turn"}
	merged := MergeRoleConfig(base, cfgpkg.RoleConfig{Executor: "conversation"})
	if merged.Executor != "conversation" {
		t.Fatalf("Executor = %q, want the overlay's conversation", merged.Executor)
	}
	kept := MergeRoleConfig(base, cfgpkg.RoleConfig{})
	if kept.Executor != "turn" {
		t.Fatalf("Executor = %q, want the base kept on empty overlay", kept.Executor)
	}
}
