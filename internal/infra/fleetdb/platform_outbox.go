// platform_outbox.go implements execution.TaskRunEventStore and
// execution.OutboxStore against fleet-db's platform v1 routes:
//
//	POST /api/v1/{ws}/task-run-events
//	GET  /api/v1/{ws}/task-run-events
//	POST /api/v1/{ws}/outbox
//	GET  /api/v1/{ws}/outbox/due
//	POST /api/v1/{ws}/outbox/{outbox_id}/result
//
// Casing note: fleet-db's /api/v1 surface is snake_case (field names AND
// enum values like "task_run_queued" / "lead_assignment"), while the
// loomcli domain structs carry camelCase json tags for the driver/watch
// wire. Responses are therefore decoded into local snake_case DTOs and
// converted — never directly into the domain structs.
package fleetdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// --- enum value translation (camelCase domain <-> snake_case fleet-db) ---

// taskRunEventTypeToWire maps a domain event type to fleet-db's
// snake_case value. Unknown values pass through unchanged so a newer
// domain enum still reaches the server (which validates it).
func taskRunEventTypeToWire(t execution.TaskRunEventType) string {
	switch t {
	case execution.TaskRunEventQueued:
		return "task_run_queued"
	case execution.TaskRunEventClaimed:
		return "task_run_claimed"
	case execution.TaskRunEventRequeued:
		return "task_run_requeued"
	case execution.TaskRunEventCompleted:
		return "task_run_completed"
	case execution.TaskRunEventFailed:
		return "task_run_failed"
	case execution.TaskRunEventCancelled:
		return "task_run_cancelled"
	}
	return string(t)
}

// taskRunEventTypeFromWire maps fleet-db's snake_case event type back to
// the domain value. Unknown values pass through unchanged.
func taskRunEventTypeFromWire(s string) execution.TaskRunEventType {
	switch s {
	case "task_run_queued":
		return execution.TaskRunEventQueued
	case "task_run_claimed":
		return execution.TaskRunEventClaimed
	case "task_run_requeued":
		return execution.TaskRunEventRequeued
	case "task_run_completed":
		return execution.TaskRunEventCompleted
	case "task_run_failed":
		return execution.TaskRunEventFailed
	case "task_run_cancelled":
		return execution.TaskRunEventCancelled
	}
	return execution.TaskRunEventType(s)
}

// outboxKindToWire maps a domain outbox kind to fleet-db's snake_case
// value. Unknown values pass through unchanged.
func outboxKindToWire(k execution.OutboxKind) string {
	switch k {
	case execution.OutboxKindLeadAssignment:
		return "lead_assignment"
	case execution.OutboxKindLeadTaskMessage:
		return "lead_task_message"
	}
	return string(k)
}

// outboxKindFromWire maps fleet-db's snake_case outbox kind back to the
// domain value. Unknown values pass through unchanged.
func outboxKindFromWire(s string) execution.OutboxKind {
	switch s {
	case "lead_assignment":
		return execution.OutboxKindLeadAssignment
	case "lead_task_message":
		return execution.OutboxKindLeadTaskMessage
	}
	return execution.OutboxKind(s)
}

// --- wire DTOs (fleet-db snake_case responses) ---

// taskRunEventWire mirrors fleet-db's models.TaskRunEvent JSON shape.
// TaskRunStatus and OutboxStatus enum values are identical on both wires
// ("queued", "pending", ...) so those pass through untranslated.
type taskRunEventWire struct {
	WorkspaceKey   string                        `json:"workspace_key"`
	EventID        string                        `json:"event_id"`
	Seq            int64                         `json:"seq"`
	EpicID         string                        `json:"epic_id"`
	DriverRunID    string                        `json:"driver_run_id"`
	TaskID         string                        `json:"task_id"`
	TaskRunID      string                        `json:"task_run_id"`
	Type           string                        `json:"type"`
	Status         execution.TaskRunRecordStatus `json:"status"`
	SchedulerState string                        `json:"scheduler_state"`
	Attempt        int                           `json:"attempt"`
	ErrorClass     string                        `json:"error_class"`
	ErrorMessage   string                        `json:"error_message"`
	LogsRef        string                        `json:"logs_ref"`
	ArtifactsRef   string                        `json:"artifacts_ref"`
	OccurredAt     time.Time                     `json:"occurred_at"`
}

