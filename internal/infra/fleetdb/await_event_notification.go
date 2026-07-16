package fleetdb

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

var _ store.AwaitEventNotificationStore = (*triggerEventStore)(nil)

func (s *triggerEventStore) ClaimAwaitEventNotifications(
	ctx context.Context,
	claim store.AwaitEventNotificationClaim,
) ([]store.AwaitEventNotification, error) {
	body := map[string]any{
		"claim_id": claim.ClaimID, "before": claim.Before,
		"claim_until": claim.ClaimUntil, "limit": claim.Limit,
	}
	var response struct {
		Notifications []struct {
			Event            automationEventWire `json:"event"`
			Attempt          int                 `json:"attempt"`
			DurableEventID   string              `json:"durable_event_id"`
			CanonicalEventID string              `json:"canonical_event_id"`
			PayloadOversized bool                `json:"payload_oversized"`
			PayloadSize      int                 `json:"payload_size"`
		} `json:"notifications"`
	}
	path := "/api/v1/" + pathEscape(claim.WorkspaceKey) + "/await-event-notifications/claim"
	if err := s.client.do(ctx, "POST", path, body, &response); err != nil {
		return nil, err
	}
	out := make([]store.AwaitEventNotification, 0, len(response.Notifications))
	for _, notification := range response.Notifications {
		event := notification.Event.event()
		if event == nil {
			continue
		}
		out = append(out, store.AwaitEventNotification{
			Event: *event, Attempt: notification.Attempt,
			DurableEventID: notification.DurableEventID, CanonicalEventID: notification.CanonicalEventID,
			PayloadOversized: notification.PayloadOversized, PayloadSize: notification.PayloadSize,
		})
	}
	return out, nil
}

func (s *triggerEventStore) CompleteAwaitEventNotification(
	ctx context.Context,
	completion store.AwaitEventNotificationCompletion,
) error {
	completedAt := completion.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	body := map[string]any{
		"event_id": completion.EventID, "claim_id": completion.ClaimID, "completed_at": completedAt,
	}
	path := "/api/v1/" + pathEscape(completion.WorkspaceKey) + "/await-event-notifications/complete"
	return s.client.do(ctx, "POST", path, body, nil)
}

func (s *triggerEventStore) RetryAwaitEventNotification(
	ctx context.Context,
	retry store.AwaitEventNotificationRetry,
) error {
	body := map[string]any{
		"event_id": retry.EventID, "claim_id": retry.ClaimID,
		"available_at": retry.AvailableAt, "error": retry.Error,
	}
	path := "/api/v1/" + pathEscape(retry.WorkspaceKey) + "/await-event-notifications/retry"
	return s.client.do(ctx, "POST", path, body, nil)
}
