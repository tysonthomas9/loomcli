package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

func (s *Service) dispatchAdmission(ctx context.Context, reserved *ReservationResult) (*AdmissionResult, error) {
	if reserved != nil && reserved.Replayed {
		refreshed, err := s.refreshReplayedDeliveries(ctx, reserved)
		if err != nil {
			return nil, err
		}
		reserved = refreshed
	}
	dispatcher := newAdmissionDispatcher(ctx, s, reserved)
	promptAgentCandidates := repositoryRequiredPromptAgentCandidates(reserved)
	if len(promptAgentCandidates) == 0 {
		dispatcher.dispatchAcceptedExcept(nil)
		return dispatcher.result, errors.Join(dispatcher.errs...)
	}

	ownerIndex, err := dispatcher.findRepositoryRequiredPromptOwner(promptAgentCandidates)
	if err != nil {
		return dispatcher.result, err
	}
	if ownerIndex < 0 {
		ownerIndex = dispatcher.dispatchRepositoryRequiredPromptOwner(promptAgentCandidates)
	}
	if ownerIndex >= 0 {
		dispatcher.markRepositoryRequiredPromptDuplicates(promptAgentCandidates, ownerIndex)
	}
	promptCandidateIndexes := make(map[int]struct{}, len(promptAgentCandidates))
	for _, index := range promptAgentCandidates {
		promptCandidateIndexes[index] = struct{}{}
	}
	dispatcher.dispatchAcceptedExcept(promptCandidateIndexes)
	return dispatcher.result, errors.Join(dispatcher.errs...)
}

type admissionDispatcher struct {
	ctx      context.Context
	service  *Service
	reserved *ReservationResult
	result   *AdmissionResult
	errs     []error
}

func newAdmissionDispatcher(ctx context.Context, service *Service, reserved *ReservationResult) *admissionDispatcher {
	result := &AdmissionResult{
		Event:     cloneEvent(reserved.Event),
		Replayed:  reserved.Replayed,
		EventType: reserved.Event.EventType,
		RouteKey:  reserved.Event.RouteKey,
		Origin:    reserved.Event.Origin,
		HopDepth:  reserved.Event.HopDepth,
	}
	result.Deliveries = make([]*Delivery, len(reserved.Deliveries))
	for index, item := range reserved.Deliveries {
		result.Deliveries[index] = cloneDelivery(item.Delivery)
	}
	return &admissionDispatcher{ctx: ctx, service: service, reserved: reserved, result: result}
}

func (dispatcher *admissionDispatcher) dispatchOne(index int, item ReservedDelivery) *Delivery {
	transitioned, err := dispatcher.service.dispatchReserved(dispatcher.ctx, dispatcher.reserved, item)
	if transitioned != nil {
		dispatcher.result.Deliveries[index] = cloneDelivery(transitioned)
	}
	if err != nil {
		dispatcher.errs = append(dispatcher.errs, err)
	}
	return transitioned
}

func (dispatcher *admissionDispatcher) findRepositoryRequiredPromptOwner(candidates []int) (int, error) {
	// Current progress wins over the immutable receipt order. In particular, a
	// later binding that already owns a run/retry must prevent an earlier stale
	// Accepted receipt from creating a second run during replay healing.
	ownerIndex, existingRunID := -1, ""
	for _, index := range candidates {
		current := dispatcher.result.Deliveries[index]
		if current.DriverRunID == "" {
			continue
		}
		if existingRunID != "" && current.DriverRunID != existingRunID {
			return -1, ErrInvalidPersistedState
		}
		existingRunID = current.DriverRunID
		if ownerIndex < 0 {
			ownerIndex = index
		}
	}
	if ownerIndex < 0 {
		for _, index := range candidates {
			current := dispatcher.result.Deliveries[index]
			if current.Status != DeliveryAccepted && promptDeliveryOwnsRepositoryRequiredFanout(current) {
				return index, nil
			}
		}
	}
	return ownerIndex, nil
}

func (dispatcher *admissionDispatcher) dispatchRepositoryRequiredPromptOwner(candidates []int) int {
	for _, index := range candidates {
		if dispatcher.result.Deliveries[index].Status != DeliveryAccepted {
			continue
		}
		transitioned := dispatcher.dispatchOne(index, dispatcher.reserved.Deliveries[index])
		if transitioned == nil || promptDeliveryOwnsRepositoryRequiredFanout(transitioned) {
			return index
		}
	}
	return -1
}