func (w *taskRunEventWire) toDomain() *execution.TaskRunJournalEvent {
	return &execution.TaskRunJournalEvent{
		WorkspaceKey:   w.WorkspaceKey,
		EventID:        w.EventID,
		Seq:            w.Seq,
		EpicID:         w.EpicID,
		DriverRunID:    w.DriverRunID,
		TaskID:         w.TaskID,
		TaskRunID:      w.TaskRunID,
		Type:           taskRunEventTypeFromWire(w.Type),
		Status:         w.Status,
		SchedulerState: w.SchedulerState,
		Attempt:        w.Attempt,
		ErrorClass:     w.ErrorClass,
		ErrorMessage:   w.ErrorMessage,
		LogsRef:        w.LogsRef,
		ArtifactsRef:   w.ArtifactsRef,
		OccurredAt:     w.OccurredAt,
	}
}

// outboxRecordWire mirrors fleet-db's models.OutboxRecord JSON shape.
type outboxRecordWire struct {
	WorkspaceKey   string                         `json:"workspace_key"`
	OutboxID       string                         `json:"outbox_id"`
	Seq            int64                          `json:"seq"`
	Kind           string                         `json:"kind"`
	EpicID         string                         `json:"epic_id"`
	DriverRunID    string                         `json:"driver_run_id"`
	TaskRunID      string                         `json:"task_run_id"`
	TargetAgent    string                         `json:"target_agent"`
	Body           string                         `json:"body"`
	DedupeKey      string                         `json:"dedupe_key"`
	Status         execution.OutboxDeliveryStatus `json:"status"`
	Attempt        int                            `json:"attempt"`
	NextRetryAt    *time.Time                     `json:"next_retry_at"`
	LastError      string                         `json:"last_error"`
	InboxMessageID string                         `json:"inbox_message_id"`
	CreatedAt      time.Time                      `json:"created_at"`
	UpdatedAt      time.Time                      `json:"updated_at"`
	DeliveredAt    *time.Time                     `json:"delivered_at"`
}

func (w *outboxRecordWire) toDomain() *execution.OutboxDelivery {
	return &execution.OutboxDelivery{
		WorkspaceKey:   w.WorkspaceKey,
		OutboxID:       w.OutboxID,
		Seq:            w.Seq,
		Kind:           outboxKindFromWire(w.Kind),
		EpicID:         w.EpicID,
		DriverRunID:    w.DriverRunID,
		TaskRunID:      w.TaskRunID,
		TargetAgent:    w.TargetAgent,
		Body:           w.Body,
		DedupeKey:      w.DedupeKey,
		Status:         w.Status,
		Attempt:        w.Attempt,
		NextRetryAt:    w.NextRetryAt,
		LastError:      w.LastError,
		InboxMessageID: w.InboxMessageID,
		CreatedAt:      w.CreatedAt,
		UpdatedAt:      w.UpdatedAt,
		DeliveredAt:    w.DeliveredAt,
	}
}

// --- TaskRunEventStore ---

type taskRunEventStore struct{ client *Client }

var _ execution.TaskRunEventStore = (*taskRunEventStore)(nil)

