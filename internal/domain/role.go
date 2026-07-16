package domain

import (
	"strings"
	"time"
)

type RoleKind string

const (
	RoleKindInteractive RoleKind = "interactive"
	RoleKindWorker      RoleKind = "worker"
)

// Role is the configuration shared by all Agents that take this role —
// prompt template, AI backend, tool allowlist, concurrency cap, etc.
// Workspace-scoped: every Workspace gets its own Role definitions
// (built-in "plan" and "task" are auto-seeded on workspace creation).
type Role struct {
	WorkspaceKey   string   `json:"workspace_key"`
	Name           string   `json:"name"`
	Kind           RoleKind `json:"kind,omitempty"`
	Description    string   `json:"description,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	PromptFile     string   `json:"prompt_file,omitempty"`
	Model          string   `json:"model,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	PathPatterns   []string `json:"path_patterns,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	MaxPriority    *int     `json:"max_priority,omitempty"`
	MaxConcurrency *int     `json:"max_concurrency,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ResolveRoleKind returns the effective kind for a role. Explicit Kind wins;
// roles with no kind fall back to the legacy name convention where
// "lead"/"orchestrator" are interactive and everything else is a worker.
func ResolveRoleKind(role *Role, roleName string) RoleKind {
	if role != nil {
		if kind := RoleKind(strings.ToLower(strings.TrimSpace(string(role.Kind)))); kind != "" {
			return kind
		}
	}
	if IsInteractiveRoleName(roleName) {
		return RoleKindInteractive
	}
	return RoleKindWorker
}

// IsInteractiveRoleName reports whether a role name uses the legacy
// interactive-agent naming convention.
func IsInteractiveRoleName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lead", "orchestrator":
		return true
	default:
		return false
	}
}
