package automation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type resolvedTarget struct {
	driverID       string
	versionID      string
	driverRevision uint64
	sourceDigest   string
	bundleDigest   string
}

const (
	taskReadyEventType                   = "task.ready"
	taskReadyRouteKey                    = "internal.task.ready"
	promptAgentDriverID                  = "prompt-agent"
	taskReadyReconcileSourceEventPrefix  = "task-ready-reconcile-"
	taskReadyExhaustedRecoveryHashPrefix = "task-ready-reconcile-exhausted-recovery-v1-"
	deliveryDispatchHashPrefix           = "delivery-dispatch:sha256:"
	internalAdmissionHashPrefix          = "internal:sha256:"
)

func (s *Service) AdmitWebhookEvent(ctx context.Context, auth authority.WebhookAuthority, event WebhookEvent) (*AdmissionResult, error) {
	workspace, err := normalizeRequired("workspace", event.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.Admit(ActionAdmitEvent, workspace, auth); err != nil {
		return nil, err
	}
	derived, err := deriveWebhookAdmission(auth, event)
	if err != nil {
		return nil, err
	}
	return s.admitEventAuthorized(ctx, derived)
}

func (s *Service) AdmitWorkflowEvent(ctx context.Context, auth authority.ExecutionAuthority, event WorkflowEvent) (*AdmissionResult, error) {
	workspace, err := normalizeRequired("workspace", event.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.Admit(ActionAdmitEvent, workspace, auth); err != nil {
		return nil, err
	}
	derived, err := s.deriveWorkflowAdmission(ctx, auth, event)
	if err != nil {
		return nil, err
	}
	return s.admitEventAuthorized(ctx, derived)
}

func (s *Service) AdmitSystemEvent(ctx context.Context, auth authority.SystemAuthority, event SystemEvent) (*AdmissionResult, error) {
	workspace, err := normalizeRequired("workspace", event.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.authority.Admit(ActionAdmitEvent, workspace, auth); err != nil {
		return nil, err
	}
	derived, err := s.deriveSystemAdmission(ctx, auth, event)
	if err != nil {
		return nil, err
	}
	return s.admitEventAuthorized(ctx, derived)
}

// admitEventAuthorized is Automation's single private admission-policy
// implementation. Origin adapters can supply only an already-derived value;
// callers outside this package cannot construct or exchange provenance.
func (s *Service) admitEventAuthorized(ctx context.Context, derived *derivedAdmission) (*AdmissionResult, error) {
	if s == nil || s.matcher == nil || s.events == nil || s.admissions == nil || s.execution == nil || s.eventTrustPolicy == nil {
		return nil, ErrUnavailable
	}

	if err := validateEventAdmissionTrust(s.eventTrustPolicy, derived); err != nil {
		return nil, err
	}
	if derived.hopDepth > s.hopDepthCap {
		return &AdmissionResult{
			Dropped:    true,
			DropReason: DropReasonHopDepthExceeded,
			EventType:  derived.eventType,
			RouteKey:   derived.routeKey,
			Origin:     derived.origin,
			HopDepth:   derived.hopDepth,
		}, nil
	}

	if replayed, err := s.probeAdmissionReplay(ctx, derived); err == nil {
		return s.dispatchReplayedAdmission(ctx, derived, replayed)
	} else if !errors.Is(err, ErrAdmissionReplayNotFound) {
		return nil, err
	}

	matched, err := s.matchBindings(ctx, derived.workspace, derived.routeKey)
	if err != nil {
		return s.recheckAdmissionReplay(ctx, derived, err)
	}
	bindings := matched.Bindings
	if len(bindings) == 0 {
		return s.recheckAdmissionReplay(ctx, derived, errors.Join(ErrNoMatchingBinding, ErrNotFound))
	}
	reservation, err := s.buildMatchedEventReservation(ctx, derived, matched)
	if err != nil {
		return s.recheckAdmissionReplay(ctx, derived, err)
	}
	reserved, err := s.reserveEvent(ctx, reservation)
	if err != nil {
		return nil, err
	}
	return s.dispatchAdmission(ctx, reserved)
}

func validateEventAdmissionTrust(policy EventTrustPolicy, derived *derivedAdmission) error {
	if policy.EligibleForAdmission(
		derived.eventType, string(derived.origin), derived.sourceKind,
		derived.actorRef, derived.sourceEventID,
	) {
		return nil
	}
	return fmt.Errorf(
		"event type %q provenance %s/%s/%s is reserved: %w",
		derived.eventType, derived.origin, derived.sourceKind, derived.actorRef, ErrInvalid,
	)
}

func (s *Service) probeAdmissionReplay(ctx context.Context, derived *derivedAdmission) (*ReservationResult, error) {
	reservation, err := admissionReservation(derived, "", true)
	if err != nil {
		return nil, err
	}
	reserved, err := s.reserveEvent(ctx, reservation)
	if err != nil {
		return nil, err
	}
	if !reserved.Replayed {
		return nil, ErrInvalidPersistedState
	}
	return reserved, nil
}

func (s *Service) recheckAdmissionReplay(
	ctx context.Context,
	derived *derivedAdmission,
	preflightErr error,
) (*AdmissionResult, error) {
	replayed, err := s.probeAdmissionReplay(ctx, derived)
	if err == nil {
		return s.dispatchReplayedAdmission(ctx, derived, replayed)
	}
	if errors.Is(err, ErrAdmissionReplayNotFound) {
		return nil, preflightErr
	}
	return nil, err
}

// dispatchReplayedAdmission gives a current Ready task one bounded recovery
// generation when its synthetic startup generation has permanently exhausted
// without creating a DriverRun. The recovery source id is deterministic, so
// concurrent/repeated startup replays converge on the same reservation. It is
// deliberately one-shot: the configured delivery retry budget remains the
// authority after the recovery generation, and serve restarts cannot turn into
// an unbounded retry mechanism.
func (s *Service) dispatchReplayedAdmission(
	ctx context.Context,
	derived *derivedAdmission,
	replayed *ReservationResult,
) (*AdmissionResult, error) {
	result, err := s.dispatchAdmission(ctx, replayed)
	if err != nil || !exhaustedTaskReadyReplayNeedsRecovery(result) {
		return result, err
	}
	recovery := *derived
	recovery.sourceEventID = taskReadyExhaustedRecoverySourceEventID(result.Event.SourceEventID)
	recovery.idempotencyKey = InternalEventIdempotencyKey(recovery.workspace, recovery.sourceEventID)
	recovery.payload = cloneRawMessage(derived.payload)
	recovery.subjectAttrs = cloneStringMap(derived.subjectAttrs)
	return s.admitEventAuthorized(ctx, &recovery)
}

func exhaustedTaskReadyReplayNeedsRecovery(result *AdmissionResult) bool {
	if result == nil || !result.Replayed || result.Event == nil ||
		result.Event.Origin != EventOriginSystem ||
		!repositoryRequiredTaskReadyEvent(result.Event, result.Event.Payload) {
		return false
	}
	sourceEventID := strings.TrimSpace(result.Event.SourceEventID)
	if !strings.HasPrefix(sourceEventID, taskReadyReconcileSourceEventPrefix) ||
		isTaskReadyExhaustedRecoverySourceEventID(sourceEventID) || len(result.Deliveries) == 0 {
		return false
	}

	exhaustedPromptAgent := false
	for _, delivery := range result.Deliveries {
		if delivery == nil || strings.TrimSpace(delivery.DriverRunID) != "" || delivery.NextRetryAt != nil {
			return false
		}
		switch delivery.Status {
		case DeliveryFailed:
			if delivery.ErrorClass != DeliveryErrorRetriesExhausted {
				return false
			}
			if delivery.DriverID == promptAgentDriverID {
				exhaustedPromptAgent = true
			}
		case DeliveryDuplicate, DeliveryRejected:
			// Terminal no-run siblings do not own this generation. In particular,
			// repository-required fanout deliberately marks all but one prompt role
			// duplicate, so the sole owner can still qualify after exhausting.
		default:
			// Accepted/Held/retryable work or any run-bearing terminal state still
			// has (or had) an owner; never fork a second generation around it.
			return false
		}
	}
	return exhaustedPromptAgent
}

func isTaskReadyExhaustedRecoverySourceEventID(sourceEventID string) bool {
	return strings.HasPrefix(sourceEventID, taskReadyExhaustedRecoveryHashPrefix)
}

func taskReadyExhaustedRecoverySourceEventID(sourceEventID string) string {
	sum := sha256.Sum256([]byte(sourceEventID))
	return taskReadyExhaustedRecoveryHashPrefix + hex.EncodeToString(sum[:])
}

func (s *Service) buildMatchedEventReservation(ctx context.Context, derived *derivedAdmission, matched *BindingMatchSnapshot) (EventReservation, error) {
	bindings := matched.Bindings
	reservation, err := admissionReservation(derived, bindings[0].BindingID, false)
	if err != nil {
		return EventReservation{}, err
	}
	event := reservation.Event
	reservations, guards, err := s.buildDeliveryReservations(ctx, event, bindings)
	if err != nil {
		return EventReservation{}, err
	}
	reservation.Deliveries = cloneDeliveryReservations(reservations)
	reservation.MatchedBindingIDs = bindingIDs(bindings)
	reservation.BindingSetRevision = matched.BindingSetRevision
	reservation.CatalogGuards = append([]CatalogGuard(nil), guards...)
	return reservation, nil
}

func admissionReservation(derived *derivedAdmission, bindingID string, replayOnly bool) (EventReservation, error) {
	event := &Event{
		WorkspaceKey:     derived.workspace,
		TriggerBindingID: bindingID,
		SourceKind:       derived.sourceKind,
		SourceEventID:    derived.sourceEventID,
		EventType:        derived.eventType,
		RouteKey:         derived.routeKey,
		SubjectRef:       derived.subjectRef,
		ActorRef:         derived.actorRef,
		EmittingRunID:    derived.emittingRunID,
		ParentEventID:    derived.parentEventID,
		EpicID:           derived.epicID,
		Origin:           derived.origin,
		HopDepth:         derived.hopDepth,
		OccurredAt:       derived.occurredAt,
		ReceivedAt:       time.Time{},
		IdempotencyKey:   derived.idempotencyKey,
		RawPayloadRef:    derived.rawPayloadRef,
		RawPayloadDigest: derived.rawPayloadDigest,
		SignatureStatus:  derived.signatureStatus,
		Payload:          cloneRawMessage(derived.payload),
		SubjectAttrs:     cloneStringMap(derived.subjectAttrs),
	}
	reservation := EventReservation{
		Event:            cloneEvent(event),
		ReplayOnly:       replayOnly,
		Payload:          cloneRawMessage(derived.payload),
		SubjectAttrs:     cloneStringMap(derived.subjectAttrs),
		EpicID:           derived.epicID,
		ExecutionNodeID:  derived.executionNodeID,
		ExecutionLeaseID: derived.executionLeaseID,
		ExecutionFence:   derived.executionFence,
	}
	fingerprint, err := eventFingerprint(reservation)
	if err != nil {
		return EventReservation{}, err
	}
	reservation.Fingerprint = fingerprint
	return reservation, nil
}

func (s *Service) reserveEvent(ctx context.Context, reservation EventReservation) (*ReservationResult, error) {
	event := reservation.Event
	reserved, err := s.admissions.ReserveEvent(ctx, reservation)
	if err != nil {
		return nil, fmt.Errorf("reserve event %q: %w", event.IdempotencyKey, err)
	}
	if err := validateReservationResult(reserved, reservation); err != nil {
		return nil, err
	}
	return reserved, nil
}

func (s *Service) buildDeliveryReservations(ctx context.Context, event *Event, bindings []*Binding) ([]DeliveryReservation, []CatalogGuard, error) {
	reservations := make([]DeliveryReservation, 0, len(bindings))
	guards := make([]CatalogGuard, 0, len(bindings))
	cache := make(map[string]resolvedTarget)
	for _, binding := range bindings {
		reservation := DeliveryReservation{
			BindingID:  binding.BindingID,
			Status:     DeliveryAccepted,
			SubjectKey: renderDeliverySubjectKey(binding, event),
		}
		if unsafeLegacyWorkflowIssueBinding(binding, event) || actorFiltered(binding.ActorFilter, event.Origin, event.ActorRef) {
			reservation.Status = DeliveryRejected
			reservation.RejectionReason = RejectionReasonActorFilter
			reservations = append(reservations, reservation)
			continue
		}
		resolved, err := s.resolveDeliveryTarget(ctx, event.WorkspaceKey, binding.DriverID, cache)
		if err != nil {
			return nil, nil, err
		}
		reservation.Target = dispatchTargetFor(binding, resolved)
		guards = append(guards, catalogGuardFor(binding.BindingID, resolved))
		reservations = append(reservations, reservation)
	}
	return reservations, guards, nil
}

func renderDeliverySubjectKey(binding *Binding, event *Event) string {
	subjectKey, err := renderSubjectKey(binding.SubjectKeyTemplate, subjectInputs{
		bindingID: binding.BindingID, eventType: event.EventType, subjectRef: event.SubjectRef,
		actorRef: event.ActorRef, attrs: event.SubjectAttrs,
	})
	if err != nil {
		return defaultSubjectKey(binding.BindingID, event.SubjectRef)
	}
	return subjectKey
}

func (s *Service) resolveDeliveryTarget(ctx context.Context, workspace, driverID string, cache map[string]resolvedTarget) (resolvedTarget, error) {
	if resolved, ok := cache[driverID]; ok {
		return resolved, nil
	}
	effective, err := s.resolveEffectiveVersion(ctx, workspace, driverID, "automation event delivery")
	if err != nil {
		return resolvedTarget{}, err
	}
	resolved := resolvedTarget{
		driverID: effective.Driver.DriverID, versionID: effective.Version.VersionID,
		driverRevision: effective.Driver.Revision, sourceDigest: effective.Version.SourceDigest,
		bundleDigest: effective.Version.BundleDigest,
	}
	cache[driverID] = resolved
	return resolved, nil
}

func dispatchTargetFor(binding *Binding, resolved resolvedTarget) *DispatchTarget {
	return &DispatchTarget{
		DriverID: resolved.driverID, DriverVersionID: resolved.versionID,
		DriverRevision: resolved.driverRevision, SourceDigest: resolved.sourceDigest,
		BundleDigest: resolved.bundleDigest, Entrypoint: binding.TargetEntrypoint,
		TargetAgentServiceID: binding.TargetAgentServiceID,
		SourceKind:           binding.SourceKind, SourceRef: binding.SourceRef, BindingID: binding.BindingID,
		ConcurrencyPolicy: binding.ConcurrencyPolicy, RetryMaxAttempts: binding.RetryMaxAttempts,
		RetryBackoff: time.Duration(binding.RetryBackoffSeconds) * time.Second,
	}
}

func catalogGuardFor(bindingID string, resolved resolvedTarget) CatalogGuard {
	return CatalogGuard{
		BindingID: bindingID, DriverID: resolved.driverID, VersionID: resolved.versionID,
		DriverRevision: resolved.driverRevision, SourceDigest: resolved.sourceDigest, BundleDigest: resolved.bundleDigest,
	}
}

func (s *Service) matchBindings(ctx context.Context, workspace, routeKey string) (*BindingMatchSnapshot, error) {
	snapshot, err := s.matcher.MatchBindings(ctx, workspace, routeKey)
	if err != nil {
		return nil, fmt.Errorf("snapshot binding matches: %w", err)
	}
	if snapshot == nil || snapshot.BindingSetRevision == 0 || snapshot.RouteKey != routeKey {
		return nil, ErrInvalidPersistedState
	}
	if err := validateWorkspace(snapshot.WorkspaceKey, workspace); err != nil {
		return nil, err
	}
	var exact *Binding
	patternMatches := make([]*Binding, 0, len(snapshot.Bindings))
	seen := make(map[string]struct{}, len(snapshot.Bindings))
	for _, candidate := range snapshot.Bindings {
		if err := validatePersistedBinding(candidate, workspace, ""); err != nil {
			return nil, err
		}
		if !candidate.Enabled {
			continue
		}
		if _, duplicate := seen[candidate.BindingID]; duplicate {
			return nil, ErrInvalidPersistedState
		}
		seen[candidate.BindingID] = struct{}{}
		if candidate.RouteKey == routeKey {
			if exact != nil {
				return nil, ErrInvalidPersistedState
			}
			exact = cloneBinding(candidate)
			continue
		}
		if matchAny(candidate.EventTypePatterns, routeKey) {
			patternMatches = append(patternMatches, cloneBinding(candidate))
		}
	}
	sort.Slice(patternMatches, func(i, j int) bool {
		return patternMatches[i].BindingID < patternMatches[j].BindingID
	})
	if exact == nil {
		return &BindingMatchSnapshot{
			WorkspaceKey: workspace, RouteKey: routeKey, BindingSetRevision: snapshot.BindingSetRevision,
			Bindings: patternMatches,
		}, nil
	}
	return &BindingMatchSnapshot{
		WorkspaceKey: workspace, RouteKey: routeKey, BindingSetRevision: snapshot.BindingSetRevision,
		Bindings: append([]*Binding{exact}, patternMatches...),
	}, nil
}

func (s *Service) dispatchReserved(ctx context.Context, reserved *ReservationResult, item ReservedDelivery) (*Delivery, error) {
	if item.Target == nil {
		return nil, ErrInvalidPersistedState
	}
	request := reservedDispatchRequest(reserved, item)
	if err := validateExecutionDispatchRequest(request); err != nil {
		return nil, err
	}
	dispatch, err := s.execution.Dispatch(ctx, request)
	transition := initialDeliveryTransition(reserved.Event.WorkspaceKey, item.Delivery)
	if err != nil {
		return s.recordInitialDispatchFailure(ctx, item, transition, err)
	}
	return validateCommittedDispatchResult(request, dispatch, item.Target)
}

func reservedDispatchRequest(reserved *ReservationResult, item ReservedDelivery) ExecutionDispatchRequest {
	event := reserved.Event
	delivery := item.Delivery
	return ExecutionDispatchRequest{
		WorkspaceKey:            event.WorkspaceKey,
		IdempotencyKey:          DeliveryDispatchIdempotencyKey(event.IdempotencyKey, delivery.TriggerBindingID),
		ExpectedDeliveryStatus:  delivery.Status,
		ExpectedDeliveryAttempt: delivery.Attempt,
		DriverID:                item.Target.DriverID,
		DriverVersionID:         item.Target.DriverVersionID,
		DriverRevision:          item.Target.DriverRevision,
		SourceDigest:            item.Target.SourceDigest,
		BundleDigest:            item.Target.BundleDigest,
		Entrypoint:              item.Target.Entrypoint,
		TargetAgentServiceID:    item.Target.TargetAgentServiceID,
		DeliveryID:              delivery.DeliveryID,
		SourceKind:              item.Target.SourceKind,
		SourceRef:               event.EventID,
		SubjectRef:              event.SubjectRef,
		TriggerBindingID:        delivery.TriggerBindingID,
		SubjectKey:              delivery.SubjectKey,
		ConcurrencyPolicy:       item.Target.ConcurrencyPolicy,
		EpicID:                  reserved.EpicID,
		ActorRef:                event.ActorRef,
		RawPayloadRef:           event.RawPayloadRef,
		Payload:                 cloneRawMessage(reserved.Payload),
		SubjectAttrs:            cloneStringMap(reserved.SubjectAttrs),
	}
}

// DeliveryDispatchIdempotencyKey derives the canonical per-binding execution
// identity for one admitted delivery. It is the Automation-owned policy used
// by delivery adapters: incomplete event/binding coordinates stay absent and
// every complete identity uses the same bounded digest representation.
func DeliveryDispatchIdempotencyKey(eventIdempotencyKey, bindingID string) string {
	if eventIdempotencyKey == "" || bindingID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(eventIdempotencyKey + "#" + bindingID))
	return deliveryDispatchHashPrefix + hex.EncodeToString(sum[:])
}

func initialDeliveryTransition(workspace string, delivery *Delivery) DeliveryTransition {
	return DeliveryTransition{
		WorkspaceKey:    workspace,
		DeliveryID:      delivery.DeliveryID,
		ExpectedStatus:  delivery.Status,
		ExpectedAttempt: delivery.Attempt,
		Attempt:         delivery.Attempt,
	}
}

func validateCommittedDispatchResult(
	request ExecutionDispatchRequest,
	dispatch *ExecutionDispatchResult,
	target *DispatchTarget,
) (*Delivery, error) {
	if dispatch == nil || dispatch.Delivery == nil {
		return nil, ErrInvalidPersistedState
	}
	delivery := dispatch.Delivery
	if err := validatePersistedDelivery(delivery, request.WorkspaceKey, request.DeliveryID, request.SourceRef); err != nil {
		return nil, err
	}
	if delivery.TriggerBindingID != request.TriggerBindingID || delivery.SubjectKey != request.SubjectKey ||
		delivery.Attempt != request.ExpectedDeliveryAttempt || !deliveryMatchesTarget(delivery, target) {
		return nil, ErrInvalidPersistedState
	}
	var err error
	if dispatch.Busy {
		err = validateCommittedBusyDispatch(request, dispatch, delivery)
	} else {
		err = validateCommittedRunDispatch(request, dispatch, delivery)
	}
	if err != nil {
		return nil, err
	}
	return cloneDelivery(delivery), nil
}

func validateCommittedBusyDispatch(
	request ExecutionDispatchRequest,
	dispatch *ExecutionDispatchResult,
	delivery *Delivery,
) error {
	if strings.TrimSpace(dispatch.RunID) != "" || strings.TrimSpace(delivery.DriverRunID) != "" ||
		strings.TrimSpace(dispatch.BusyRunID) == "" || dispatch.BusyRunID != strings.TrimSpace(dispatch.BusyRunID) {
		return ErrInvalidPersistedState
	}
	switch request.ConcurrencyPolicy {
	case ConcurrencyQueue:
		if delivery.Status != DeliveryHeld || delivery.NextRetryAt == nil {
			return ErrInvalidPersistedState
		}
	case ConcurrencyForbid:
		if delivery.Status != DeliveryRejected || delivery.RejectionReason != RejectionConcurrencyForbid {
			return ErrInvalidPersistedState
		}
	default:
		return ErrInvalidPersistedState
	}
	return nil
}

func validateCommittedRunDispatch(
	request ExecutionDispatchRequest,
	dispatch *ExecutionDispatchResult,
	delivery *Delivery,
) error {
	runID := strings.TrimSpace(dispatch.RunID)
	if runID == "" || runID != dispatch.RunID || delivery.DriverRunID != runID || dispatch.BusyRunID != "" {
		return ErrInvalidPersistedState
	}
	if delivery.Status != DeliveryDispatched &&
		(delivery.Status != DeliverySuperseded || request.ConcurrencyPolicy != ConcurrencyReplace) {
		return ErrInvalidPersistedState
	}
	return nil
}

func (s *Service) recordInitialDispatchFailure(ctx context.Context, item ReservedDelivery, transition DeliveryTransition, dispatchErr error) (*Delivery, error) {
	next := s.now().UTC().Add(item.Target.RetryBackoff)
	transition.Status = DeliveryFailed
	transition.ErrorClass = DeliveryErrorDispatchFailed
	transition.NextRetryAt = &next
	transition.IdempotencyKey = deliveryTransitionKey(transition)
	updated, transitionErr := s.transitionDelivery(ctx, transition, item.Target)
	return updated, errors.Join(fmt.Errorf("dispatch delivery %q: %w", item.Delivery.DeliveryID, dispatchErr), transitionErr)
}

func deliveryTransitionKey(transition DeliveryTransition) string {
	return fmt.Sprintf("%s:%s:%d:%s:%d", transition.DeliveryID, transition.ExpectedStatus,
		transition.ExpectedAttempt, transition.Status, transition.Attempt)
}

func (s *Service) transitionDelivery(ctx context.Context, transition DeliveryTransition, target *DispatchTarget) (*Delivery, error) {
	updated, err := s.admissions.TransitionDelivery(ctx, transition)
	if err != nil {
		return nil, fmt.Errorf("transition delivery %q to %q: %w", transition.DeliveryID, transition.Status, err)
	}
	if err := validatePersistedDelivery(updated, transition.WorkspaceKey, transition.DeliveryID, ""); err != nil {
		return nil, err
	}
	if updated.Status != transition.Status || !deliveryMatchesTarget(updated, target) {
		return nil, ErrInvalidPersistedState
	}
	return cloneDelivery(updated), nil
}

func validateReservationResult(result *ReservationResult, requested EventReservation) error {
	if result == nil {
		return ErrInvalidPersistedState
	}
	if requested.ReplayOnly && !result.Replayed {
		return ErrInvalidPersistedState
	}
	if err := validateReservedEvent(result, requested); err != nil {
		return err
	}
	if !result.Replayed {
		if len(result.Deliveries) != len(requested.Deliveries) {
			return ErrInvalidPersistedState
		}
		for index, delivery := range result.Deliveries {
			if !reservedDeliveryMatchesRequest(delivery, requested.Deliveries[index]) {
				return ErrInvalidPersistedState
			}
		}
	}
	return validateReservedDeliveries(result, requested.Event.WorkspaceKey)
}

type reservedEventIdentity struct {
	idempotencyKey string
	sourceKind     string
	sourceEventID  string
	eventType      string
	origin         EventOrigin
	hopDepth       int
	routeKey       string
	subjectRef     string
	actorRef       string
	emittingRunID  string
	parentEventID  string
	epicID         string
	rawPayloadRef  string
	rawDigest      string
	signature      string
}

func reservationIdentity(event *Event) reservedEventIdentity {
	return reservedEventIdentity{
		idempotencyKey: event.IdempotencyKey, sourceKind: event.SourceKind,
		sourceEventID: event.SourceEventID, eventType: event.EventType,
		origin: event.Origin, hopDepth: event.HopDepth, routeKey: event.RouteKey,
		subjectRef: event.SubjectRef, actorRef: event.ActorRef, emittingRunID: event.EmittingRunID,
		parentEventID: event.ParentEventID, epicID: event.EpicID,
		rawPayloadRef: event.RawPayloadRef, rawDigest: event.RawPayloadDigest,
		signature: event.SignatureStatus,
	}
}

func validateReservedEvent(result *ReservationResult, requested EventReservation) error {
	if err := validatePersistedEvent(result.Event, requested.Event.WorkspaceKey, ""); err != nil {
		return err
	}
	if reservationIdentity(result.Event) != reservationIdentity(requested.Event) {
		return ErrInvalidPersistedState
	}
	if requested.Event.TriggerBindingID != "" && result.Event.TriggerBindingID != requested.Event.TriggerBindingID {
		return ErrInvalidPersistedState
	}
	if !requested.Event.OccurredAt.IsZero() && !result.Event.OccurredAt.Equal(requested.Event.OccurredAt) {
		return ErrInvalidPersistedState
	}
	if !bytes.Equal(result.Event.Payload, requested.Event.Payload) ||
		!equalStringMaps(result.Event.SubjectAttrs, requested.Event.SubjectAttrs) {
		return ErrInvalidPersistedState
	}
	if !bytes.Equal(result.Payload, result.Event.Payload) ||
		!equalStringMaps(result.SubjectAttrs, result.Event.SubjectAttrs) || result.EpicID != result.Event.EpicID {
		return ErrInvalidPersistedState
	}
	return nil
}

func reservedDeliveryMatchesRequest(result ReservedDelivery, requested DeliveryReservation) bool {
	if result.Delivery == nil || result.Delivery.TriggerBindingID != requested.BindingID ||
		result.Delivery.Status != requested.Status || result.Delivery.SubjectKey != requested.SubjectKey ||
		result.Delivery.RejectionReason != requested.RejectionReason {
		return false
	}
	if requested.Target == nil {
		return result.Target == nil
	}
	if result.Target == nil {
		return false
	}
	return result.Target.DriverID == requested.Target.DriverID &&
		result.Target.DriverVersionID == requested.Target.DriverVersionID &&
		result.Target.DriverRevision == requested.Target.DriverRevision &&
		result.Target.SourceDigest == requested.Target.SourceDigest &&
		result.Target.BundleDigest == requested.Target.BundleDigest &&
		result.Target.Entrypoint == requested.Target.Entrypoint &&
		result.Target.TargetAgentServiceID == requested.Target.TargetAgentServiceID &&
		result.Target.SourceKind == requested.Target.SourceKind &&
		result.Target.BindingID == requested.Target.BindingID &&
		result.Target.ConcurrencyPolicy == requested.Target.ConcurrencyPolicy &&
		result.Target.RetryMaxAttempts == requested.Target.RetryMaxAttempts &&
		result.Target.RetryBackoff == requested.Target.RetryBackoff
}

func validateReservedDeliveries(result *ReservationResult, workspace string) error {
	for _, item := range result.Deliveries {
		if err := validatePersistedDelivery(item.Delivery, workspace, "", result.Event.EventID); err != nil {
			return err
		}
		if item.Delivery.Status == DeliveryAccepted {
			if item.Target == nil || !deliveryMatchesTarget(item.Delivery, item.Target) {
				return ErrInvalidPersistedState
			}
		}
	}
	return nil
}

func deliveryMatchesTarget(delivery *Delivery, target *DispatchTarget) bool {
	if delivery == nil || target == nil {
		return false
	}
	return delivery.DriverID == target.DriverID &&
		delivery.DriverVersionID == target.DriverVersionID &&
		delivery.TargetEntrypoint == target.Entrypoint &&
		delivery.TargetAgentServiceID == target.TargetAgentServiceID &&
		delivery.SourceKind == target.SourceKind &&
		delivery.TriggerBindingID == target.BindingID &&
		delivery.ConcurrencyPolicy == target.ConcurrencyPolicy &&
		delivery.RetryMaxAttempts == target.RetryMaxAttempts &&
		delivery.RetryBackoffSeconds == int(target.RetryBackoff/time.Second)
}

// eventFingerprint excludes execution node/lease/fence because ownership can
// legitimately transfer after the semantic event has committed. EmittingRunID
// and the event content remain immutable replay identity.
func eventFingerprint(reservation EventReservation) (string, error) {
	event := reservation.Event
	stable := struct {
		WorkspaceKey     string            `json:"workspace_key"`
		SourceKind       string            `json:"source_kind"`
		SourceEventID    string            `json:"source_event_id"`
		EventType        string            `json:"event_type"`
		RouteKey         string            `json:"route_key"`
		SubjectRef       string            `json:"subject_ref"`
		ActorRef         string            `json:"actor_ref"`
		EmittingRunID    string            `json:"emitting_run_id"`
		ParentEventID    string            `json:"parent_event_id"`
		Origin           EventOrigin       `json:"origin"`
		HopDepth         int               `json:"hop_depth"`
		OccurredAt       time.Time         `json:"occurred_at"`
		IdempotencyKey   string            `json:"idempotency_key"`
		RawPayloadRef    string            `json:"raw_payload_ref"`
		RawPayloadDigest string            `json:"raw_payload_digest"`
		Payload          json.RawMessage   `json:"payload"`
		SubjectAttrs     map[string]string `json:"subject_attrs"`
		EpicID           string            `json:"epic_id"`
	}{
		WorkspaceKey: event.WorkspaceKey, SourceKind: event.SourceKind,
		SourceEventID: event.SourceEventID, EventType: event.EventType,
		RouteKey: event.RouteKey, SubjectRef: event.SubjectRef, ActorRef: event.ActorRef,
		EmittingRunID: event.EmittingRunID, ParentEventID: event.ParentEventID, Origin: event.Origin,
		HopDepth: event.HopDepth, OccurredAt: event.OccurredAt,
		IdempotencyKey: event.IdempotencyKey, RawPayloadRef: event.RawPayloadRef,
		RawPayloadDigest: event.RawPayloadDigest, Payload: reservation.Payload,
		SubjectAttrs: reservation.SubjectAttrs, EpicID: reservation.EpicID,
	}
	encoded, err := json.Marshal(stable)
	if err != nil {
		return "", fmt.Errorf("fingerprint event: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func actorFiltered(filter *ActorFilter, origin EventOrigin, actor string) bool {
	if filter == nil || filter.IsZero() {
		return false
	}
	kind := eventActorKind(origin, actor)
	for _, excluded := range filter.ExcludeActorKinds {
		if strings.ToLower(strings.TrimSpace(excluded)) == kind {
			return true
		}
	}
	if len(filter.AllowActors) == 0 {
		return false
	}
	actor = strings.TrimSpace(actor)
	for _, allowed := range filter.AllowActors {
		if strings.TrimSpace(allowed) == actor {
			return false
		}
	}
	return true
}

// unsafeLegacyWorkflowIssueBinding fails closed for bindings persisted before
// the create/update invariant required workflow actor exclusion. The binding
// already matched this event, so an actual internal.issue.* route plus a
// workflow actor is sufficient to identify the self-trigger risk. Recording a
// rejected delivery preserves admission/audit behavior without dispatching the
// loop-producing workflow.
func unsafeLegacyWorkflowIssueBinding(binding *Binding, event *Event) bool {
	return binding != nil && event != nil && isInternalIssueRoute(event.RouteKey) &&
		eventActorKind(event.Origin, event.ActorRef) == string(EventOriginWorkflow) &&
		!excludesWorkflowActor(binding.ActorFilter)
}

// eventActorKind classifies the trusted actor identity separately from event
// provenance. Most events use their structurally derived origin directly, but
// issue-journal entries are system-origin roots whose durable actor can still
// identify work performed by a DriverRun or TaskRun. Treating those actors as
// "system" would make exclude_actor_kinds=["workflow"] ineffective and let a
// workflow that creates an issue retrigger itself forever at hop depth zero.
//
// ActorRef is safe to consult here because Automation derives it from sealed
// authority or verified execution context; origin-specific event inputs cannot override
// it for system or workflow admission. The FleetDB reservation boundary uses
// the same canonical classifier when it atomically revalidates actor filters.
func eventActorKind(origin EventOrigin, actor string) string {
	kind := strings.ToLower(strings.TrimSpace(string(origin)))
	if origin != EventOriginSystem {
		return kind
	}
	actor = strings.ToLower(strings.TrimSpace(actor))
	if strings.HasPrefix(actor, "driver-run:") || strings.HasPrefix(actor, "task-run:") {
		return string(EventOriginWorkflow)
	}
	return kind
}

// InternalEventIdempotencyKey derives Automation's canonical identity for an
// internally emitted event.
func InternalEventIdempotencyKey(workspace, sourceEventID string) string {
	sum := sha256.Sum256([]byte(workspace + ":" + sourceEventID))
	return internalAdmissionHashPrefix + hex.EncodeToString(sum[:])
}

func cloneRawMessage(in json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), in...)
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func cloneDeliveryReservations(in []DeliveryReservation) []DeliveryReservation {
	out := make([]DeliveryReservation, len(in))
	for index, item := range in {
		out[index] = item
		if item.Target != nil {
			target := *item.Target
			out[index].Target = &target
		}
	}
	return out
}

func bindingIDs(bindings []*Binding) []string {
	out := make([]string, len(bindings))
	for index, binding := range bindings {
		out[index] = binding.BindingID
	}
	return out
}
