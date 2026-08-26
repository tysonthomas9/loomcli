package teamtemplate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

var (
	// templateIDPattern is the stable slug that is also the CLI argument.
	templateIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	// memberNamePattern is the shared agent/agent-role name pattern: loomcli's
	// stored-name validator, the frontend's, and fleet-db's all agree on it.
	memberNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`)
	// labelPattern keeps bundle labels to plain lowercase issue-label text. The
	// router compares labels exactly and case-sensitively, so a stray capital or
	// space silently stops matching.
	labelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*$`)
)

// reservedRoleNames are names a template agent role must never take. Each one
// already means something: the seeded trio, the frontend's background-role
// vocabulary, the interactive/epic-owner convention, the automatic PR reviewer,
// and fleet-db's auth tiers.
var reservedRoleNames = map[string]string{
	"plan":         "a seeded built-in agent role",
	"task":         "a seeded built-in agent role",
	"lead":         "a seeded built-in agent role with interactive semantics",
	"orchestrator": "a name that silently acquires interactive and epic-ownership semantics",
	"planner":      "the onboarding default agent name",
	"coder":        "a frontend background agent-role name",
	"worker":       "a frontend background agent-role name",
	"pr-reviewer":  "the automatic PR-reviewer agent role",
	"admin":        "an auth tier",
	"maintainer":   "an auth tier",
	"developer":    "an auth tier",
	"viewer":       "an auth tier",
}

// seededRoleNames are the agent roles every workspace already has, so a bundle
// agent may reference one without the bundle declaring it.
var seededRoleNames = map[string]bool{"plan": true, "task": true, "lead": true}

var (
	validDisplayLabels = map[string]bool{"Developer": true, "QA": true, "Architecture": true}
	// validTaskFilters deliberately omits needs_plan and needs_design. The CLI
	// does handle both (mapTaskFilter and applyTaskFilter each route them to the
	// planning branch, as synonyms), so this is a bundle-schema rule, not a
	// missing implementation. A bundle routes design work by the architect
	// label — the architect role takes task_filter: any plus labels:
	// [architect], and every implementer excludes that label — so a planning
	// filter here would be a second, competing routing mechanism for work the
	// label already claims.
	validTaskFilters   = map[string]bool{"": true, "any": true, "has_design": true}
	validEfforts       = map[string]bool{"": true, "low": true, "medium": true, "high": true, "max": true}
	validDesiredStates = map[string]bool{"running": true, "idle": true, "stopped": true}
)

// validate applies every bundle rule. It is called at init for the embedded
// bundles and directly by the package tests.
func validate(tpl TeamTemplate) error {
	if err := validateMetadata(tpl); err != nil {
		return err
	}
	kinds := make(map[string]string, len(tpl.Roles))
	for _, role := range tpl.Roles {
		if _, dup := kinds[role.Name]; dup {
			return fmt.Errorf("agent role %q: declared twice", role.Name)
		}
		if err := validateRole(role); err != nil {
			return err
		}
		kinds[role.Name] = role.Kind
	}
	names := make(map[string]bool, len(tpl.Agents))
	for _, agent := range tpl.Agents {
		if names[agent.Name] {
			return fmt.Errorf("agent %q: declared twice", agent.Name)
		}
		if err := validateAgent(agent, kinds); err != nil {
			return err
		}
		names[agent.Name] = true
	}
	return nil
}

func validateMetadata(tpl TeamTemplate) error {
	if tpl.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d: this build reads schema_version %d", tpl.SchemaVersion, SchemaVersion)
	}
	if tpl.Revision < 1 {
		return fmt.Errorf("revision %d: must be at least 1", tpl.Revision)
	}
	if !templateIDPattern.MatchString(tpl.ID) {
		return fmt.Errorf("id %q: must be 1-32 lowercase letters, numbers or hyphens and may not start or end with a hyphen", tpl.ID)
	}
	if strings.TrimSpace(tpl.Label) == "" {
		return fmt.Errorf("id %q: label is required", tpl.ID)
	}
	if strings.TrimSpace(tpl.Description) == "" {
		return fmt.Errorf("id %q: description is required", tpl.ID)
	}
	if len(tpl.Roles) == 0 {
		return fmt.Errorf("id %q: at least one agent role is required", tpl.ID)
	}
	return nil
}

func validateRole(role TemplateRole) error {
	if err := validateRoleIdentity(role); err != nil {
		return err
	}
	if err := validateRoleRouting(role); err != nil {
		return err
	}
	return validateRolePolicy(role)
}

func validateRoleIdentity(role TemplateRole) error {
	if !memberNamePattern.MatchString(role.Name) {
		return fmt.Errorf("agent role %q: name must be 1-100 lowercase letters, numbers, dots, hyphens or underscores and may not start or end with punctuation", role.Name)
	}
	if why, reserved := reservedRoleNames[role.Name]; reserved {
		return fmt.Errorf("agent role %q: reserved — %s", role.Name, why)
	}
	if role.Kind != string(domain.RoleKindWorker) && role.Kind != string(domain.RoleKindInteractive) {
		return fmt.Errorf("agent role %q: kind %q must be explicitly worker or interactive", role.Name, role.Kind)
	}
	if strings.TrimSpace(role.Description) == "" {
		return fmt.Errorf("agent role %q: description is required — it is the delegation criterion", role.Name)
	}
	if !validDisplayLabels[role.DisplayLabel] {
		return fmt.Errorf("agent role %q: display_label %q must be Developer, QA or Architecture", role.Name, role.DisplayLabel)
	}
	return validateRolePrompt(role)
}

