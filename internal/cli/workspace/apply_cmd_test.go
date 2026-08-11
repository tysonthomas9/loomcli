package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Each case below is a live failure this validation exists to prevent. They are
// written as "the spec that used to apply cleanly and then misbehaved", so a
// regression removes the guard rather than merely renaming a message.

func intPtr(i int) *int { return &i }

func allowTrust() *domain.RoleInputPolicy {
	return &domain.RoleInputPolicy{Default: domain.RoleInputDeny, Kinds: map[string]string{trustPromptKind: domain.RoleInputAllow}}
}

// pipelineSpec is a minimal two-stage pipeline that must validate clean.
func pipelineSpec() *cfgpkg.DaemonConfig {
	return &cfgpkg.DaemonConfig{
		Daemon: cfgpkg.DaemonSettings{MaxAgents: intPtr(4)},
		Roles: map[string]cfgpkg.RoleConfig{
			"plan": {TaskFilter: "any", ExcludeLabels: []string{"drafted"}, InputPolicy: allowTrust()},
			"task": {TaskFilter: "any", Labels: []string{"drafted"}, ExcludeLabels: []string{"done"}, InputPolicy: allowTrust()},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "planner", Role: "plan", Auto: true, Backend: "claude", Hooks: &domain.AgentHooks{
				OnComplete: []domain.AgentHookAction{{Type: domain.AgentHookActionAddLabel, Value: "drafted"}},
			}},
			{Worktree: "worker", Role: "task", Auto: true, Backend: "claude", Hooks: &domain.AgentHooks{
				OnComplete: []domain.AgentHookAction{{Type: domain.AgentHookActionAddLabel, Value: "done"}},
			}},
		},
	}
}

func problemsContaining(t *testing.T, problems []string, substr string) bool {
	t.Helper()
	for _, p := range problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

func TestValidateSpec_CleanPipelineApplies(t *testing.T) {
	if problems := validateSpec(pipelineSpec(), t.TempDir()); len(problems) != 0 {
		t.Fatalf("expected a clean pipeline to validate, got: %v", problems)
	}
}

// A stage that stamps a label it does not exclude re-claims its own handed-off
// task. Seen live as one task shipped 41 times in ~2 minutes.
func TestValidateSpec_StampedLabelMustBeExcludedBySameRole(t *testing.T) {
	spec := pipelineSpec()
	rc := spec.Roles["plan"]
	rc.ExcludeLabels = nil
	spec.Roles["plan"] = rc

	problems := validateSpec(spec, t.TempDir())
	if !problemsContaining(t, problems, "re-claim the task it just handed on") {
		t.Fatalf("expected a self-exclusion problem, got: %v", problems)
	}
}

// A TUI backend with no answer for the trust prompt hangs forever rather than
// failing: the harness waits for a composer that never becomes ready.
func TestValidateSpec_TUIBackendRequiresTrustPromptAllowance(t *testing.T) {
	spec := pipelineSpec()
	rc := spec.Roles["task"]
	rc.InputPolicy = nil
	spec.Roles["task"] = rc

	problems := validateSpec(spec, t.TempDir())
	if !problemsContaining(t, problems, trustPromptKind) {
		t.Fatalf("expected a trust-prompt problem, got: %v", problems)
	}
}

// An external (non-TUI) backend does not raise the prompt, so it must NOT be
// forced to carry a policy — otherwise every fixture pipeline fails to apply.
func TestValidateSpec_ExternalBackendNeedsNoInputPolicy(t *testing.T) {
	spec := pipelineSpec()
	for i := range spec.Agents {
		spec.Agents[i].Backend = "testfixture"
	}
	for name, rc := range spec.Roles {
		rc.InputPolicy = nil
		spec.Roles[name] = rc
	}
	if problems := validateSpec(spec, t.TempDir()); len(problems) != 0 {
		t.Fatalf("external backends should need no input policy, got: %v", problems)
	}
}

// Exceeding max_agents fails DAEMON CREATION, so every agent stops — not just
// the extra one.
func TestValidateSpec_AutoAgentsMustFitTheCeiling(t *testing.T) {
	spec := pipelineSpec()
	spec.Daemon.MaxAgents = intPtr(1)

	problems := validateSpec(spec, t.TempDir())
	if !problemsContaining(t, problems, "fails daemon creation") {
		t.Fatalf("expected a capacity problem, got: %v", problems)
	}
}

// The built-in plan role defaults to task_filter=needs_plan, which silently
// drops a planner REVISION pass once a design exists.
func TestValidateSpec_LabelRoutedRoleNeedsExplicitTaskFilter(t *testing.T) {
	spec := pipelineSpec()
	rc := spec.Roles["plan"]
	rc.TaskFilter = ""
	spec.Roles["plan"] = rc

	problems := validateSpec(spec, t.TempDir())
	if !problemsContaining(t, problems, "explicit task_filter") {
		t.Fatalf("expected a task_filter problem, got: %v", problems)
	}
}

// An unresolvable prompt_file fails daemon creation entirely, so it is caught
// before anything is written.
func TestValidateSpec_CustomRolePromptFileMustExist(t *testing.T) {
	dir := t.TempDir()
	spec := pipelineSpec()
	spec.Roles["critic"] = cfgpkg.RoleConfig{
		Kind: "worker", PromptFile: "prompts/critic.md", TaskFilter: "any",
		Labels: []string{"drafted"}, InputPolicy: allowTrust(),
	}

	problems := validateSpec(spec, dir)
	if !problemsContaining(t, problems, "not found relative to the spec") {
		t.Fatalf("expected a missing prompt_file problem, got: %v", problems)
	}

	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "critic.md"), []byte("be critical"), 0o600); err != nil {
		t.Fatal(err)
	}
	if problems := validateSpec(spec, dir); problemsContaining(t, problems, "not found relative to the spec") {
		t.Fatalf("prompt file exists now; got: %v", problems)
	}
}

