package terminal

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	agentsowner "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type terminalTestState struct{ *memstore.Store }

func newTerminalTestState() *terminalTestState {
	return &terminalTestState{Store: memstore.New()}
}

func (state *terminalTestState) GetRole(ctx context.Context, workspace, roleName string) (*agentsowner.Role, error) {
	role, err := state.Roles().Get(ctx, workspace, roleName)
	if errors.Is(err, persistence.ErrNotFound) {
		return nil, agentsowner.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &agentsowner.Role{
		WorkspaceKey: role.WorkspaceKey, Name: role.Name, Kind: role.Kind,
		Description: role.Description, Prompt: role.Prompt, PromptFile: role.PromptFile,
		Model: role.Model, TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
		PathPatterns: role.PathPatterns, Skills: role.Skills, MaxPriority: role.MaxPriority,
		MaxConcurrency: role.MaxConcurrency, ReadOnly: role.ReadOnly,
		AllowedTools: role.AllowedTools, DeniedTools: role.DeniedTools,
		MaxBudgetUSD: role.MaxBudgetUSD, CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}, nil
}

func (state *terminalTestState) FindActiveOrchestrationSession(ctx context.Context, workspace, agentID string) (string, error) {
	return interaction.OrchestrationSessionIDFor(ctx, state.Store, workspace, agentID)
}

func (state *terminalTestState) ResolveWorkspaceName(ctx context.Context, workspace string) (string, error) {
	record, err := state.Workspaces().Get(ctx, workspace)
	if err != nil || record == nil {
		return "", err
	}
	return record.Name, nil
}

func (state *terminalTestState) ResolveWorkspacePath(ctx context.Context, workspace string) string {
	return storeadapter.ResolveOrHealWorkspacePath(ctx, state.Store, workspace)
}

type terminalTestAgentCreate struct {
	WorkspaceKey   string
	Name           string
	RoleName       string
	Backend        string
	Parent         string
	Mode           string
	DesiredState   agentsowner.DesiredState
	MaxConcurrency int
}

type terminalTestAgentState string

const (
	terminalTestAgentStateActive  terminalTestAgentState = "active"
	terminalTestAgentStateStopped terminalTestAgentState = "stopped"
)

type terminalTestAgentUpdate struct {
	State        *terminalTestAgentState
	DesiredState *agentsowner.DesiredState
}

type terminalTestAgentStore struct {
	services agentsowner.AgentServiceStore
	roles    agentsowner.RoleRecordStore
}

func terminalTestAgents(st interface {
	AgentServices() agentsowner.AgentServiceStore
	Roles() agentsowner.RoleRecordStore
}) terminalTestAgentStore {
	return terminalTestAgentStore{services: st.AgentServices(), roles: st.Roles()}
}

func (fixture terminalTestAgentStore) Create(ctx context.Context, input terminalTestAgentCreate) (*agentsowner.AgentServiceRecord, error) {
	if _, err := fixture.roles.Get(ctx, input.WorkspaceKey, input.RoleName); errors.Is(err, persistence.ErrNotFound) {
		if _, err := fixture.roles.Create(ctx, agentsowner.RoleRecordCreate{WorkspaceKey: input.WorkspaceKey, Name: input.RoleName}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	desired := input.DesiredState
	if desired == "" {
		desired = agentsowner.DesiredRunning
	}
	metadata, err := agentsowner.WithRuntimeMetadata(nil, agentsowner.RuntimeMetadata{Backend: input.Backend})
	if err != nil {
		return nil, err
	}
	maxInstances := input.MaxConcurrency
	if maxInstances < 1 {
		maxInstances = 1
	}
	return fixture.services.Create(ctx, agentsowner.AgentServiceCreate{
		WorkspaceKey: input.WorkspaceKey,
		ServiceID:    input.Name,
		Name:         input.Name,
		Kind:         agentsowner.AgentKindSupport,
		DesiredState: desired,
		RoleName:     input.RoleName,
		MaxInstances: maxInstances,
		Metadata:     metadata,
	})
}

func (fixture terminalTestAgentStore) Update(
	ctx context.Context,
	workspace, name string,
	update terminalTestAgentUpdate,
) (*agentsowner.AgentServiceRecord, error) {
	var desired *agentsowner.DesiredState
	if update.DesiredState != nil {
		value := *update.DesiredState
		desired = &value
	} else if update.State != nil && *update.State == terminalTestAgentStateStopped {
		value := agentsowner.DesiredStopped
		desired = &value
	}
	return fixture.services.Update(ctx, workspace, name, agentsowner.AgentServiceUpdate{DesiredState: desired})
}

type terminalStoreIdentity struct{ services agentsowner.AgentServiceStore }

func (identity terminalStoreIdentity) GetAgent(
	ctx context.Context,
	workspace, agentID string,
) (*agentsowner.Agent, error) {
	record, err := identity.services.Get(ctx, workspace, agentID)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			return nil, agentsowner.ErrNotFound
		}
		return nil, err
	}
	return &agentsowner.Agent{
		WorkspaceKey: record.WorkspaceKey,
		AgentID:      record.ServiceID,
		GenerationID: record.GenerationID,
		Name:         record.Name,
		Kind:         record.Kind,
		Behavior: agentsowner.BehaviorReference{
			RoleName: record.RoleName, DriverID: record.DriverID, DriverVersionID: record.DriverVersionID,
		},
		DesiredState: record.DesiredState,
		ProfileName:  record.ProfileName,
		MaxInstances: record.MaxInstances,
		BudgetPolicy: record.BudgetPolicy,
		Metadata:     record.Metadata,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
	}, nil
}
