package store

import (
	"context"
	"encoding/json"
	"time"
)

// JournalEvent is one entry of fleet-db's issue mutation journal as the
// bridge reads it (chunk A4). It is a deliberately narrow projection of
// fleet-db's GET /api/v1/{ws}/events/mutations event shape: the bridge polls
// issue lifecycle (entity_type=issue) and only needs the post-mutation state.
//
// ID is the stream position — the opaque cursor token for this event
// (immutable, monotonically increasing, e.g. "1707001234567-0"). Callers
// resume by passing the ID of the last event they consumed back as the
// afterCursor argument; fleet-db echoes the batch's last position via the
// response Cursor field, which ListIssueEvents returns as nextCursor.
//
// After carries the post-mutation entity state as raw JSON (fleet-db
// serializes it as a JSON-encoded string on the wire; the reader unwraps
// that into the JSON value here). Before is deliberately dropped: the bridge
// reacts to the current state and the prior snapshot would only inflate the
// payload. After is nil when the event carried no after-state.
type JournalEvent struct {
	ID        string
	Action    string
	Actor     string
	EntityID  string
	Timestamp time.Time
	After     json.RawMessage
	Metadata  map[string]string
}

// AuditEvent is the complete fleet-db mutation event exposed by the audit
// read path. Before and After intentionally retain fleet-db's JSON-encoded
// snapshot strings so `loom audit --output json` can emit the server's raw
// event shape without inventing a second wire representation.
//
// Details is populated only for synthetic local daemon events. It is excluded
// from raw JSON output; the web UI audit handler folds it into its locked
// details object alongside fleet-db Before/After/Metadata fields.
type AuditEvent struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Actor       string            `json:"actor"`
	Action      string            `json:"action"`
	EntityType  string            `json:"entity_type"`
	EntityID    string            `json:"entity_id"`
	WorkspaceID string            `json:"workspace_id"`
	Before      string            `json:"before,omitempty"`
	After       string            `json:"after,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Details     map[string]any    `json:"-"`
}

// AuditEventFilter narrows both historical reads and live subscriptions.
// Empty fields match every audit event.
type AuditEventFilter struct {
	EntityID string
	Actor    string
}

// AuditJournalReader is an OPTIONAL TriggerEventStore capability implemented
// by the fleet-db HTTP store. ListAuditEvents reads events strictly after the
// cursor, oldest first. SubscribeAuditEvents attaches to fleet-db's SSE event
// stream at the supplied cursor; cancellation is owned by ctx.
type AuditJournalReader interface {
	ListAuditEvents(ctx context.Context, workspaceKey, afterCursor string, limit int, filter AuditEventFilter) (events []AuditEvent, nextCursor string, hasMore bool, err error)
	SubscribeAuditEvents(ctx context.Context, workspaceKey, afterCursor string, filter AuditEventFilter) (<-chan AuditEvent, <-chan error)
}

// IssueJournalReader is an OPTIONAL store capability (detected by type
// assertion on the TriggerEventStore, like TriggerEventAppender): a forward,
// since-cursor read over fleet-db's issue event journal so the A4 bridge can
// poll it. ListIssueEvents fetches issue mutations strictly after afterCursor
// (an opaque cursor token previously returned as nextCursor, or "" / "0" to
// start from the beginning), up to limit events, oldest first.
//
// nextCursor is the stream position to resume from on the next call — the
// last returned event's ID, or the unchanged afterCursor when the batch is
// empty. hasMore reports whether fleet-db filled the page (limit events
// returned) and more may be waiting; callers keep paging while it is true.
//
// Only the fleet-db client implements this. memstore deliberately does NOT —
// the bridge is capability-gated on its presence, exactly as the run.finished
// lane gates on TriggerEventAppender. There is no fleet-db change: the
// endpoint already supports since/limit and entity_type/action filters.
type IssueJournalReader interface {
	ListIssueEvents(ctx context.Context, workspaceKey, afterCursor string, limit int) (events []JournalEvent, nextCursor string, hasMore bool, err error)
}
