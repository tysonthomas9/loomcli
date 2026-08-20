package store

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// AgentCreate is the input for AgentStore.Create. WorkspaceKey, Name,
// and RoleName are required; RoleName must reference an existing Role
// in the same workspace.
type AgentCreate struct {
	WorkspaceKey     string
	Name             string
	RoleName         string
	Auto             bool
	Backend          string
	FallbackBackends []string
	RuntimeProvider  domain.RuntimeProvider
	Repos            []string
	RepoGroups       []string
	CrossRepo        bool
	Parent           string
	// OrchestratorSessionID was here historically as a denormalized cache
	// of the lead-to-orchestration AgentSession join. AgentSession is the
	// single source of truth; use store.OrchestrationSessionIDFor.
	Mode           domain.AgentMode
	TaskFilter     string
	MaxConcurrency int
	BudgetPolicy   string
	DesiredState   domain.AgentDesiredState
	Hooks          *domain.AgentHooks
}

// AgentUpdate is the partial-update payload for agents.
type AgentUpdate struct {
	RoleName         *string
	Auto             *bool
	Backend          *string
	FallbackBackends *[]string
	RuntimeProvider  *domain.RuntimeProvider
	Repos            *[]string
	RepoGroups       *[]string
	CrossRepo        *bool
	Parent           *string
	// OrchestratorSessionID removed; see comment on AgentCreate.
	State                *domain.AgentState
	Mode                 *domain.AgentMode
	TaskFilter           *string
	MaxConcurrency       *int
	BudgetPolicy         *string
	DesiredState         *domain.AgentDesiredState
	LastProvisionOutcome *string
	LastProvisionError   *string
	LastProvisionAt      *time.Time
	// Hooks replaces the whole completion pipeline. Nil leaves it untouched;
	// a non-nil empty value clears it.
	Hooks *domain.AgentHooks
}

// AgentStore is the persistence interface for Agent assignments.
type AgentStore interface {
	Create(ctx context.Context, in AgentCreate) (*domain.Agent, error)
	Get(ctx context.Context, workspaceKey, name string) (*domain.Agent, error)
	List(ctx context.Context, workspaceKey string) ([]*domain.Agent, error)
	Update(ctx context.Context, workspaceKey, name string, patch AgentUpdate) (*domain.Agent, error)
	Delete(ctx context.Context, workspaceKey, name string) error
}
