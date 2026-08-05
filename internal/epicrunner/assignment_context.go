package epicrunner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// LeadAssignmentContext is the provider-neutral context a lead runtime should
// see when the backend has assigned it an epic.
type LeadAssignmentContext struct {
	WorkspaceKey          string
	LeadName              string
	EpicID                string
	AssignmentVersion     string
	OrchestratorSessionID string
}

// LeadAssignmentIdentity is the canonical Agents-owned identity projection
// needed by lead assignment. It deliberately excludes the retired
// supervised-assignment record.
type LeadAssignmentIdentity struct {
	WorkspaceKey string
	AgentID      string
	RoleName     string
	ProfileName  string
}

// LeadAssignmentProfile is the Execution-owned scheduling projection that
// carries the currently assigned epic and its revision.
type LeadAssignmentProfile struct {
	WorkspaceKey string
	ProfileID    string
	RoleName     string
	ParentEpic   string
	UpdatedAt    time.Time
}

// LeadAssignmentSource is the combined read model consumed by interactive
// providers. Its three operations remain owned by Agents, Execution, and
// Interaction respectively; this query never exposes a mutation surface.
type LeadAssignmentSource interface {
	GetLeadAssignmentIdentity(context.Context, string, string) (*LeadAssignmentIdentity, error)
	GetLeadAssignmentProfile(context.Context, string, string) (*LeadAssignmentProfile, error)
	GetLeadOrchestrationSessionID(context.Context, string, string) (string, error)
}

// LoadLeadAssignmentContext returns the current backend assignment for a lead,
// or nil when the agent is not a lead or has no assigned epic.
func LoadLeadAssignmentContext(ctx context.Context, source LeadAssignmentSource, workspace, leadName string) (*LeadAssignmentContext, error) {
	workspace = strings.TrimSpace(workspace)
	leadName = strings.TrimSpace(leadName)
	if source == nil || workspace == "" || leadName == "" {
		return nil, nil
	}

	lead, err := source.GetLeadAssignmentIdentity(ctx, workspace, leadName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load lead assignment context: %w", err)
	}
	if lead == nil || lead.WorkspaceKey != workspace || lead.AgentID != leadName ||
		!IsLeadRole(lead.RoleName) || strings.TrimSpace(lead.ProfileName) == "" {
		return nil, nil
	}
	profile, err := source.GetLeadAssignmentProfile(ctx, workspace, lead.ProfileName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load lead assignment profile: %w", err)
	}
	if profile == nil || profile.WorkspaceKey != workspace || profile.ProfileID != lead.ProfileName ||
		profile.RoleName != lead.RoleName || strings.TrimSpace(profile.ParentEpic) == "" {
		return nil, nil
	}

	orchestratorID, err := source.GetLeadOrchestrationSessionID(ctx, workspace, lead.AgentID)
	if err != nil {
		orchestratorID = ""
	}

	version := profile.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if profile.UpdatedAt.IsZero() {
		version = profile.ParentEpic
	}
	return &LeadAssignmentContext{
		WorkspaceKey:          workspace,
		LeadName:              lead.AgentID,
		EpicID:                profile.ParentEpic,
		AssignmentVersion:     version,
		OrchestratorSessionID: orchestratorID,
	}, nil
}

// FormatLeadAssignmentContext renders assignment state for provider prompts
// and hook additionalContext. Keep this compact; provider adapters should pass
// details by reference to backend state, not by dumping the whole epic.
func FormatLeadAssignmentContext(a *LeadAssignmentContext) string {
	if a == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Loom backend assignment:\n")
	fmt.Fprintf(&b, "- workspace: %s\n", a.WorkspaceKey)
	fmt.Fprintf(&b, "- lead: %s\n", a.LeadName)
	fmt.Fprintf(&b, "- assigned_epic: %s\n", a.EpicID)
	if a.AssignmentVersion != "" {
		fmt.Fprintf(&b, "- assignment_version: %s\n", a.AssignmentVersion)
	}
	if a.OrchestratorSessionID != "" {
		fmt.Fprintf(&b, "- orchestration_session: %s\n", a.OrchestratorSessionID)
	}
	b.WriteString("\nTreat this as authoritative backend state. If the visible conversation did not mention this epic, acknowledge that Loom assigned it and continue from this assignment unless the user explicitly changes or clears it.")
	return b.String()
}
