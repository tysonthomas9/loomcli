package prreview

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

const (
	reviewerPresetID        = "pr-review-checkout"
	reviewerPresetRevision  = int64(1)
	reviewerRoleName        = "pr-reviewer"
	reviewerRolePromptFile  = "builtin:pr-review-checkout"
	reviewerRoleDescription = "PR review checkout terminal agent"
)

// reviewerPreset is PR Review's complete, versioned identity policy. Agents
// canonicalizes and fingerprints it before crossing the FleetDB boundary.
func reviewerPreset() (agents.ManagedReviewerPreset, error) {
	metadata, err := agents.WithRuntimeMetadata(nil, agents.RuntimeMetadata{
		RoleKind: agents.RoleKindInteractive,
	})
	if err != nil {
		return agents.ManagedReviewerPreset{}, fmt.Errorf("build PR reviewer runtime metadata: %w", err)
	}
	return agents.ManagedReviewerPreset{
		PresetID: reviewerPresetID,
		Revision: reviewerPresetRevision,
		Role: agents.ManagedReviewerRoleDefinition{
			Name: reviewerRoleName, Kind: agents.RoleKindInteractive,
			Description: reviewerRoleDescription, PromptFile: reviewerRolePromptFile,
		},
		Agent: agents.ManagedReviewerAgentDefinition{
			Kind: agents.AgentKindSupport, DesiredState: agents.DesiredRunning,
			RoleName: reviewerRoleName, MaxInstances: 1, Metadata: metadata,
		},
	}, nil
}

func (m *Module) archiveReviewer(w http.ResponseWriter, r *http.Request) {
	if m == nil || m.reviewerIdentities == nil {
		writeReviewerProvisioningError(w, agents.ErrUnavailable)
		return
	}
	ws, params, ok := m.resolveAuthorizedPR(w, r)
	if !ok {
		return
	}
	agentName := reviewerAgentName(params.owner, params.repo, params.number)
	result, err := m.archiveReviewerAgent(r.Context(), ws, agentName)
	if err != nil {
		writeReviewerProvisioningError(w, err)
		return
	}
	writeJSON(w, reviewerArchiveResult{AgentName: agentName, Archived: result.Agent.DeletedAt != nil})
}

func (m *Module) archiveReviewerAgent(
	ctx context.Context,
	workspace,
	agentID string,
) (*agents.ManagedReviewerResult, error) {
	if m == nil || m.reviewerIdentities == nil {
		return nil, agents.ErrUnavailable
	}
	preset, err := reviewerPreset()
	if err != nil {
		return nil, err
	}
	result, err := m.reviewerIdentities.ConvergeReviewerIdentity(ctx, agents.ManagedReviewerCommand{
		WorkspaceKey: workspace, AgentID: agentID,
		DesiredState: agents.ManagedReviewerArchived, Preset: preset,
	})
	if err != nil {
		return nil, err
	}
	if result == nil || result.Agent == nil || result.Agent.DeletedAt == nil ||
		result.Agent.WorkspaceKey != workspace || result.Agent.AgentID != agentID {
		return nil, fmt.Errorf("PR reviewer archival returned an invalid identity: %w", agents.ErrInvalidPersistedState)
	}
	return result, nil
}