func (s *taskRunEventStore) Append(ctx context.Context, in execution.TaskRunEventAppend) (*execution.TaskRunJournalEvent, error) {
	eventID := in.EventID
	if eventID == "" {
		eventID = execution.TaskRunEventID(in.TaskRunID, in.Attempt, in.Type)
	}
	body := map[string]any{
		"event_id":        eventID,
		"epic_id":         in.EpicID,
		"driver_run_id":   in.DriverRunID,
		"task_id":         in.TaskID,
		"task_run_id":     in.TaskRunID,
		"type":            taskRunEventTypeToWire(in.Type),
		"status":          in.Status,
		"scheduler_state": in.SchedulerState,
		"attempt":         in.Attempt,
		"error_class":     in.ErrorClass,
		"error_message":   in.ErrorMessage,
		"logs_ref":        in.LogsRef,
		"artifacts_ref":   in.ArtifactsRef,
		"occurred_at":     in.OccurredAt,
	}
	var out taskRunEventWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/task-run-events", body, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

func (s *taskRunEventStore) ListSince(ctx context.Context, ws string, filter execution.TaskRunEventFilter) ([]*execution.TaskRunJournalEvent, error) {
	q := url.Values{}
	if filter.EpicID != "" {
		q.Set("epic_id", filter.EpicID)
	}
	if filter.DriverRunID != "" {
		q.Set("driver_run_id", filter.DriverRunID)
	}
	if filter.AfterSeq > 0 {
		q.Set("after_seq", strconv.FormatInt(filter.AfterSeq, 10))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/task-run-events", q)
	var resp struct {
		TaskRunEvents []*taskRunEventWire `json:"task_run_events"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*execution.TaskRunJournalEvent, 0, len(resp.TaskRunEvents))
	for _, event := range resp.TaskRunEvents {
		out = append(out, event.toDomain())
	}
	return out, nil
}

// --- OutboxStore ---

type outboxStore struct{ client *Client }

var _ execution.OutboxStore = (*outboxStore)(nil)

func (s *outboxStore) Create(ctx context.Context, in execution.OutboxCreate) (*execution.OutboxDelivery, error) {
	// fleet-db requires a caller-supplied outbox_id; memstore generates one
	// when empty, so mirror that contract here. Dedupe still happens
	// server-side on dedupe_key, so a fresh random id never duplicates rows.
	outboxID := in.OutboxID
	if outboxID == "" {
		outboxID = generatedOutboxID()
	}
	body := map[string]any{
		"outbox_id":     outboxID,
		"kind":          outboxKindToWire(in.Kind),
		"epic_id":       in.EpicID,
		"driver_run_id": in.DriverRunID,
		"task_run_id":   in.TaskRunID,
		"target_agent":  in.TargetAgent,
		"body":          in.Body,
		"dedupe_key":    in.DedupeKey,
	}
	var out outboxRecordWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/outbox", body, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

func (s *outboxStore) ListDue(ctx context.Context, ws string, filter execution.OutboxDueFilter) ([]*execution.OutboxDelivery, error) {
	q := url.Values{}
	if !filter.Now.IsZero() {
		q.Set("now", filter.Now.UTC().Format(time.RFC3339Nano))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/outbox/due", q)
	var resp struct {
		Outbox []*outboxRecordWire `json:"outbox"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*execution.OutboxDelivery, 0, len(resp.Outbox))
	for _, record := range resp.Outbox {
		out = append(out, record.toDomain())
	}
	return out, nil
}

func (s *outboxStore) MarkResult(ctx context.Context, ws, outboxID string, update execution.OutboxDeliveryUpdate) (*execution.OutboxDelivery, error) {
	body := map[string]any{
		"status":           update.Status,
		"attempt":          update.Attempt,
		"last_error":       update.LastError,
		"inbox_message_id": update.InboxMessageID,
	}
	if update.NextRetryAt != nil {
		body["next_retry_at"] = update.NextRetryAt
	}
	path := "/api/v1/" + pathEscape(ws) + "/outbox/" + pathEscape(outboxID) + "/result"
	var out outboxRecordWire
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

// Get fetches a single record by ID via the natural REST path. NOTE:
// fleet-db@2ea6d00 does not register GET /api/v1/{ws}/outbox/{outbox_id}
// yet (its storage layer has GetOutboxRecord, but no route), so against
// that build this returns persistence.ErrNotFound for every ID. The route gap
// is tracked as a fleet-db follow-up; this client side is already
// correct once the route lands.
func (s *outboxStore) Get(ctx context.Context, ws, outboxID string) (*execution.OutboxDelivery, error) {
	var out outboxRecordWire
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/outbox/"+pathEscape(outboxID), nil, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

// generatedOutboxID returns a random outbox row id for creates that did
// not specify one (rows are addressed by DedupeKey for idempotency, and
// by this id for MarkResult).
func generatedOutboxID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "outbox-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
	}
	return "outbox-" + hex.EncodeToString(buf)
}