func validateRolePrompt(role TemplateRole) error {
	id, ok := role.promptID()
	if !ok {
		return fmt.Errorf("agent role %q: prompt_file %q must be a builtin: reference — a template never writes prompt files into a workspace", role.Name, role.PromptFile)
	}
	if role.Kind == string(domain.RoleKindInteractive) {
		if !domain.IsBuiltinInteractivePrompt(id) {
			return fmt.Errorf("agent role %q: prompt_file %q is not a registered interactive prompt", role.Name, role.PromptFile)
		}
		return nil
	}
	if !domain.IsBuiltinWorkerPrompt(id) {
		return fmt.Errorf("agent role %q: prompt_file %q is not a registered worker prompt", role.Name, role.PromptFile)
	}
	return nil
}

func validateRoleRouting(role TemplateRole) error {
	if !validTaskFilters[role.TaskFilter] {
		return fmt.Errorf("agent role %q: task_filter %q must be has_design, any or empty (route design work with the architect label, not a planning filter)", role.Name, role.TaskFilter)
	}
	if !validEfforts[role.Effort] {
		return fmt.Errorf("agent role %q: effort %q must be low, medium, high, max or empty", role.Name, role.Effort)
	}
	if role.Model != "" {
		return fmt.Errorf("agent role %q: model must be empty so the bundle stays backend-neutral", role.Name)
	}
	if err := validateLabels(role.Name, "labels", role.Labels); err != nil {
		return err
	}
	if err := validateLabels(role.Name, "exclude_labels", role.ExcludeLabels); err != nil {
		return err
	}
	if role.Kind == string(domain.RoleKindWorker) && role.TaskFilter == "any" && len(role.Labels) == 0 {
		return fmt.Errorf("agent role %q: task_filter any needs at least one entry in labels, or it out-claims the seeded task agent role", role.Name)
	}
	excluded := make(map[string]bool, len(role.ExcludeLabels))
	for _, label := range role.ExcludeLabels {
		excluded[label] = true
	}
	for _, label := range role.Labels {
		if excluded[label] {
			return fmt.Errorf("agent role %q: label %q is both required and excluded — exclusion always wins, so the agent role can never match", role.Name, label)
		}
	}
	return nil
}

func validateLabels(roleName, field string, labels []string) error {
	for _, label := range labels {
		if !labelPattern.MatchString(label) {
			return fmt.Errorf("agent role %q: %s entry %q must be a non-empty lowercase issue label", roleName, field, label)
		}
	}
	return nil
}

// validateRolePolicy asserts the two safety knobs that fail far from the
// bundle: read_only costs a design agent role the ability to write its design,
// and a non-empty tool list is a hard refusal to run on every non-Claude
// backend while bundles inherit the workspace default.
func validateRolePolicy(role TemplateRole) error {
	if role.ReadOnly {
		return fmt.Errorf("agent role %q: read_only must be false — the deny-set includes Bash, and a design agent role would lose the ability to persist its design", role.Name)
	}
	if len(role.AllowedTools) > 0 || len(role.DeniedTools) > 0 {
		return fmt.Errorf("agent role %q: allowed_tools and denied_tools must be empty — only Claude enforces tool lists and a non-empty list is a hard refusal to run on every other backend", role.Name)
	}
	if role.MaxConcurrency != nil && *role.MaxConcurrency < 1 {
		return fmt.Errorf("agent role %q: max_concurrency must be at least 1", role.Name)
	}
	if role.MaxBudgetUSD != nil && *role.MaxBudgetUSD <= 0 {
		return fmt.Errorf("agent role %q: max_budget_usd must be positive", role.Name)
	}
	if role.MaxRunDuration != nil && *role.MaxRunDuration <= 0 {
		return fmt.Errorf("agent role %q: max_run_duration must be positive", role.Name)
	}
	return nil
}

// validateAgent checks one bundle agent against the agent roles the bundle
// declares (kinds) plus the seeded built-ins.
func validateAgent(agent TemplateAgent, kinds map[string]string) error {
	if !memberNamePattern.MatchString(agent.Name) {
		return fmt.Errorf("agent %q: name must be 1-100 lowercase letters, numbers, dots, hyphens or underscores and may not start or end with punctuation", agent.Name)
	}
	if want := agent.RoleName + "-1"; agent.Name != want {
		return fmt.Errorf("agent %q: must be named %q — every template agent is <agent role>-1", agent.Name, want)
	}
	kind, declared := kinds[agent.RoleName]
	if !declared && !seededRoleNames[agent.RoleName] {
		return fmt.Errorf("agent %q: agent role %q is neither declared in this template nor seeded", agent.Name, agent.RoleName)
	}
	if declared && kind == string(domain.RoleKindInteractive) {
		return fmt.Errorf("agent %q: agent role %q is interactive — interactive agent roles are provisioned without agents", agent.Name, agent.RoleName)
	}
	if !validDesiredStates[agent.DesiredState] {
		return fmt.Errorf("agent %q: desired_state %q must be running, idle or stopped", agent.Name, agent.DesiredState)
	}
	if !agent.CrossRepo {
		return fmt.Errorf("agent %q: cross_repo must be true so worktrees match the routing scope", agent.Name)
	}
	if err := agent.Hooks.Validate(); err != nil {
		return fmt.Errorf("agent %q: %w", agent.Name, err)
	}
	return nil
}
