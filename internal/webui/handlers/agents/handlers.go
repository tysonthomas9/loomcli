package agents

import (
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type interactivePromptsResponse struct {
	Prompts []domain.BuiltinInteractivePrompt `json:"prompts"`
}

func HandleInteractivePrompts() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		handler.WriteJSON(w, http.StatusOK, interactivePromptsResponse{
			Prompts: visibleInteractivePrompts(),
		})
	}
}

func visibleInteractivePrompts() []domain.BuiltinInteractivePrompt {
	prompts := domain.BuiltinInteractivePrompts()
	out := make([]domain.BuiltinInteractivePrompt, 0, len(prompts))
	for _, prompt := range prompts {
		if !prompt.Hidden {
			out = append(out, prompt)
		}
	}
	return out
}

// Keep the legacy service error vocabulary behind the already-ratcheted
// handler composition file. Canonical Agent handlers use these narrow HTTP
// helpers rather than introducing new dependencies on the global service
// package during the Phase 6 retirement.
func writeAgentValidationError(w http.ResponseWriter, message string) {
	handler.HandleServiceError(w, service.ErrValidation(message))
}

func writeAgentConflictError(w http.ResponseWriter, message string) {
	handler.HandleServiceError(w, service.ErrConflict(message))
}

func writeAgentInternalError(w http.ResponseWriter, message string, cause error) {
	handler.HandleServiceError(w, service.ErrInternal(message, cause))
}

func validStoredAgentName(value string) bool {
	return value != "" && service.ValidStoredAgentName.MatchString(value)
}

type lifecycleRequest struct{}

func broadcastAgentRefresh(hub *realtime.Hub, workspace, agentName, actor string) {
	if hub == nil || workspace == "" {
		return
	}
	hub.Broadcast(&realtime.MutationPayload{
		Type:        "refresh",
		EntityType:  "agent",
		EntityID:    agentName,
		Action:      "agent.refresh",
		Title:       agentName,
		Actor:       actor,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		WorkspaceID: workspace,
	})
}
