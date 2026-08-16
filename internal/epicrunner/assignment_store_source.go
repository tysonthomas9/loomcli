package epicrunner

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// StoreLeadAssignmentSource is the composition-edge adapter for standalone
// lead and hook processes. It reads the canonical AgentService,
// WorkerProfile, and orchestration-session projections and never reads or
// mutates the retired supervised-assignment aggregate.
type StoreLeadAssignmentSource struct {
	store leadAssignmentStore
}

type leadAssignmentStore interface {
	interaction.OrchestrationSessionStore
	AgentServices() agents.AgentServiceStore
	WorkerProfiles() execution.WorkerProfileStore
}

func NewStoreLeadAssignmentSource(st leadAssignmentStore) LeadAssignmentSource {
	if st == nil || st.AgentServices() == nil || st.WorkerProfiles() == nil || st.AgentSessions() == nil {
		return nil
	}
	return &StoreLeadAssignmentSource{store: st}
}

func (source *StoreLeadAssignmentSource) GetLeadAssignmentIdentity(
	ctx context.Context,
	workspace,
	agentID string,
) (*LeadAssignmentIdentity, error) {
	if source == nil || source.store == nil {
		return nil, persistence.ErrUnavailable
	}
	record, err := source.store.AgentServices().Get(ctx, workspace, agentID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("canonical Agent %q is nil: %w", agentID, persistence.ErrInvalid)
	}
	return &LeadAssignmentIdentity{
		WorkspaceKey: record.WorkspaceKey,
		AgentID:      record.ServiceID,
		RoleName:     record.RoleName,
		ProfileName:  record.ProfileName,
	}, nil
}

func (source *StoreLeadAssignmentSource) GetLeadAssignmentProfile(
	ctx context.Context,
	workspace,
	profileID string,
) (*LeadAssignmentProfile, error) {
	if source == nil || source.store == nil {
		return nil, persistence.ErrUnavailable
	}
	profile, err := source.store.WorkerProfiles().Get(ctx, workspace, profileID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, fmt.Errorf("canonical WorkerProfile %q is nil: %w", profileID, persistence.ErrInvalid)
	}
	return &LeadAssignmentProfile{
		WorkspaceKey: profile.WorkspaceKey,
		ProfileID:    profile.ProfileID,
		RoleName:     profile.Role,
		ParentEpic:   profile.ParentEpic,
		UpdatedAt:    profile.UpdatedAt,
	}, nil
}

func (source *StoreLeadAssignmentSource) GetLeadOrchestrationSessionID(
	ctx context.Context,
	workspace,
	agentID string,
) (string, error) {
	if source == nil || source.store == nil {
		return "", persistence.ErrUnavailable
	}
	return interaction.OrchestrationSessionIDFor(ctx, source.store, workspace, agentID)
}
