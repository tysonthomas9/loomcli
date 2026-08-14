package terminal

import (
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

// terminalTabDTO is the explicit transport projection of Interaction-owned
// terminal state. Launch arguments, environment, canonical session IDs,
// leases, and fencing tokens stay private to the owner and its adapters.
func terminalTabDTO(meta *interaction.TabMetadata) *loomapi.TabMetadata {
	if meta == nil {
		return nil
	}
	dto := &loomapi.TabMetadata{
		AttachedClients: meta.AttachedClients,
		CreatedAt:       meta.CreatedAt,
		Label:           meta.Label,
		Notes:           meta.Notes,
		Pinned:          meta.Pinned,
		PtyAlive:        meta.PTYAlive,
		SessionName:     meta.SessionName,
		SortOrder:       meta.SortOrder,
		UpdatedAt:       meta.UpdatedAt,
	}
	setOptionalString(&dto.Workspace, meta.Workspace)
	setOptionalString(&dto.IssueId, meta.IssueID)
	setOptionalString(&dto.Kind, meta.Kind)
	setOptionalString(&dto.AgentId, meta.AgentID)
	setOptionalString(&dto.Role, meta.Role)
	setOptionalString(&dto.Backend, meta.Backend)
	if meta.Kind == "agent" || meta.Writable {
		writable := meta.Writable
		dto.Writable = &writable
	}
	return dto
}

func terminalTabDTOs(tabs []interaction.TabMetadata) []loomapi.TabMetadata {
	result := make([]loomapi.TabMetadata, len(tabs))
	for i := range tabs {
		result[i] = *terminalTabDTO(&tabs[i])
	}
	return result
}

// terminalSetupDTO keeps Interaction's setup result transport neutral while
// the HTTP adapter emits the canonical generated contract.
func terminalSetupDTO(result *interaction.TerminalSetupResult) *loomapi.TerminalSetupResult {
	if result == nil {
		return nil
	}
	return &loomapi.TerminalSetupResult{
		Action:      loomapi.TerminalSetupResultAction(result.Action),
		Backend:     loomapi.TerminalSetupResultBackend(result.Backend),
		Command:     result.Command,
		Created:     result.Created,
		Label:       result.Label,
		Manual:      result.Manual,
		Message:     result.Message,
		SessionName: result.SessionName,
		Title:       result.Title,
	}
}

func setOptionalString(target **string, value string) {
	if value == "" {
		return
	}
	copy := value
	*target = &copy
}
