package epicrunner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

// LoadLeadAssignmentContext returns the current backend assignment for a lead,
// or nil when the agent is not a lead or has no assigned epic.
func LoadLeadAssignmentContext(ctx context.Context, st store.Store, workspace, leadName string) (*LeadAssignmentContext, error) {
	workspace = strings.TrimSpace(workspace)
	leadName = strings.TrimSpace(leadName)
	if st == nil || st.Agents() == nil || workspace == "" || leadName == "" {
		return nil, nil
	}

	lead, err := st.Agents().Get(ctx, workspace, leadName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load lead assignment context: %w", err)
	}
	if lead == nil || !IsLeadRole(lead.RoleName) || strings.TrimSpace(lead.Parent) == "" {
		return nil, nil
	}

	orchestratorID, err := store.OrchestrationSessionIDFor(ctx, st, workspace, lead.Name)
	if err != nil {
		orchestratorID = ""
	}

	version := lead.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if lead.UpdatedAt.IsZero() {
		version = lead.Parent
	}
	return &LeadAssignmentContext{
		WorkspaceKey:          workspace,
		LeadName:              lead.Name,
		EpicID:                lead.Parent,
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
