package agents

import (
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type agentSessionTranscriptResponse struct {
	Success bool                        `json:"success"`
	Data    *agentSessionTranscriptData `json:"data,omitempty"`
	Error   string                      `json:"error,omitempty"`
}

type agentSessionTranscriptData struct {
	SessionID string                       `json:"session_id"`
	Entries   agentSessionTranscriptEvents `json:"entries"`
}

func (m *Module) getAgentSessionTranscript(w http.ResponseWriter, r *http.Request) {
	if m.sessionTranscripts == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, agentSessionTranscriptResponse{
			Success: false,
			Error:   "agent session transcript service not configured",
		})
		return
	}
	workspace, ok := m.requireCanonicalWorkspace(w, r)
	if !ok {
		return
	}
	sessionID := r.PathValue("session_id")
	entries, err := m.sessionTranscripts.GetAgentSessionTranscript(
		r.Context(),
		workspace,
		r.PathValue("id"),
		sessionID,
	)
	if err != nil {
		writeAgentSessionTranscriptServiceError(w, err)
		return
	}
	if entries == nil {
		entries = agentSessionTranscriptEvents{}
	}
	handler.WriteJSON(w, http.StatusOK, agentSessionTranscriptResponse{
		Success: true,
		Data: &agentSessionTranscriptData{
			SessionID: sessionID,
			Entries:   entries,
		},
	})
}
