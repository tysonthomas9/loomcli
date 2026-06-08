package domain

import "time"

// Role is the configuration shared by all Agents that take this role —
// prompt template, AI backend, tool allowlist, concurrency cap, etc.
// Workspace-scoped: every Workspace gets its own Role definitions
// (built-in "plan" and "task" are auto-seeded on workspace creation).
type Role struct {
	WorkspaceKey   string   `json:"workspace_key"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
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
