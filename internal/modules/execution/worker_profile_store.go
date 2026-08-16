package execution

import (
	"context"
)

type WorkerProfileCreate struct {
	WorkspaceKey  string
	ProfileID     string
	Name          string
	Role          string
	Backend       string
	RuntimePolicy map[string]string
	Repos         []string
	MaxPriority   *int
	MaxParallel   int
	ParentEpic    string
	Labels        []string
	Capabilities  []string
	Enabled       *bool
	Metadata      map[string]string
}

type WorkerProfileUpdate struct {
	Name               *string
	Role               *string
	Backend            *string
	RuntimePolicy      *map[string]string
	Repos              *[]string
	MaxPriority        *int
	MaxParallel        *int
	ClearMaxPriority   bool
	ParentEpic         *string
	ExpectedParentEpic *string
	Labels             *[]string
	Capabilities       *[]string
	Enabled            *bool
	Metadata           *map[string]string
}

type WorkerProfileStore interface {
	Create(ctx context.Context, in WorkerProfileCreate) (*WorkerProfile, error)
	Get(ctx context.Context, workspaceKey, profileID string) (*WorkerProfile, error)
	List(ctx context.Context, workspaceKey string, filter WorkerProfileFilter) ([]*WorkerProfile, error)
	Update(ctx context.Context, workspaceKey, profileID string, patch WorkerProfileUpdate) (*WorkerProfile, error)
	Delete(ctx context.Context, workspaceKey, profileID string) error
}