func TestValidateSpec_BuiltinRoleCannotSetPromptFile(t *testing.T) {
	spec := pipelineSpec()
	rc := spec.Roles["plan"]
	rc.PromptFile = "prompts/plan.md"
	spec.Roles["plan"] = rc

	if problems := validateSpec(spec, t.TempDir()); !problemsContaining(t, problems, "built-in roles cannot set prompt_file") {
		t.Fatalf("expected a built-in prompt_file problem, got: %v", problems)
	}
}

func TestValidateSpec_AgentRoleMustExist(t *testing.T) {
	spec := pipelineSpec()
	spec.Agents = append(spec.Agents, cfgpkg.AgentEntry{Worktree: "tester", Role: "tester", Auto: false, Backend: "claude"})

	if problems := validateSpec(spec, t.TempDir()); !problemsContaining(t, problems, "neither built-in nor defined in this spec") {
		t.Fatalf("expected an unknown-role problem, got: %v", problems)
	}
}

// A cycle's ship label is a stamp like any other: the cycle stage must exclude
// it, or it re-ships the task until the downstream stage wins the claim race.
func TestValidateSpec_CycleShipLabelIsAStamp(t *testing.T) {
	spec := pipelineSpec()
	spec.Roles["critic"] = cfgpkg.RoleConfig{
		Kind: "worker", Prompt: "critique", TaskFilter: "any",
		Labels: []string{"drafted"}, InputPolicy: allowTrust(),
	}
	spec.Agents = append(spec.Agents, cfgpkg.AgentEntry{
		Worktree: "critic", Role: "critic", Auto: false, Backend: "claude",
		Hooks: &domain.AgentHooks{OnComplete: []domain.AgentHookAction{{
			Type:  domain.AgentHookActionCycle,
			Cycle: &domain.AgentHookCycle{Threshold: 2, RearmLabel: "drafted", ShipLabel: "shipped"},
		}}},
	})

	if problems := validateSpec(spec, t.TempDir()); !problemsContaining(t, problems, "stamps \"shipped\"") {
		t.Fatalf("expected the cycle ship label to be treated as a stamp, got: %v", problems)
	}
}
