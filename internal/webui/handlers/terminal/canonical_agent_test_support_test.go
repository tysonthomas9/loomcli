package terminal

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type terminalTestAgentCreate struct {
	WorkspaceKey   string
	Name           string
	RoleName       string
	Backend        string
	Parent         string
	Mode           string
	DesiredState   agents.DesiredState
	MaxConcurrency int
}

type terminalTestAgentState string

const (
	terminalTestAgentStateActive  terminalTestAgentState = "active"
	terminalTestAgentStateStopped terminalTestAgentState = "stopped"
)

type terminalTestAgentUpdate struct {
	State        *terminalTestAgentState
	DesiredState *agents.DesiredState
}

type terminalTestAgentStore struct {
	services store.AgentServiceStore
	roles    store.RoleStore
}

func terminalTestAgents(st *memstore.Store) terminalTestAgentStore {
	return terminalTestAgentStore{services: st.AgentServices(), roles: st.Roles()}
}

func (fixture terminalTestAgentStore) Create(ctx context.Context, input terminalTestAgentCreate) (*domain.AgentService, error) {
	if _, err := fixture.roles.Get(ctx, input.WorkspaceKey, input.RoleName); errors.Is(err, domain.ErrNotFound) {
		if _, err := fixture.roles.Create(ctx, store.RoleCreate{WorkspaceKey: input.WorkspaceKey, Name: input.RoleName}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	desired := input.DesiredState
	if desired == "" {
		desired = agents.DesiredRunning
	}
	metadata, err := agents.WithRuntimeMetadata(nil, agents.RuntimeMetadata{Backend: input.Backend})
	if err != nil {
		return nil, err
	}
	maxInstances := input.MaxConcurrency
	if maxInstances < 1 {
		maxInstances = 1
	}
	return fixture.services.Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: input.WorkspaceKey,
		ServiceID:    input.Name,
		Name:         input.Name,
		Kind:         domain.AgentServiceKindSupport,
		DesiredState: domain.AgentServiceDesiredState(desired),
		RoleName:     input.RoleName,
		MaxInstances: maxInstances,
		Metadata:     metadata,
	})
}

func (fixture terminalTestAgentStore) Update(
	ctx context.Context,
	workspace, name string,
	update terminalTestAgentUpdate,
) (*domain.AgentService, error) {
	var desired *domain.AgentServiceDesiredState
	if update.DesiredState != nil {
		value := domain.AgentServiceDesiredState(*update.DesiredState)
		desired = &value
	} else if update.State != nil && *update.State == terminalTestAgentStateStopped {
		value := domain.AgentServiceDesiredStopped
		desired = &value
	}
	return fixture.services.Update(ctx, workspace, name, store.AgentServiceUpdate{DesiredState: desired})
}

type terminalStoreIdentity struct{ services store.AgentServiceStore }

func (identity terminalStoreIdentity) GetAgent(
	ctx context.Context,
	workspace, agentID string,
) (*agents.Agent, error) {
	record, err := identity.services.Get(ctx, workspace, agentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, agents.ErrNotFound
		}
		return nil, err
	}
	return &agents.Agent{
		WorkspaceKey: record.WorkspaceKey,
		AgentID:      record.ServiceID,
		GenerationID: record.GenerationID,
		Name:         record.Name,
		Kind:         agents.AgentKind(record.Kind),
		Behavior: agents.BehaviorReference{
			RoleName: record.RoleName, DriverID: record.DriverID, DriverVersionID: record.DriverVersionID,
		},
		DesiredState: agents.DesiredState(record.DesiredState),
		ProfileName:  record.ProfileName,
		MaxInstances: record.MaxInstances,
		BudgetPolicy: record.BudgetPolicy,
		Metadata:     record.Metadata,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
	}, nil
}
