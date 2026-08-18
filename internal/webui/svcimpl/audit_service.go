package svcimpl

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type auditService struct {
	store            store.Store
	resolveEventsDir func(context.Context, string) string
	readDaemonEvents func(string) ([]events.Event, error)
}

func NewAuditService(st store.Store) *auditService {
	s := &auditService{store: st, readDaemonEvents: events.ReadJSONLDir}
	s.resolveEventsDir = s.daemonEventsDir
	return s
}

// ListAuditEvents reads fleet-db's canonical mutation trail and merges the
// daemon's structured local lifecycle events (D4 option b). Synthetic events
// are intentionally cursor-less; nextCursor remains a fleet-db resume token.
func (s *auditService) ListAuditEvents(
	ctx context.Context,
	workspaceKey, since string,
	limit int,
	entityID, actor string,
) ([]store.AuditEvent, string, error) {
	if s.store == nil {
		return nil, "", service.ErrUnavailable("audit trail store unavailable")
	}
	if _, err := s.store.Workspaces().Get(ctx, workspaceKey); err != nil {
		return nil, "", classifyStoreError("load workspace", err)
	}
	reader, ok := s.store.TriggerEvents().(store.AuditJournalReader)
	if !ok {
		return nil, "", service.ErrUnavailable("audit trail store unavailable")
	}
	filter := store.AuditEventFilter{EntityID: entityID, Actor: actor}
	fleetEvents, nextCursor, _, err := reader.ListAuditEvents(ctx, workspaceKey, since, limit, filter)
	if err != nil {
		return nil, "", classifyStoreError("read audit trail", err)
	}

	merged := make([]store.AuditEvent, 0, len(fleetEvents))
	for _, event := range fleetEvents {
		if auditStoreEventMatches(event, filter) {
			merged = append(merged, event)
		}
	}
	merged = append(merged, s.localDaemonAuditEvents(ctx, workspaceKey, since, filter)...)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Timestamp.Equal(merged[j].Timestamp) {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].Timestamp.Before(merged[j].Timestamp)
	})
	truncated := limit > 0 && len(merged) > limit
	if truncated {
		merged = merged[:limit]
	}
	// When local events consume page slots, resume after the last fleet event
	// actually returned instead of skipping a fetched-but-truncated mutation.
	if truncated {
		nextCursor = since
		for i := len(merged) - 1; i >= 0; i-- {
			if merged[i].ID != "" {
				nextCursor = merged[i].ID
				break
			}
		}
	}
	return merged, nextCursor, nil
}

func (s *auditService) daemonEventsDir(ctx context.Context, workspaceKey string) string {
	workspacePath := storeadapter.ResolveWorkspacePath(workspaceKey)
	if workspacePath == "" {
		return ""
	}
	eventsDir := filepath.Join(".loom", "events")
	if profile, err := s.store.Daemon().Get(ctx, workspaceKey); err == nil && profile != nil && strings.TrimSpace(profile.EventsDir) != "" {
		eventsDir = strings.TrimSpace(profile.EventsDir)
	}
	if filepath.IsAbs(eventsDir) {
		return filepath.Clean(eventsDir)
	}
	return filepath.Join(workspacePath, eventsDir)
}

func (s *auditService) localDaemonAuditEvents(
	ctx context.Context,
	workspaceKey, since string,
	filter store.AuditEventFilter,
) []store.AuditEvent {
	if s.resolveEventsDir == nil || s.readDaemonEvents == nil {
		return nil
	}
	dir := s.resolveEventsDir(ctx, workspaceKey)
	if dir == "" {
		return nil
	}
	runtimeEvents, err := s.readDaemonEvents(dir)
	if err != nil {
		// The fleet-db trail remains authoritative and available even when a
		// host-local JSONL file is unreadable or mid-rotation.
		slog.Warn("daemon audit events unavailable", "workspace", workspaceKey, "events_dir", dir, "err", err)
		return nil
	}
	sinceTime := auditCursorTime(since)
	out := make([]store.AuditEvent, 0, len(runtimeEvents))
	for _, event := range runtimeEvents {
		if !sinceTime.IsZero() && !event.Timestamp.After(sinceTime) {
			continue
		}
		synthetic, ok := daemonAuditEvent(workspaceKey, event)
		if ok && auditStoreEventMatches(synthetic, filter) {
			out = append(out, synthetic)
		}
	}
	return out
}

func daemonAuditEvent(workspaceKey string, event events.Event) (store.AuditEvent, bool) { //nolint:funlen // Keep synthetic lifecycle mapping and its shared details contract together.
	action, entityType, entityID, actor := "", "agent", event.Agent, event.Agent
	switch event.Type {
	case events.AgentStarted:
		action = "agent.session_start"
	case events.AgentStopped:
		action = "agent.session_exit"
	case events.AgentRestarted:
		action = "agent.session_restart"
	case events.TaskFailed, events.TaskStuck:
		action = "agent.blocked"
	case events.CircuitOpened:
		action = "agent.blocked"
		if event.Agent == "" {
			action, entityType, entityID, actor = "daemon.blocked", "daemon_profile", workspaceKey, "daemon"
		}
	case events.DaemonStarted:
		action, entityType, entityID, actor = "daemon.start", "daemon_profile", workspaceKey, "daemon"
	default:
		return store.AuditEvent{}, false
	}
	if actor == "" {
		actor = "daemon"
	}
	if entityID == "" {
		entityID = workspaceKey
	}
	details := map[string]any{
		"source":     "daemon",
		"event_type": string(event.Type),
	}
	if event.Role != "" {
		details["agent_role"] = event.Role
	}
	if event.EpicID != "" {
		details["epic_id"] = event.EpicID
	}
	if len(event.Data) > 0 {
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err == nil {
			for key, value := range data {
				details[key] = value
			}
		}
	}
	return store.AuditEvent{
		Timestamp:   event.Timestamp,
		Actor:       actor,
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		WorkspaceID: workspaceKey,
		Details:     details,
	}, true
}

func auditStoreEventMatches(event store.AuditEvent, filter store.AuditEventFilter) bool {
	return (filter.EntityID == "" || event.EntityID == filter.EntityID) &&
		(filter.Actor == "" || event.Actor == filter.Actor)
}

func auditCursorTime(cursor string) time.Time {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" || cursor == "0" {
		return time.Time{}
	}
	millisPart, _, ok := strings.Cut(cursor, "-")
	if !ok {
		return time.Time{}
	}
	millis, err := strconv.ParseInt(millisPart, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(millis)
}
