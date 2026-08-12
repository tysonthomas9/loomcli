package agents

import (
	"encoding/json"
	"net/http"

	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func (m *Module) getAgentSessionTranscript(w http.ResponseWriter, r *http.Request) {
	if m.sessionTranscripts == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, loomapi.TranscriptResponse{
			Success: false,
			Error:   optionalAgentRecordString("agent session transcript service not configured"),
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
	handler.WriteJSON(w, http.StatusOK, loomapi.TranscriptResponse{
		Success: true,
		Data: &loomapi.TranscriptData{
			SessionId: sessionID,
			Entries:   generatedTranscriptEntries(entries),
		},
	})
}

func generatedTranscriptEntries(entries agentSessionTranscriptEvents) []loomapi.TranscriptEntry {
	out := make([]loomapi.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		var toolInput interface{}
		if len(entry.ToolInput) != 0 {
			toolInput = json.RawMessage(append([]byte(nil), entry.ToolInput...))
		}
		out = append(out, loomapi.TranscriptEntry{
			Seq: entry.Seq, Timestamp: entry.Timestamp,
			Role: loomapi.TranscriptEntryRole(entry.Role), Type: loomapi.TranscriptEntryType(entry.Type),
			Text: optionalAgentRecordString(entry.Text), ToolName: optionalAgentRecordString(entry.ToolName),
			ToolUseId: optionalAgentRecordString(entry.ToolUseID), ToolInput: toolInput,
			Output: optionalAgentRecordString(entry.Output), Uuid: optionalAgentRecordString(entry.UUID),
		})
	}
	return out
}
