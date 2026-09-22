package agent

import "github.com/tysonthomas9/loomcli/internal/hookcfg"

// EnsureSkillMaterializeHook keeps harness pre-turn hook setup behind the
// agent runtime seam shared by callers that launch agents.
func EnsureSkillMaterializeHook(worktreePath, backend string) error {
	return hookcfg.EnsureSkillMaterializeHook(worktreePath, backend)
}
