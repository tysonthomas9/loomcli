package execution

import "time"

// AuditEvent is the immutable activity projection for one DriverRun event.
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
}

// AuditPage is one cursor-delimited page of DriverRun activity.
type AuditPage struct {
	Events []AuditEvent `json:"events"`
	Cursor string       `json:"cursor"`
}
