package store

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// TaskRunEventAppend is the input to TaskRunEventStore.Append. The store
// assigns Seq; everything else is caller-supplied. If EventID is empty,
// implementations derive it with domain.TaskRunEventID(TaskRunID,
// Attempt, Type).
type TaskRunEventAppend struct {
	WorkspaceKey   string
	EventID        string
	EpicID         string
	DriverRunID    string
	TaskID         string
	TaskRunID      string
	Type           domain.TaskRunEventType
	Status         domain.TaskRunStatus
	SchedulerState string
	Attempt        int
	ErrorClass     string
	ErrorMessage   string
	LogsRef        string
	ArtifactsRef   string
	LeaseToken     string
	// NextEligibleAt is only meaningful on taskRunRequeued events (retry
	// backoff); the zero value means unset and is omitted from the event.
	NextEligibleAt time.Time
	OccurredAt     time.Time
}

// TaskRunEventFilter narrows TaskRunEventStore.ListSince. AfterSeq is an
// exclusive cursor: only events with Seq > AfterSeq are returned, in
// ascending Seq order. Zero-valued fields match everything.
type TaskRunEventFilter struct {
	EpicID      string
	DriverRunID string
	AfterSeq    int64
	Limit       int
}

// TaskRunEventStore is the append-only journal of task-run lifecycle
// events that feeds watch streams.
//
// Errors wrap the sentinels in package domain per the package doc.
type TaskRunEventStore interface {
	// Append records one lifecycle event. It is idempotent on EventID:
	// appending an event whose EventID already exists returns the
	// existing event without writing a duplicate.
	Append(ctx context.Context, in TaskRunEventAppend) (*domain.TaskRunEvent, error)

	// ListSince returns events for the workspace matching the filter,
	// ordered by ascending Seq.
	ListSince(ctx context.Context, workspaceKey string, filter TaskRunEventFilter) ([]*domain.TaskRunEvent, error)
}

// OutboxCreate is the input to OutboxStore.Create. New records start in
// domain.OutboxStatusPending with Attempt 0.
type OutboxCreate struct {
	WorkspaceKey string
	OutboxID     string
	Kind         domain.OutboxKind
	EpicID       string
	DriverRunID  string
	TaskRunID    string
	TargetAgent  string
	Body         string
	DedupeKey    string
}

// OutboxDueFilter narrows OutboxStore.ListDue. A record is due when its
// Status is pending and its NextRetryAt is nil or not after Now.
type OutboxDueFilter struct {
	Now   time.Time
	Limit int
}

// OutboxDeliveryUpdate is the input to OutboxStore.MarkResult after a
// delivery attempt. NextRetryAt is only meaningful when Status stays
// pending (retry later). InboxMessageID records the agent-inbox message
// created on successful delivery.
type OutboxDeliveryUpdate struct {
	Status         domain.OutboxStatus
	Attempt        int
	NextRetryAt    *time.Time
	LastError      string
	InboxMessageID string
}

// OutboxStore is the server-side notification queue that replaces the
// workflow-side lead-delivery retry loops.
//
// Errors wrap the sentinels in package domain per the package doc.
type OutboxStore interface {
	// Create enqueues a notification. It dedupes on DedupeKey: creating
	// a record whose DedupeKey already exists in the workspace returns
	// the existing record without writing a duplicate.
	Create(ctx context.Context, in OutboxCreate) (*domain.OutboxRecord, error)

	// ListDue returns pending records eligible for a delivery attempt at
	// filter.Now, ordered by ascending Seq.
	ListDue(ctx context.Context, workspaceKey string, filter OutboxDueFilter) ([]*domain.OutboxRecord, error)

	// MarkResult records the outcome of a delivery attempt.
	MarkResult(ctx context.Context, workspaceKey, outboxID string, update OutboxDeliveryUpdate) (*domain.OutboxRecord, error)

	// Get returns a single record by ID.
	Get(ctx context.Context, workspaceKey, outboxID string) (*domain.OutboxRecord, error)
}