func (dispatcher *admissionDispatcher) markRepositoryRequiredPromptDuplicates(candidates []int, ownerIndex int) {
	for _, index := range candidates {
		if index == ownerIndex || dispatcher.result.Deliveries[index].Status != DeliveryAccepted {
			continue
		}
		transitioned, err := dispatcher.service.markRepositoryRequiredPromptAgentDuplicate(
			dispatcher.ctx, dispatcher.reserved.Event.WorkspaceKey, dispatcher.reserved.Deliveries[index],
		)
		if transitioned != nil {
			dispatcher.result.Deliveries[index] = cloneDelivery(transitioned)
		}
		if err != nil {
			dispatcher.errs = append(dispatcher.errs, err)
		}
	}
}

func (dispatcher *admissionDispatcher) dispatchAcceptedExcept(skip map[int]struct{}) {
	for index, item := range dispatcher.reserved.Deliveries {
		if _, skipped := skip[index]; skipped || item.Delivery.Status != DeliveryAccepted {
			continue
		}
		dispatcher.dispatchOne(index, item)
	}
}

// refreshReplayedDeliveries overlays only mutable progress from the current
// Delivery rows onto Fleet's intentionally immutable admission receipt. This
// lets a crash-after-reservation Accepted leg heal while Failed/Held work stays
// in the retry lane and terminal work remains a no-op on event replay.

func (s *Service) refreshReplayedDeliveries(ctx context.Context, reserved *ReservationResult) (*ReservationResult, error) {
	if s == nil || s.deliveries == nil || reserved == nil || reserved.Event == nil {
		return nil, ErrUnavailable
	}
	refreshed := *reserved
	refreshed.Deliveries = make([]ReservedDelivery, len(reserved.Deliveries))
	for index, item := range reserved.Deliveries {
		receipt := item.Delivery
		if receipt == nil {
			return nil, ErrInvalidPersistedState
		}
		current, err := s.deliveries.GetDelivery(ctx, reserved.Event.WorkspaceKey, receipt.DeliveryID)
		if err != nil {
			return nil, fmt.Errorf("refresh replayed delivery %q: %w", receipt.DeliveryID, err)
		}
		if err := validatePersistedDelivery(current, reserved.Event.WorkspaceKey, receipt.DeliveryID, reserved.Event.EventID); err != nil {
			return nil, err
		}
		if !sameDeliveryImmutableIdentity(receipt, current) || current.Attempt < receipt.Attempt ||
			current.UpdatedAt.Before(receipt.UpdatedAt) {
			return nil, ErrInvalidPersistedState
		}
		if item.Target == nil {
			if receipt.Status != DeliveryRejected || current.Status != DeliveryRejected {
				return nil, ErrInvalidPersistedState
			}
		} else if !deliveryMatchesTarget(current, item.Target) {
			return nil, ErrInvalidPersistedState
		}
		delivery := cloneDelivery(receipt)
		delivery.Status = current.Status
		delivery.RejectionReason = current.RejectionReason
		delivery.DriverRunID = current.DriverRunID
		delivery.Attempt = current.Attempt
		delivery.NextRetryAt = nil
		if current.NextRetryAt != nil {
			nextRetryAt := *current.NextRetryAt
			delivery.NextRetryAt = &nextRetryAt
		}
		delivery.ErrorClass = current.ErrorClass
		delivery.UpdatedAt = current.UpdatedAt
		refreshed.Deliveries[index] = ReservedDelivery{Delivery: delivery, Target: item.Target}
	}
	return &refreshed, nil
}

