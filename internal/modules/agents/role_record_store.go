package agents

import "context"

// RoleRecordCreate is the input for RoleRecordStore.Create. WorkspaceKey + Name
// required.
type RoleRecordCreate struct {
	WorkspaceKey   string
	Name           string
	Kind           string
	Description    string
	Prompt         string
	PromptFile     string
	Model          string
	TaskFilter     string
	Backend        string
	Effort         string
	PathPatterns   []string
	Skills         []string
	MaxPriority    *int
	MaxConcurrency *int
	ReadOnly       bool
	AllowedTools   []string
	DeniedTools    []string
	MaxBudgetUSD   *float64
}

// RoleRecordUpdate is the partial-update payload for roles.
type RoleRecordUpdate struct {
	Kind           *string
	Description    *string
	Prompt         *string
	PromptFile     *string
	Model          *string
	TaskFilter     *string
	Backend        *string
	Effort         *string
	PathPatterns   *[]string
	Skills         *[]string
	MaxPriority    **int
	MaxConcurrency **int
	ReadOnly       *bool
	AllowedTools   *[]string
	DeniedTools    *[]string
	MaxBudgetUSD   **float64
}

// RoleRecordStore is the persistence interface for Role entities. Roles are
// workspace-scoped; built-in "plan" and "task" roles are auto-seeded on
// workspace creation.
type RoleRecordStore interface {
	Create(ctx context.Context, in RoleRecordCreate) (*Role, error)
	Get(ctx context.Context, workspaceKey, name string) (*Role, error)
	List(ctx context.Context, workspaceKey string) ([]*Role, error)
	Update(ctx context.Context, workspaceKey, name string, patch RoleRecordUpdate) (*Role, error)
	Delete(ctx context.Context, workspaceKey, name string) error
}
