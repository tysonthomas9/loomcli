package store

import (
	"context"

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
	Repos            []string
	RepoGroups       []string
	CrossRepo        bool
	Parent           string
}

// AgentUpdate is the partial-update payload for agents.
type AgentUpdate struct {
	RoleName         *string
	Auto             *bool
	Backend          *string
	FallbackBackends *[]string
	Repos            *[]string
	RepoGroups       *[]string
	CrossRepo        *bool
	Parent           *string
	State            *domain.AgentState
}

// AgentStore is the persistence interface for Agent assignments.
type AgentStore interface {
	Create(ctx context.Context, in AgentCreate) (*domain.Agent, error)
	Get(ctx context.Context, workspaceKey, name string) (*domain.Agent, error)
	List(ctx context.Context, workspaceKey string) ([]*domain.Agent, error)
	Update(ctx context.Context, workspaceKey, name string, patch AgentUpdate) (*domain.Agent, error)
	Delete(ctx context.Context, workspaceKey, name string) error
}