func sameDeliveryImmutableIdentity(receipt, current *Delivery) bool {
	return receipt != nil && current != nil &&
		receipt.WorkspaceKey == current.WorkspaceKey &&
		receipt.DeliveryID == current.DeliveryID &&
		receipt.TriggerEventID == current.TriggerEventID &&
		receipt.TriggerBindingID == current.TriggerBindingID &&
		receipt.SubjectKey == current.SubjectKey &&
		receipt.DriverID == current.DriverID &&
		receipt.DriverVersionID == current.DriverVersionID &&
		receipt.TargetEntrypoint == current.TargetEntrypoint &&
		receipt.TargetAgentServiceID == current.TargetAgentServiceID &&
		receipt.SourceKind == current.SourceKind &&
		receipt.ConcurrencyPolicy == current.ConcurrencyPolicy &&
		receipt.RetryMaxAttempts == current.RetryMaxAttempts &&
		receipt.RetryBackoffSeconds == current.RetryBackoffSeconds &&
		receipt.CreatedAt.Equal(current.CreatedAt)
}

// repositoryRequiredPromptAgentCandidates identifies only the explanatory
// repository-required outcome for task.ready. A repository-less task cannot
// be claimed by any prompt-agent role, so dispatching every matching role would
// create several identical skipped DriverRuns. Candidate order is stable across
// fresh admission, concurrent replay, and partial progress; ordinary task.ready
// events retain their full role fanout.
func repositoryRequiredPromptAgentCandidates(reserved *ReservationResult) []int {
	if reserved == nil || !repositoryRequiredTaskReadyEvent(reserved.Event, reserved.Payload) {
		return nil
	}
	candidates := make([]int, 0, len(reserved.Deliveries))
	for index, item := range reserved.Deliveries {
		delivery := item.Delivery
		// A target proves the leg survived actor filtering and carries the
		// immutable Catalog guard. An actor-filtered prompt binding must not
		// become the winner and suppress every actually dispatchable role.
		if delivery == nil || item.Target == nil || delivery.DriverID != promptAgentDriverID {
			continue
		}
		candidates = append(candidates, index)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return reserved.Deliveries[candidates[i]].Delivery.TriggerBindingID <
			reserved.Deliveries[candidates[j]].Delivery.TriggerBindingID
	})
	return candidates
}

func repositoryRequiredTaskReadyEvent(event *Event, payload json.RawMessage) bool {
	if event == nil || event.SourceKind != SourceKindInternal || event.EventType != taskReadyEventType ||
		event.RouteKey != taskReadyRouteKey {
		return false
	}
	var facts struct {
		RepositoryRequired bool `json:"repositoryRequired"`
	}
	if err := json.Unmarshal(payload, &facts); err != nil {
		return false
	}
	return facts.RepositoryRequired
}

// A terminal rejection/duplicate or exhausted dispatch failure created no run
// and owns no retry lease, so the next prompt binding may still provide the one
// explanatory DriverRun. A run, held delivery, or scheduled retry owns this
// event's prompt-agent fanout and lets the remaining accepted legs collapse.
func promptDeliveryOwnsRepositoryRequiredFanout(delivery *Delivery) bool {
	if delivery == nil {
		return true
	}
	if delivery.DriverRunID != "" || delivery.NextRetryAt != nil {
		return true
	}
	switch delivery.Status {
	case DeliveryFailed:
		return delivery.ErrorClass != DeliveryErrorRetriesExhausted
	case DeliveryRejected, DeliveryDuplicate:
		return false
	default:
		return true
	}
}

func (s *Service) markRepositoryRequiredPromptAgentDuplicate(
	ctx context.Context,
	workspace string,
	item ReservedDelivery,
) (*Delivery, error) {
	if item.Delivery == nil || item.Target == nil {
		return nil, ErrInvalidPersistedState
	}
	if item.Delivery.WorkspaceKey != workspace {
		return nil, ErrWrongWorkspace
	}
	return s.markRepositoryRequiredPromptAgentDeliveryDuplicate(ctx, item.Delivery, item.Target)
}

func (s *Service) markRepositoryRequiredPromptAgentDeliveryDuplicate(
	ctx context.Context,
	delivery *Delivery,
	target *DispatchTarget,
) (*Delivery, error) {
	if delivery == nil || target == nil || !deliveryMatchesTarget(delivery, target) {
		return nil, ErrInvalidPersistedState
	}
	transition := initialDeliveryTransition(delivery.WorkspaceKey, delivery)
	transition.Status = DeliveryDuplicate
	transition.IdempotencyKey = deliveryTransitionKey(transition)
	return s.transitionDelivery(ctx, transition, target)
}
