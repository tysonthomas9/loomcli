package terminal

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// HandleEnsureAgentTerminalSession is the HTTP adapter for Interaction's
// canonical interactive-terminal command. Agent, Role, placement, launch,
// stale-tab, and lifecycle policy remain inside Interaction.
func HandleEnsureAgentTerminalSession(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false, Error: "terminal service not initialized",
			})
			return
		}
		workspace := middleware.WorkspaceFromContext(r.Context())
		agentID := r.PathValue("name")
		if !agentcoord.IsValidAgentName(agentID) {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false, Error: "invalid agent name",
			})
			return
		}
		meta, err := svc.EnsureAgentTerminal(r.Context(), interaction.EnsureAgentTerminalCommand{
			WorkspaceKey: workspace,
			AgentID:      agentID,
		})
		if err != nil {
			handler.HandleTerminalError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{Success: true, Data: terminalTabDTO(meta)})
	}
}
