package domain

import (
	"strconv"
	"time"
)

// TaskRunEventType classifies entries in the append-only TaskRunEvent
// journal. Values are camelCase because they travel on the driver-API /
// watch wire.
//
// Event type table:
//
//	taskRunQueued    — task run created and waiting for a worker claim
//	taskRunClaimed   — a worker claimed the queued run and started executing
//	taskRunRequeued  — run released back to the queue (lease lost, retryable error)
//	taskRunCompleted — run finished successfully
//	taskRunParked    — run exhausted retries / needs operator review; epic continues
//	taskRunFailed    — run finished with a terminal failure
//	taskRunCancelled — run was cancelled before reaching a terminal status
type TaskRunEventType string

const (
	TaskRunEventQueued    TaskRunEventType = "taskRunQueued"
	TaskRunEventClaimed   TaskRunEventType = "taskRunClaimed"
	TaskRunEventRequeued  TaskRunEventType = "taskRunRequeued"
	TaskRunEventCompleted TaskRunEventType = "taskRunCompleted"
	TaskRunEventParked    TaskRunEventType = "taskRunParked"
	TaskRunEventFailed    TaskRunEventType = "taskRunFailed"
	TaskRunEventCancelled TaskRunEventType = "taskRunCancelled"
)

// TaskRunEventID builds the deterministic identity of a journal entry:
// taskRunID + "#" + attempt + "#" + type. Append is idempotent on this
// key, so replaying the same lifecycle transition for the same attempt
// never produces a duplicate row.
func TaskRunEventID(taskRunID string, attempt int, eventType TaskRunEventType) string {
	return taskRunID + "#" + strconv.Itoa(attempt) + "#" + string(eventType)
}

// TaskRunEvent is one entry in the append-only task-run journal that
// feeds watch streams. Seq is store-assigned and monotonically
// increasing per workspace; consumers resume with AfterSeq. LeaseToken
// is included so a watch consumer can call complete-task on behalf of
// the run it observed.
type TaskRunEvent struct {
	WorkspaceKey   string           `json:"workspaceKey"`
	EventID        string           `json:"eventID"`
	Seq            int64            `json:"seq"`
	EpicID         string           `json:"epicID,omitempty"`
	DriverRunID    string           `json:"driverRunID,omitempty"`
	TaskID         string           `json:"taskID,omitempty"`
	TaskRunID      string           `json:"taskRunID"`
	Type           TaskRunEventType `json:"type"`
	Status         TaskRunStatus    `json:"status,omitempty"`
	SchedulerState string           `json:"schedulerState,omitempty"`
	Attempt        int              `json:"attempt"`
	ErrorClass     string           `json:"errorClass,omitempty"`
	ErrorMessage   string           `json:"errorMessage,omitempty"`
	LogsRef        string           `json:"logsRef,omitempty"`
	ArtifactsRef   string           `json:"artifactsRef,omitempty"`
	LeaseToken     string           `json:"leaseToken,omitempty"`
	OccurredAt     time.Time        `json:"occurredAt"`
}

// OutboxKind classifies what an OutboxRecord delivers.
type OutboxKind string

const (
	// OutboxKindLeadAssignment notifies a lead agent that an epic has
	// been assigned to it.
	OutboxKindLeadAssignment OutboxKind = "leadAssignment"

	// OutboxKindLeadTaskMessage delivers a task-completion (or similar)
	// message to the lead agent's inbox.
	OutboxKindLeadTaskMessage OutboxKind = "leadTaskMessage"
)

// OutboxStatus is the delivery lifecycle of an OutboxRecord.
type OutboxStatus string

const (
	// OutboxStatusPending means the record has not been delivered yet
	// (or is awaiting its next retry).
	OutboxStatusPending OutboxStatus = "pending"

	// OutboxStatusDelivered means the dispatcher delivered the message.
	OutboxStatusDelivered OutboxStatus = "delivered"

	// OutboxStatusUnsupported means the target agent cannot receive this
	// kind of message; the record is terminal and never retried.
	OutboxStatusUnsupported OutboxStatus = "unsupported"

	// OutboxStatusFailed means delivery failed terminally (retries
	// exhausted or a non-retryable error).
	OutboxStatusFailed OutboxStatus = "failed"
)

// OutboxRecord is a server-side notification queue row. It replaces the
// workflow-side lead-delivery retry loops: the server creates records,
// and a dispatcher drains due rows and marks results. Seq is
// store-assigned and monotonic per workspace. DedupeKey makes Create
// idempotent — re-creating with the same key returns the existing row.
type OutboxRecord struct {
	WorkspaceKey   string       `json:"workspaceKey"`
	OutboxID       string       `json:"outboxID"`
	Seq            int64        `json:"seq"`
	Kind           OutboxKind   `json:"kind"`
	EpicID         string       `json:"epicID,omitempty"`
	DriverRunID    string       `json:"driverRunID,omitempty"`
	TaskRunID      string       `json:"taskRunID,omitempty"`
	TargetAgent    string       `json:"targetAgent"`
	Body           string       `json:"body,omitempty"`
	DedupeKey      string       `json:"dedupeKey,omitempty"`
	Status         OutboxStatus `json:"status"`
	Attempt        int          `json:"attempt"`
	NextRetryAt    *time.Time   `json:"nextRetryAt,omitempty"`
	LastError      string       `json:"lastError,omitempty"`
	InboxMessageID string       `json:"inboxMessageID,omitempty"`
	CreatedAt      time.Time    `json:"createdAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	DeliveredAt    *time.Time   `json:"deliveredAt,omitempty"`
}
