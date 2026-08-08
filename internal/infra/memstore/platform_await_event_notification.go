package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type awaitEventNotificationRow struct {
	state       string
	attempt     int
	claimID     string
	claimUntil  time.Time
	availableAt time.Time
	lastError   string
	completedAt time.Time
}

var _ store.AwaitEventNotificationStore = (*triggerEventStore)(nil)

func (s *triggerEventStore) enqueueAwaitEventNotificationLocked(event *automation.Event) {
	if event == nil {
		return
	}
	canonicalID, canonical := event.CanonicalEventID()
	if !canonical || domain.IsAwaitTimeoutEventID(canonicalID) {
		return
	}
	if s.notifications[event.WorkspaceKey] == nil {
		s.notifications[event.WorkspaceKey] = make(map[string]*awaitEventNotificationRow)
	}
	if s.notifications[event.WorkspaceKey][event.EventID] != nil {
		return
	}
	availableAt := event.ReceivedAt.UTC()
	if availableAt.IsZero() {
		availableAt = time.Now().UTC()
	}
	s.notifications[event.WorkspaceKey][event.EventID] = &awaitEventNotificationRow{
		state: "pending", availableAt: availableAt,
	}
}

//nolint:funlen // Selection, ordering, and lease mutation share the same in-memory critical section.
func (s *triggerEventStore) ClaimAwaitEventNotifications(
	_ context.Context,
	claim store.AwaitEventNotificationClaim,
) ([]store.AwaitEventNotification, error) {
	if strings.TrimSpace(claim.WorkspaceKey) == "" || strings.TrimSpace(claim.ClaimID) == "" ||
		claim.Before.IsZero() || claim.ClaimUntil.IsZero() || !claim.ClaimUntil.After(claim.Before) || claim.Limit < 1 {
		return nil, fmt.Errorf("claim await event notifications: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type candidate struct {
		event *automation.Event
		row   *awaitEventNotificationRow
	}
	values := make([]candidate, 0)
	for eventID, row := range s.notifications[claim.WorkspaceKey] {
		eligible := row.state == "pending" && !row.availableAt.After(claim.Before)
		if row.state == "claimed" && !row.claimUntil.After(claim.Before) {
			eligible = true
		}
		if !eligible {
			continue
		}
		event := s.items[claim.WorkspaceKey][eventID]
		if event == nil {
			continue
		}
		values = append(values, candidate{event: event, row: row})
	}
	sort.Slice(values, func(i, j int) bool {
		if !values[i].row.availableAt.Equal(values[j].row.availableAt) {
			return values[i].row.availableAt.Before(values[j].row.availableAt)
		}
		if !values[i].event.ReceivedAt.Equal(values[j].event.ReceivedAt) {
			return values[i].event.ReceivedAt.Before(values[j].event.ReceivedAt)
		}
		return values[i].event.EventID < values[j].event.EventID
	})
	if len(values) > claim.Limit {
		values = values[:claim.Limit]
	}
	out := make([]store.AwaitEventNotification, 0, len(values))
	for _, value := range values {
		value.row.state = "claimed"
		value.row.claimID = claim.ClaimID
		value.row.claimUntil = claim.ClaimUntil.UTC()
		value.row.attempt++
		event := cloneAwaitNotificationEvent(value.event)
		canonicalID, _ := event.CanonicalEventID()
		payloadSize := len(event.Payload)
		payloadOversized := payloadSize > domain.DefaultAwaitResumePayloadCap
		if payloadOversized {
			event.Payload = nil
		}
		out = append(out, store.AwaitEventNotification{
			Event: event, Attempt: value.row.attempt,
			DurableEventID: value.event.EventID, CanonicalEventID: canonicalID,
			PayloadOversized: payloadOversized, PayloadSize: payloadSize,
		})
	}
	return out, nil
}

func (s *triggerEventStore) CompleteAwaitEventNotification(
	_ context.Context,
	completion store.AwaitEventNotificationCompletion,
) error {
	if strings.TrimSpace(completion.WorkspaceKey) == "" || strings.TrimSpace(completion.EventID) == "" ||
		strings.TrimSpace(completion.ClaimID) == "" {
		return fmt.Errorf("complete await event notification: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.notifications[completion.WorkspaceKey][completion.EventID]
	if row == nil {
		return domain.ErrNotFound
	}
	if row.state == "delivered" && row.claimID == completion.ClaimID {
		return nil
	}
	if row.state != "claimed" || row.claimID != completion.ClaimID {
		return domain.ErrNotOwner
	}
	completedAt := completion.CompletedAt.UTC()
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	row.state = "delivered"
	row.claimUntil = time.Time{}
	row.lastError = ""
	row.completedAt = completedAt
	return nil
}

func (s *triggerEventStore) RetryAwaitEventNotification(
	_ context.Context,
	retry store.AwaitEventNotificationRetry,
) error {
	if strings.TrimSpace(retry.WorkspaceKey) == "" || strings.TrimSpace(retry.EventID) == "" ||
		strings.TrimSpace(retry.ClaimID) == "" || retry.AvailableAt.IsZero() {
		return fmt.Errorf("retry await event notification: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.notifications[retry.WorkspaceKey][retry.EventID]
	if row == nil {
		return domain.ErrNotFound
	}
	if row.state != "claimed" || row.claimID != retry.ClaimID {
		return domain.ErrNotOwner
	}
	row.state = "pending"
	row.claimID = ""
	row.claimUntil = time.Time{}
	row.availableAt = retry.AvailableAt.UTC()
	row.lastError = retry.Error
	return nil
}

func cloneAwaitNotificationEvent(event *automation.Event) automation.Event {
	out := *event
	out.Payload = append([]byte(nil), event.Payload...)
	if event.SubjectAttrs != nil {
		out.SubjectAttrs = make(map[string]string, len(event.SubjectAttrs))
		for key, value := range event.SubjectAttrs {
			out.SubjectAttrs[key] = value
		}
	}
	return out
}
