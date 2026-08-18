package teamtemplate

import "github.com/tysonthomas9/loomcli/internal/domain"

// Divergence comparison. With no provenance stamp on the stored rows, config
// comparison is the only way to tell "this template's agent role from an
// earlier apply" from "the user's own agent role that happens to share the
// name". The comparison is report-only: apply never mutates what exists.
//
// Two rules make it usable:
//
//   - Only the fields the bundle SETS are compared. A field the bundle leaves
//     zero is ignored, so a user's extra path_patterns or max_priority is never
//     reported as divergence.
//   - Slice fields compare order-insensitively, as sets.
//
// Field names use the store/API vocabulary the user sees everywhere else, and
// the returned order is stable so a report reads the same on every run.

type fieldCheck struct {
	name     string
	diverged bool
}

func divergedFields(checks []fieldCheck) []string {
	var out []string
	for _, check := range checks {
		if check.diverged {
			out = append(out, check.name)
		}
	}
	return out
}

// compareRole returns the bundle-set fields on which an existing agent role
// differs from the bundle. An empty result means skipped_match.
func compareRole(tpl TemplateRole, existing *domain.Role) []string {
	// The stored kind may be blank on an agent role created before kinds were
	// explicit; resolving it the way the runtime does keeps a legacy worker from
	// looking like a divergence.
	kind := string(domain.ResolveRoleKind(existing, existing.Name))
	return divergedFields([]fieldCheck{
		stringDiff("kind", tpl.Kind, kind),
		stringDiff("description", tpl.Description, existing.Description),
		stringDiff("prompt_file", tpl.PromptFile, existing.PromptFile),
		stringDiff("model", tpl.Model, existing.Model),
		stringDiff("task_filter", tpl.TaskFilter, existing.TaskFilter),
		stringDiff("effort", tpl.Effort, existing.Effort),
		setDiff("skills", tpl.Skills, existing.Skills),
		setDiff("labels", tpl.Labels, existing.Labels),
		setDiff("exclude_labels", tpl.ExcludeLabels, existing.ExcludeLabels),
		boolDiff("read_only", tpl.ReadOnly, existing.ReadOnly),
		setDiff("denied_tools", tpl.DeniedTools, existing.DeniedTools),
		setDiff("allowed_tools", tpl.AllowedTools, existing.AllowedTools),
		ptrDiff("max_concurrency", tpl.MaxConcurrency, existing.MaxConcurrency),
		ptrDiff("max_budget_usd", tpl.MaxBudgetUSD, existing.MaxBudgetUSD),
		ptrDiff("max_run_duration", tpl.MaxRunDuration, existing.MaxRunDuration),
	})
}

// compareAgent returns the bundle-set fields on which an existing agent differs
// from the bundle.
func compareAgent(tpl TemplateAgent, existing *domain.Agent) []string {
	return divergedFields([]fieldCheck{
		stringDiff("role_name", tpl.RoleName, existing.RoleName),
		boolDiff("auto", tpl.Auto, existing.Auto),
		stringDiff("desired_state", tpl.DesiredState, string(existing.DesiredState)),
		boolDiff("cross_repo", tpl.CrossRepo, existing.CrossRepo),
	})
}

func stringDiff(name, bundle, existing string) fieldCheck {
	return fieldCheck{name: name, diverged: bundle != "" && bundle != existing}
}

func boolDiff(name string, bundle, existing bool) fieldCheck {
	return fieldCheck{name: name, diverged: bundle && bundle != existing}
}

func setDiff(name string, bundle, existing []string) fieldCheck {
	return fieldCheck{name: name, diverged: len(bundle) > 0 && !sameSet(bundle, existing)}
}

func ptrDiff[T comparable](name string, bundle, existing *T) fieldCheck {
	if bundle == nil {
		return fieldCheck{name: name}
	}
	return fieldCheck{name: name, diverged: existing == nil || *existing != *bundle}
}

func sameSet(a, b []string) bool {
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	other := make(map[string]bool, len(b))
	for _, v := range b {
		if !seen[v] {
			return false
		}
		other[v] = true
	}
	return len(seen) == len(other)
}
