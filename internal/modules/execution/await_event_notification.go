package execution

import (
	"context"
)

// AwaitEventNotificationStore is an optional TriggerEventStore capability.
// Production FleetDB implements a leased durable outbox populated atomically
// with event admission. Expired claims are reclaimable; completion and retry
// are owner-checked and idempotent.
type AwaitEventNotificationStore interface {
	ClaimAwaitEventNotifications(context.Context, AwaitEventNotificationLease) ([]AwaitEventNotification, error)
	CompleteAwaitEventNotification(context.Context, AwaitEventNotificationCompletion) error
	RetryAwaitEventNotification(context.Context, AwaitEventNotificationRetry) error
}
