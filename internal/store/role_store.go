package store

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// RoleCreate is the input for RoleStore.Create. WorkspaceKey + Name
// required.
type RoleCreate struct {
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

// RoleUpdate is the partial-update payload for roles.
type RoleUpdate struct {
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

// RoleStore is the persistence interface for Role entities. Roles are
// workspace-scoped; built-in "plan" and "task" roles are auto-seeded on
// workspace creation.
type RoleStore interface {
	Create(ctx context.Context, in RoleCreate) (*domain.Role, error)
	Get(ctx context.Context, workspaceKey, name string) (*domain.Role, error)
	List(ctx context.Context, workspaceKey string) ([]*domain.Role, error)
	Update(ctx context.Context, workspaceKey, name string, patch RoleUpdate) (*domain.Role, error)
	Delete(ctx context.Context, workspaceKey, name string) error
}
