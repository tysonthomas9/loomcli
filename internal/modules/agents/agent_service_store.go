package agents

import (
	"context"
)

type AgentServiceCreate struct {
	WorkspaceKey    string
	ServiceID       string
	Name            string
	Kind            AgentKind
	DesiredState    DesiredState
	RoleName        string
	DriverID        string
	DriverVersionID string
	ProfileName     string
	ScheduleID      string
	EventSources    []string
	TriggerRefs     []string
	PlacementPolicy string
	MaxInstances    int
	LeaseID         string
	RestartPolicy   string
	Permissions     []string
	BudgetPolicy    string
	StateRef        string
	Metadata        map[string]string
}

type AgentServiceFilter struct {
	Kind           AgentKind
	DesiredState   DesiredState
	RoleName       string
	ProfileName    string
	IncludeDeleted bool
	Limit          int
}

type AgentServiceUpdate struct {
	Name            *string
	Kind            *AgentKind
	DesiredState    *DesiredState
	RoleName        *string
	DriverID        *string
	DriverVersionID *string
	ProfileName     *string
	ScheduleID      *string
	EventSources    *[]string
	TriggerRefs     *[]string
	PlacementPolicy *string
	MaxInstances    *int
	LeaseID         *string
	RestartPolicy   *string
	Permissions     *[]string
	BudgetPolicy    *string
	StateRef        *string
	Metadata        *map[string]string
}

type AgentServiceStore interface {
	Create(ctx context.Context, in AgentServiceCreate) (*AgentServiceRecord, error)
	Get(ctx context.Context, workspaceKey, serviceID string) (*AgentServiceRecord, error)
	List(ctx context.Context, workspaceKey string, filter AgentServiceFilter) ([]*AgentServiceRecord, error)
	Update(ctx context.Context, workspaceKey, serviceID string, patch AgentServiceUpdate) (*AgentServiceRecord, error)
	Delete(ctx context.Context, workspaceKey, serviceID string) error
}
