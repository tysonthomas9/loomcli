package audit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	defaultLimit = 100
	maxLimit     = 500
)

type listResponse struct {
	Events     []eventResponse `json:"events"`
	NextCursor string          `json:"next_cursor"`
}

type responseEnvelope struct {
	Success bool         `json:"success"`
	Data    listResponse `json:"data"`
}

type eventResponse struct {
	Cursor     string         `json:"cursor"`
	Timestamp  string         `json:"timestamp"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Details    map[string]any `json:"details,omitempty"`
}

func HandleList(auditService Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, err := parseLimit(r.URL.Query().Get("limit"))
		if err != nil {
			handler.HandleServiceError(w, service.ErrValidation(err.Error()))
			return
		}
		workspaceKey := middleware.WorkspaceFromContext(r.Context())
		if workspaceKey == "" {
			workspaceKey = r.PathValue("ws")
		}
		events, nextCursor, err := auditService.ListAuditEvents(
			r.Context(),
			strings.TrimSpace(workspaceKey),
			strings.TrimSpace(r.URL.Query().Get("since")),
			limit,
			strings.TrimSpace(r.URL.Query().Get("entity")),
			strings.TrimSpace(r.URL.Query().Get("actor")),
		)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		response := listResponse{Events: make([]eventResponse, 0, len(events)), NextCursor: nextCursor}
		for _, event := range events {
			response.Events = append(response.Events, auditEventResponse(event))
		}
		handler.WriteJSON(w, http.StatusOK, responseEnvelope{Success: true, Data: response})
	}
}

func parseLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxLimit {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", maxLimit)
	}
	return limit, nil
}

func auditEventResponse(event store.AuditEvent) eventResponse {
	return eventResponse{
		Cursor:     event.ID,
		Timestamp:  event.Timestamp.UTC().Format(time.RFC3339Nano),
		Actor:      event.Actor,
		Action:     event.Action,
		EntityType: event.EntityType,
		EntityID:   event.EntityID,
		Details:    auditEventDetails(event),
	}
}

func auditEventDetails(event store.AuditEvent) map[string]any {
	details := make(map[string]any, len(event.Details)+len(event.Metadata))
	for key, value := range event.Details {
		details[key] = value
	}
	for key, value := range event.Metadata {
		details[key] = value
	}
	flattenSnapshot(details, "old", event.Before)
	flattenSnapshot(details, "new", event.After)
	if len(details) == 0 {
		return nil
	}
	return details
}

func flattenSnapshot(details map[string]any, prefix, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err == nil && object != nil {
		for key, value := range object {
			details[prefix+"_"+key] = value
		}
		return
	}
	// Preserve a malformed or non-object snapshot rather than silently losing
	// payload data from the audit response.
	details[prefix+"_value"] = raw
}
