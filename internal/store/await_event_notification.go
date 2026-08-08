package store

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// AwaitEventNotification is one durable trigger-event notification claimed
// by Execution's await reconciler. Event.EventID is the outbox identity;
// SourceEventID (when present) is the canonical resume/audit identity.
type AwaitEventNotification struct {
	Event            automation.Event `json:"event"`
	Attempt          int              `json:"attempt"`
	DurableEventID   string           `json:"durable_event_id,omitempty"`
	CanonicalEventID string           `json:"canonical_event_id,omitempty"`
	PayloadOversized bool             `json:"payload_oversized,omitempty"`
	PayloadSize      int              `json:"payload_size,omitempty"`
}

type AwaitEventNotificationClaim struct {
	WorkspaceKey string
	ClaimID      string
	Before       time.Time
	ClaimUntil   time.Time
	Limit        int
}

type AwaitEventNotificationCompletion struct {
	WorkspaceKey string
	EventID      string
	ClaimID      string
	CompletedAt  time.Time
}

type AwaitEventNotificationRetry struct {
	WorkspaceKey string
	EventID      string
	ClaimID      string
	AvailableAt  time.Time
	Error        string
}

// AwaitEventNotificationStore is an optional TriggerEventStore capability.
// Production FleetDB implements a leased durable outbox populated atomically
// with event admission. Expired claims are reclaimable; completion and retry
// are owner-checked and idempotent.
type AwaitEventNotificationStore interface {
	ClaimAwaitEventNotifications(context.Context, AwaitEventNotificationClaim) ([]AwaitEventNotification, error)
	CompleteAwaitEventNotification(context.Context, AwaitEventNotificationCompletion) error
	RetryAwaitEventNotification(context.Context, AwaitEventNotificationRetry) error
}
