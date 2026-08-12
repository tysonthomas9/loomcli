package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

var (
	ErrAutomationInvalid                       = errors.New("fleetdb: automation invalid request")
	ErrAutomationRouteNotFound                 = errors.New("fleetdb: automation route not found")
	ErrAutomationParentRunNotFound             = errors.New("fleetdb: automation parent run not found")
	ErrAutomationExecutionOwnerConflict        = errors.New("fleetdb: automation execution owner conflict")
	ErrAutomationIdempotencyConflict           = errors.New("fleetdb: automation idempotency conflict")
	ErrAutomationBindingSnapshotConflict       = errors.New("fleetdb: automation binding snapshot conflict")
	ErrAutomationCatalogSnapshotConflict       = errors.New("fleetdb: automation catalog snapshot conflict")
	ErrAutomationHopDepthExceeded              = errors.New("fleetdb: automation hop depth exceeded")
	ErrAutomationCatalogUnavailable            = errors.New("fleetdb: automation catalog unavailable")
	ErrAutomationFanoutLimitExceeded           = errors.New("fleetdb: automation fanout limit exceeded")
	ErrAutomationAdmissionUnavailable          = errors.New("fleetdb: automation admission unavailable")
	ErrAutomationAdmissionReplayNotFound       = errors.New("fleetdb: automation admission replay not found")
	ErrAutomationDeliveryNotFound              = errors.New("fleetdb: automation delivery not found")
	ErrAutomationDeliveryNotDispatchable       = errors.New("fleetdb: automation delivery not dispatchable")
	ErrAutomationDeliveryTransitionConflict    = errors.New("fleetdb: automation delivery transition conflict")
	ErrAutomationPayloadDigestMismatch         = errors.New("fleetdb: automation payload digest mismatch")
	ErrAutomationBindingNotFound               = errors.New("fleetdb: automation binding not found")
	ErrAutomationBindingDispatchReplayNotFound = errors.New("fleetdb: automation binding dispatch replay not found")
	ErrAutomationCronOccurrenceNotFound        = errors.New("fleetdb: automation cron occurrence not found")
	ErrAutomationCronCompletionConflict        = errors.New("fleetdb: automation cron completion conflict")
	ErrAutomationManagedBindingConflict        = errors.New("fleetdb: automation managed binding conflict")
)

type AutomationBindingFilter struct {
	SourceKind           string
	RouteKey             string
	DriverID             string
	TargetAgentServiceID string
	Enabled              *bool
	Limit                int
}

type AutomationManagedBindingSnapshot struct {
	WorkspaceKey                 string
	BindingID                    string
	ExpectedTargetAgentServiceID string
	ExpectedRouteKey             string
	ExpectedCreatedAt            time.Time
	ExpectedUpdatedAt            time.Time
}

type AutomationManagedBindingReplacement struct {
	Expected AutomationManagedBindingSnapshot
	Binding  *domain.TriggerBinding
}

type AutomationUnmanagedBindingSnapshot struct {
	WorkspaceKey      string
	BindingID         string
	ExpectedRouteKey  string
	ExpectedCreatedAt time.Time
	ExpectedUpdatedAt time.Time
}

type AutomationUnmanagedBindingReplacement struct {
	Expected AutomationUnmanagedBindingSnapshot
	Binding  *domain.TriggerBinding
}

type AutomationEventFilter struct {
	BindingID  string
	SourceKind string
	Origin     domain.TriggerEventOrigin
	Limit      int
}

type AutomationDeliveryFilter struct {
	EventID   string
	BindingID string
	Status    domain.TriggerDeliveryStatus
	Limit     int
}

type AutomationBindingMatchSnapshot struct {
	WorkspaceKey       string                   `json:"workspace_key"`
	RouteKey           string                   `json:"route_key"`
	BindingSetRevision uint64                   `json:"binding_set_revision"`
	Bindings           []*domain.TriggerBinding `json:"bindings"`
}

type AutomationCatalogGuard struct {
	BindingID      string `json:"binding_id"`
	DriverID       string `json:"driver_id"`
	VersionID      string `json:"version_id"`
	DriverRevision uint64 `json:"driver_revision"`
	SourceDigest   string `json:"source_digest"`
	BundleDigest   string `json:"bundle_digest"`
}

// AutomationEventReservation is serialized only by ingress-specific methods.
// Origin and EmittingRunID select trusted server routes and are never emitted
// in the request body.
type AutomationEventReservation struct {
	WorkspaceKey       string
	RouteKey           string
	IdempotencyKey     string
	ReplayOnly         bool
	Origin             domain.TriggerEventOrigin
	EmittingRunID      string
	NodeID             string
	LeaseID            string
	FencingToken       int64
	BindingSetRevision uint64
	MatchedBindingIDs  []string
	CatalogGuards      []AutomationCatalogGuard
	SourceEventID      string
	EventType          string
	SubjectRef         string
	ActorRef           string
	OccurredAt         time.Time
	RawPayloadRef      string
	RawPayloadDigest   string
	Payload            json.RawMessage
	SubjectAttrs       map[string]string
}

type AutomationReservationResult struct {
	Event             *domain.TriggerEvent
	Deliveries        []*domain.TriggerDelivery
	EffectiveVersions []AutomationCatalogGuard
	Replayed          bool
}

type AutomationClaimedDelivery struct {
	Event    *domain.TriggerEvent
	Delivery *domain.TriggerDelivery
}

type AutomationCronClaim struct {
	WorkspaceKey   string
	IdempotencyKey string
	Before         time.Time
	ClaimUntil     time.Time
	Limit          int
}

type AutomationCronOccurrence struct {
	WorkspaceKey string    `json:"workspace_key"`
	BindingID    string    `json:"binding_id"`
	RouteKey     string    `json:"route_key,omitempty"`
	OccurrenceID string    `json:"occurrence_id"`
	OccurredAt   time.Time `json:"occurred_at"`
}

type AutomationCronCompletionStatus string

const (
	AutomationCronCompletionAdmitted AutomationCronCompletionStatus = "admitted"
	AutomationCronCompletionDropped  AutomationCronCompletionStatus = "dropped"
	AutomationCronCompletionFailed   AutomationCronCompletionStatus = "failed"
)

type AutomationCronCompletion struct {
	WorkspaceKey string
	BindingID    string
	OccurrenceID string
	Status       AutomationCronCompletionStatus
	ErrorClass   string
}

type AutomationDeliveryTransition struct {
	WorkspaceKey    string
	DeliveryID      string
	IdempotencyKey  string
	ExpectedStatus  domain.TriggerDeliveryStatus
	ExpectedAttempt int
	Status          domain.TriggerDeliveryStatus
	DriverRunID     string
	RejectionReason string
	NextRetryAt     *time.Time
	ErrorClass      string
}

// AutomationDeliveryDispatch is the caller-controlled part of FleetDB's
// atomic reserved-delivery dispatch intent. FleetDB reloads all target,
// payload, policy, actor, and provenance fields from the durable reservation.
type AutomationDeliveryDispatch struct {
	WorkspaceKey    string
	DeliveryID      string
	IdempotencyKey  string
	ExpectedStatus  domain.TriggerDeliveryStatus
	ExpectedAttempt int
}

type AutomationBindingDispatch struct {
	WorkspaceKey     string
	BindingID        string
	IdempotencyKey   string
	ReplayOnly       bool
	EffectiveVersion AutomationCatalogGuard
	SubjectRef       string
	EpicID           string
	ActorRef         string
	RawPayloadRef    string
	Payload          json.RawMessage
	SubjectAttrs     map[string]string
}

type AutomationBindingDispatchResult struct {
	DriverRun *domain.DriverRun `json:"driver_run,omitempty"`
	// DriverRunSnapshot preserves the exact committed Fleet response object,
	// including fields unknown to this client version. It is never sent back to Fleet.
	DriverRunSnapshot json.RawMessage                   `json:"-"`
	Outcome           AutomationDeliveryDispatchOutcome `json:"outcome"`
	BusyRunID         string                            `json:"busy_run_id,omitempty"`
	SupersededRunIDs  []string                          `json:"superseded_run_ids,omitempty"`
	RunReused         bool                              `json:"run_reused"`
	Replayed          bool                              `json:"replayed"`
}

type automationBindingDispatchRequestWire struct {
	ReplayOnly       bool                    `json:"replay_only,omitempty"`
	EffectiveVersion *AutomationCatalogGuard `json:"effective_version,omitempty"`
	SubjectRef       string                  `json:"subject_ref,omitempty"`
	EpicID           string                  `json:"epic_id,omitempty"`
	ActorRef         string                  `json:"actor_ref,omitempty"`
	RawPayloadRef    string                  `json:"raw_payload_ref,omitempty"`
	PayloadBase64    []byte                  `json:"payload_base64,omitempty"`
	SubjectAttrs     map[string]string       `json:"subject_attrs,omitempty"`
}

type automationBindingDispatchResponseWire struct {
	DriverRun        json.RawMessage                   `json:"driver_run,omitempty"`
	Outcome          AutomationDeliveryDispatchOutcome `json:"outcome"`
	BusyRunID        string                            `json:"busy_run_id,omitempty"`
	SupersededRunIDs []string                          `json:"superseded_run_ids,omitempty"`
	RunReused        bool                              `json:"run_reused"`
	Replayed         bool                              `json:"replayed"`
}

type AutomationDeliveryDispatchOutcome string

const (
	AutomationDeliveryDispatchRun        AutomationDeliveryDispatchOutcome = "run"
	AutomationDeliveryDispatchBusy       AutomationDeliveryDispatchOutcome = "busy"
	AutomationDeliveryDispatchReused     AutomationDeliveryDispatchOutcome = "reused"
	AutomationDeliveryDispatchSuperseded AutomationDeliveryDispatchOutcome = "superseded"
)

// AutomationDeliveryDispatchResult is FleetDB's committed cross-record
// outcome. Event.Payload contains the exact admission bytes decoded from the
// payload_base64 wire field.
type AutomationDeliveryDispatchResult struct {
	Event            *domain.TriggerEvent
	Delivery         *domain.TriggerDelivery
	DriverRun        *domain.DriverRun
	Outcome          AutomationDeliveryDispatchOutcome
	BusyRunID        string
	SupersededRunIDs []string
	RunReused        bool
	Replayed         bool
}

// AutomationTransport is the process-wide FleetDB client's low-level
// Automation surface. Capability mapping and policy remain in the module-local
// adapter and core respectively.
type AutomationTransport interface {
	CreateBinding(context.Context, *domain.TriggerBinding) (*domain.TriggerBinding, error)
	GetBinding(context.Context, string, string) (*domain.TriggerBinding, error)
	ListBindings(context.Context, string, AutomationBindingFilter) ([]*domain.TriggerBinding, error)
	UpdateBinding(context.Context, *domain.TriggerBinding) (*domain.TriggerBinding, error)
	DeleteBinding(context.Context, string, string) error
	ReplaceUnmanagedBinding(context.Context, AutomationUnmanagedBindingReplacement) (*domain.TriggerBinding, error)
	DeleteUnmanagedBindingIfUnchanged(context.Context, AutomationUnmanagedBindingSnapshot) error
	CreateManagedBinding(context.Context, *domain.TriggerBinding) (*domain.TriggerBinding, error)
	ReplaceManagedBinding(context.Context, AutomationManagedBindingReplacement) (*domain.TriggerBinding, error)
	DeleteManagedBindingIfUnchanged(context.Context, AutomationManagedBindingSnapshot) error
	MatchBindings(context.Context, string, string) (*AutomationBindingMatchSnapshot, error)
	GetEvent(context.Context, string, string) (*domain.TriggerEvent, error)
	ListEvents(context.Context, string, AutomationEventFilter) ([]*domain.TriggerEvent, error)
	GetDelivery(context.Context, string, string) (*domain.TriggerDelivery, error)
	ListDeliveries(context.Context, string, AutomationDeliveryFilter) ([]*domain.TriggerDelivery, error)
	ReserveEvent(context.Context, AutomationEventReservation) (*AutomationReservationResult, error)
	ClaimDueCron(context.Context, AutomationCronClaim) ([]AutomationCronOccurrence, error)
	CompleteCron(context.Context, AutomationCronCompletion) error
	DispatchAutomationBinding(context.Context, AutomationBindingDispatch) (*AutomationBindingDispatchResult, error)
	ClaimDueDeliveries(context.Context, string, string, time.Time, time.Time, int) ([]AutomationClaimedDelivery, error)
	DispatchAutomationDelivery(context.Context, AutomationDeliveryDispatch) (*AutomationDeliveryDispatchResult, error)
	TransitionDelivery(context.Context, AutomationDeliveryTransition) (*domain.TriggerDelivery, error)
}

type automationStore struct{ client *Client }

var _ AutomationTransport = (*automationStore)(nil)

func (s *automationStore) GetEvent(ctx context.Context, workspace, eventID string) (*domain.TriggerEvent, error) {
	var wire automationEventWire
	path := "/api/v1/" + pathEscape(workspace) + "/trigger-events/" + pathEscape(eventID)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &wire); err != nil {
		return nil, err
	}
	return wire.event(), nil
}

func (s *automationStore) ListEvents(ctx context.Context, workspace string, filter AutomationEventFilter) ([]*domain.TriggerEvent, error) {
	query := url.Values{}
	queryValue(query, "trigger_binding_id", filter.BindingID)
	queryValue(query, "source_kind", filter.SourceKind)
	// Fleet's compatibility list route has no origin predicate. Fetch the full
	// already-filtered set before applying origin and limit locally so a server
	// limit cannot hide later matching rows.
	if filter.Origin == "" {
		queryLimit(query, filter.Limit)
	}
	var out struct {
		Events []automationEventWire `json:"trigger_events"`
	}
	path := withQuery("/api/v1/"+pathEscape(workspace)+"/trigger-events", query)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	events := make([]*domain.TriggerEvent, 0, len(out.Events))
	for index := range out.Events {
		event := out.Events[index].event()
		if filter.Origin != "" && event.Origin != filter.Origin {
			continue
		}
		events = append(events, event)
		if filter.Limit > 0 && len(events) == filter.Limit {
			break
		}
	}
	return events, nil
}

func (s *automationStore) GetDelivery(ctx context.Context, workspace, deliveryID string) (*domain.TriggerDelivery, error) {
	var out domain.TriggerDelivery
	path := "/api/v1/" + pathEscape(workspace) + "/trigger-deliveries/" + pathEscape(deliveryID)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *automationStore) ListDeliveries(ctx context.Context, workspace string, filter AutomationDeliveryFilter) ([]*domain.TriggerDelivery, error) {
	query := url.Values{}
	queryValue(query, "trigger_event_id", filter.EventID)
	queryValue(query, "trigger_binding_id", filter.BindingID)
	queryValue(query, "status", string(filter.Status))
	queryLimit(query, filter.Limit)
	var out struct {
		Deliveries []*domain.TriggerDelivery `json:"trigger_deliveries"`
	}
	path := withQuery("/api/v1/"+pathEscape(workspace)+"/trigger-deliveries", query)
	if err := s.client.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Deliveries == nil {
		out.Deliveries = []*domain.TriggerDelivery{}
	}
	return out.Deliveries, nil
}

func (s *automationStore) ReserveEvent(ctx context.Context, reservation AutomationEventReservation) (*AutomationReservationResult, error) {
	path, err := automationAdmissionPath(reservation)
	if err != nil {
		return nil, err
	}
	body := automationAdmissionBody{
		ReplayOnly:         reservation.ReplayOnly,
		BindingSetRevision: reservation.BindingSetRevision,
		MatchedBindingIDs:  append([]string(nil), reservation.MatchedBindingIDs...),
		SourceEventID:      reservation.SourceEventID, EventType: reservation.EventType,
		NodeID: reservation.NodeID, LeaseID: reservation.LeaseID, FencingToken: reservation.FencingToken,
		SubjectRef: reservation.SubjectRef, ActorRef: reservation.ActorRef, OccurredAt: reservation.OccurredAt,
		RawPayloadRef: reservation.RawPayloadRef, RawPayloadDigest: reservation.RawPayloadDigest,
		PayloadBase64: append([]byte(nil), reservation.Payload...), SubjectAttrs: cloneAutomationAttrs(reservation.SubjectAttrs),
	}
	if !reservation.ReplayOnly {
		effectiveVersions := append(make([]AutomationCatalogGuard, 0, len(reservation.CatalogGuards)), reservation.CatalogGuards...)
		body.EffectiveVersions = &effectiveVersions
	}
	var wire automationReservationWire
	headers := map[string]string{"Idempotency-Key": reservation.IdempotencyKey}
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &wire, headers); err != nil {
		return nil, err
	}
	return wire.result(), nil
}

func (s *automationStore) ClaimDueDeliveries(
	ctx context.Context,
	workspace, idempotencyKey string,
	before, claimUntil time.Time,
	limit int,
) ([]AutomationClaimedDelivery, error) {
	body := struct {
		Before     time.Time `json:"before"`
		ClaimUntil time.Time `json:"claim_until"`
		Limit      int       `json:"limit,omitempty"`
	}{Before: before, ClaimUntil: claimUntil, Limit: limit}
	var wire struct {
		Deliveries []automationClaimedDeliveryWire `json:"deliveries"`
		Count      int                             `json:"count"`
	}
	path := "/api/v1/" + pathEscape(workspace) + "/automation/deliveries/claim-due"
	headers := map[string]string{"Idempotency-Key": idempotencyKey}
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &wire, headers); err != nil {
		return nil, err
	}
	if wire.Count != len(wire.Deliveries) {
		return nil, fmt.Errorf("automation claim response count %d does not match %d deliveries: %w", wire.Count, len(wire.Deliveries), ErrAutomationInvalid)
	}
	out := make([]AutomationClaimedDelivery, 0, len(wire.Deliveries))
	for _, item := range wire.Deliveries {
		out = append(out, AutomationClaimedDelivery{Event: item.Event.event(), Delivery: item.Delivery})
	}
	return out, nil
}

func (s *automationStore) ClaimDueCron(ctx context.Context, claim AutomationCronClaim) ([]AutomationCronOccurrence, error) {
	if err := validateAutomationCronClaim(claim); err != nil {
		return nil, err
	}
	body := struct {
		Before     time.Time `json:"before"`
		ClaimUntil time.Time `json:"claim_until"`
		Limit      int       `json:"limit,omitempty"`
	}{Before: claim.Before.UTC(), ClaimUntil: claim.ClaimUntil.UTC(), Limit: claim.Limit}
	var wire struct {
		Occurrences []AutomationCronOccurrence `json:"occurrences"`
		Count       int                        `json:"count"`
	}
	path := "/api/v1/" + pathEscape(claim.WorkspaceKey) + "/automation/cron/claim-due"
	headers := map[string]string{"Idempotency-Key": claim.IdempotencyKey}
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &wire, headers); err != nil {
		return nil, err
	}
	if wire.Count != len(wire.Occurrences) || len(wire.Occurrences) > claim.Limit {
		return nil, fmt.Errorf("automation cron claim response count %d does not match valid limit/result %d/%d: %w",
			wire.Count, len(wire.Occurrences), claim.Limit, ErrAutomationInvalid)
	}
	seen := make(map[string]struct{}, len(wire.Occurrences))
	for index := range wire.Occurrences {
		occurrence := &wire.Occurrences[index]
		if err := validateAutomationCronOccurrence(*occurrence, claim.WorkspaceKey); err != nil {
			return nil, err
		}
		if _, duplicate := seen[occurrence.OccurrenceID]; duplicate {
			return nil, fmt.Errorf("automation cron claim response duplicates occurrence %q: %w", occurrence.OccurrenceID, ErrAutomationInvalid)
		}
		seen[occurrence.OccurrenceID] = struct{}{}
		occurrence.OccurredAt = occurrence.OccurredAt.UTC()
	}
	if wire.Occurrences == nil {
		wire.Occurrences = []AutomationCronOccurrence{}
	}
	return wire.Occurrences, nil
}

func (s *automationStore) CompleteCron(ctx context.Context, completion AutomationCronCompletion) error {
	if err := validateAutomationCronCompletion(completion); err != nil {
		return err
	}
	body := struct {
		BindingID  string                         `json:"binding_id"`
		Status     AutomationCronCompletionStatus `json:"status"`
		ErrorClass string                         `json:"error_class,omitempty"`
	}{BindingID: completion.BindingID, Status: completion.Status, ErrorClass: completion.ErrorClass}
	path := "/api/v1/" + pathEscape(completion.WorkspaceKey) + "/automation/cron/" + pathEscape(completion.OccurrenceID) + "/complete"
	return s.client.do(ctx, http.MethodPost, path, body, nil)
}

func (s *automationStore) DispatchAutomationBinding(ctx context.Context, dispatch AutomationBindingDispatch) (*AutomationBindingDispatchResult, error) {
	if err := validateAutomationBindingDispatch(dispatch); err != nil {
		return nil, err
	}
	body := automationBindingDispatchRequestWire{
		ReplayOnly: dispatch.ReplayOnly,
		SubjectRef: dispatch.SubjectRef, EpicID: dispatch.EpicID, ActorRef: dispatch.ActorRef,
		RawPayloadRef: dispatch.RawPayloadRef, PayloadBase64: append([]byte(nil), dispatch.Payload...),
		SubjectAttrs: cloneAutomationAttrs(dispatch.SubjectAttrs),
	}
	if !dispatch.ReplayOnly {
		body.EffectiveVersion = &dispatch.EffectiveVersion
	}
	var wire automationBindingDispatchResponseWire
	path := "/api/v1/" + pathEscape(dispatch.WorkspaceKey) + "/automation/bindings/" + pathEscape(dispatch.BindingID) + "/dispatch"
	headers := map[string]string{"Idempotency-Key": dispatch.IdempotencyKey}
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &wire, headers); err != nil {
		return nil, err
	}
	result := AutomationBindingDispatchResult{
		Outcome: wire.Outcome, BusyRunID: wire.BusyRunID,
		SupersededRunIDs: append([]string(nil), wire.SupersededRunIDs...),
		RunReused:        wire.RunReused, Replayed: wire.Replayed,
	}
	if len(wire.DriverRun) > 0 && string(wire.DriverRun) != "null" {
		var run domain.DriverRun
		if err := json.Unmarshal(wire.DriverRun, &run); err != nil {
			return nil, fmt.Errorf("decode automation binding DriverRun: %v: %w", err, ErrAutomationInvalid)
		}
		result.DriverRun = &run
		result.DriverRunSnapshot = append(json.RawMessage(nil), wire.DriverRun...)
	}
	if err := validateAutomationBindingDispatchResult(&result, dispatch); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *automationStore) DispatchAutomationDelivery(
	ctx context.Context,
	dispatch AutomationDeliveryDispatch,
) (*AutomationDeliveryDispatchResult, error) {
	body := struct {
		ExpectedStatus  domain.TriggerDeliveryStatus `json:"expected_status"`
		ExpectedAttempt int                          `json:"expected_attempt"`
	}{ExpectedStatus: dispatch.ExpectedStatus, ExpectedAttempt: dispatch.ExpectedAttempt}
	var wire automationDeliveryDispatchWire
	path := "/api/v1/" + pathEscape(dispatch.WorkspaceKey) + "/automation/deliveries/" + pathEscape(dispatch.DeliveryID) + "/dispatch"
	headers := map[string]string{"Idempotency-Key": dispatch.IdempotencyKey}
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &wire, headers); err != nil {
		return nil, err
	}
	return wire.result(), nil
}

func (s *automationStore) TransitionDelivery(ctx context.Context, transition AutomationDeliveryTransition) (*domain.TriggerDelivery, error) {
	body := struct {
		ExpectedStatus  domain.TriggerDeliveryStatus `json:"expected_status"`
		ExpectedAttempt int                          `json:"expected_attempt"`
		Status          domain.TriggerDeliveryStatus `json:"status"`
		DriverRunID     string                       `json:"driver_run_id,omitempty"`
		RejectionReason string                       `json:"rejection_reason,omitempty"`
		NextRetryAt     *time.Time                   `json:"next_retry_at,omitempty"`
		ErrorClass      string                       `json:"error_class,omitempty"`
	}{
		ExpectedStatus: transition.ExpectedStatus, ExpectedAttempt: transition.ExpectedAttempt,
		Status: transition.Status, DriverRunID: transition.DriverRunID,
		RejectionReason: transition.RejectionReason, NextRetryAt: transition.NextRetryAt, ErrorClass: transition.ErrorClass,
	}
	var out domain.TriggerDelivery
	path := "/api/v1/" + pathEscape(transition.WorkspaceKey) + "/automation/deliveries/" + pathEscape(transition.DeliveryID) + "/transition"
	headers := map[string]string{"Idempotency-Key": transition.IdempotencyKey}
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &out, headers); err != nil {
		return nil, err
	}
	return &out, nil
}

type automationAdmissionBody struct {
	ReplayOnly         bool                      `json:"replay_only,omitempty"`
	BindingSetRevision uint64                    `json:"binding_set_revision,omitempty"`
	MatchedBindingIDs  []string                  `json:"matched_binding_ids,omitempty"`
	EffectiveVersions  *[]AutomationCatalogGuard `json:"effective_versions,omitempty"`
	NodeID             string                    `json:"node_id,omitempty"`
	LeaseID            string                    `json:"lease_id,omitempty"`
	FencingToken       int64                     `json:"fencing_token,omitempty"`
	SourceEventID      string                    `json:"source_event_id,omitempty"`
	EventType          string                    `json:"event_type"`
	SubjectRef         string                    `json:"subject_ref,omitempty"`
	ActorRef           string                    `json:"actor_ref,omitempty"`
	OccurredAt         time.Time                 `json:"occurred_at,omitempty"`
	RawPayloadRef      string                    `json:"raw_payload_ref,omitempty"`
	RawPayloadDigest   string                    `json:"raw_payload_digest,omitempty"`
	PayloadBase64      []byte                    `json:"payload_base64,omitempty"`
	SubjectAttrs       map[string]string         `json:"subject_attrs,omitempty"`
}

type automationReservationWire struct {
	Event             *automationEventWire      `json:"event"`
	Deliveries        []*domain.TriggerDelivery `json:"deliveries"`
	EffectiveVersions []AutomationCatalogGuard  `json:"effective_versions"`
	Replayed          bool                      `json:"replayed"`
}

func (wire automationReservationWire) result() *AutomationReservationResult {
	if wire.Event == nil {
		return &AutomationReservationResult{
			Deliveries: wire.Deliveries, EffectiveVersions: wire.EffectiveVersions, Replayed: wire.Replayed,
		}
	}
	return &AutomationReservationResult{
		Event: wire.Event.event(), Deliveries: wire.Deliveries,
		EffectiveVersions: wire.EffectiveVersions, Replayed: wire.Replayed,
	}
}

type automationClaimedDeliveryWire struct {
	Event    automationEventWire     `json:"event"`
	Delivery *domain.TriggerDelivery `json:"delivery"`
}

type automationDeliveryDispatchWire struct {
	Event            *automationEventWire              `json:"event"`
	Delivery         *domain.TriggerDelivery           `json:"delivery"`
	DriverRun        *domain.DriverRun                 `json:"driver_run,omitempty"`
	Outcome          AutomationDeliveryDispatchOutcome `json:"outcome"`
	BusyRunID        string                            `json:"busy_run_id,omitempty"`
	SupersededRunIDs []string                          `json:"superseded_run_ids,omitempty"`
	RunReused        bool                              `json:"run_reused"`
	Replayed         bool                              `json:"replayed"`
}

func (wire automationDeliveryDispatchWire) result() *AutomationDeliveryDispatchResult {
	result := &AutomationDeliveryDispatchResult{
		Delivery: wire.Delivery, DriverRun: wire.DriverRun, Outcome: wire.Outcome,
		BusyRunID: wire.BusyRunID, SupersededRunIDs: append([]string(nil), wire.SupersededRunIDs...),
		RunReused: wire.RunReused, Replayed: wire.Replayed,
	}
	if wire.Event != nil {
		result.Event = wire.Event.event()
	}
	return result
}

type automationEventWire struct {
	WorkspaceKey     string                    `json:"workspace_key"`
	EventID          string                    `json:"event_id"`
	TriggerBindingID string                    `json:"trigger_binding_id,omitempty"`
	SourceKind       string                    `json:"source_kind"`
	SourceEventID    string                    `json:"source_event_id,omitempty"`
	EventType        string                    `json:"event_type"`
	RouteKey         string                    `json:"route_key,omitempty"`
	SubjectRef       string                    `json:"subject_ref,omitempty"`
	ActorRef         string                    `json:"actor_ref,omitempty"`
	EmittingRunID    string                    `json:"emitting_run_id,omitempty"`
	ParentEventID    string                    `json:"parent_event_id,omitempty"`
	EpicID           string                    `json:"epic_id,omitempty"`
	Origin           domain.TriggerEventOrigin `json:"origin,omitempty"`
	HopDepth         int                       `json:"hop_depth,omitempty"`
	OccurredAt       time.Time                 `json:"occurred_at"`
	ReceivedAt       time.Time                 `json:"received_at"`
	IdempotencyKey   string                    `json:"idempotency_key,omitempty"`
	RawPayloadRef    string                    `json:"raw_payload_ref,omitempty"`
	RawPayloadDigest string                    `json:"raw_payload_digest,omitempty"`
	SignatureStatus  string                    `json:"signature_status,omitempty"`
	ReplayOfEventID  string                    `json:"replay_of_event_id,omitempty"`
	PayloadBase64    []byte                    `json:"payload_base64,omitempty"`
	SubjectAttrs     map[string]string         `json:"subject_attrs,omitempty"`
}

func (wire automationEventWire) event() *domain.TriggerEvent {
	event := &domain.TriggerEvent{
		WorkspaceKey: wire.WorkspaceKey, EventID: wire.EventID, TriggerBindingID: wire.TriggerBindingID,
		SourceKind: wire.SourceKind, SourceEventID: wire.SourceEventID, EventType: wire.EventType,
		RouteKey: wire.RouteKey, SubjectRef: wire.SubjectRef, ActorRef: wire.ActorRef,
		EmittingRunID: wire.EmittingRunID, ParentEventID: wire.ParentEventID, EpicID: wire.EpicID,
		Origin: wire.Origin, HopDepth: wire.HopDepth, OccurredAt: wire.OccurredAt, ReceivedAt: wire.ReceivedAt,
		IdempotencyKey: wire.IdempotencyKey, RawPayloadRef: wire.RawPayloadRef,
		RawPayloadDigest: wire.RawPayloadDigest, SignatureStatus: wire.SignatureStatus,
		ReplayOfEventID: wire.ReplayOfEventID, Payload: append(json.RawMessage(nil), wire.PayloadBase64...),
		SubjectAttrs: cloneAutomationAttrs(wire.SubjectAttrs),
	}
	event.NormalizeProvenance()
	return event
}

func automationAdmissionPath(reservation AutomationEventReservation) (string, error) {
	base := "/api/v1/" + pathEscape(reservation.WorkspaceKey)
	switch reservation.Origin {
	case domain.TriggerEventOriginExternal:
		return base + "/automation/admissions/external/" + pathEscape(reservation.RouteKey), nil
	case domain.TriggerEventOriginSystem:
		return base + "/automation/admissions/system/" + pathEscape(reservation.RouteKey), nil
	case domain.TriggerEventOriginWorkflow:
		if reservation.EmittingRunID == "" || reservation.NodeID == "" || reservation.LeaseID == "" || reservation.FencingToken <= 0 {
			return "", fmt.Errorf("workflow automation reservation requires emitting run owner and fence: %w", ErrAutomationInvalid)
		}
		return base + "/driver-runs/" + pathEscape(reservation.EmittingRunID) + "/automation/admissions/" + pathEscape(reservation.RouteKey), nil
	default:
		return "", fmt.Errorf("automation reservation origin %q is invalid: %w", reservation.Origin, ErrAutomationInvalid)
	}
}

func validateAutomationCronClaim(claim AutomationCronClaim) error {
	if !automationWorkspaceKeyValid(claim.WorkspaceKey) {
		return fmt.Errorf("automation cron claim workspace is invalid: %w", ErrAutomationInvalid)
	}
	if !automationIdempotencyKeyValid(claim.IdempotencyKey) {
		return fmt.Errorf("automation cron claim idempotency key is invalid: %w", ErrAutomationInvalid)
	}
	if claim.Before.IsZero() || claim.ClaimUntil.IsZero() || !claim.ClaimUntil.After(claim.Before) ||
		claim.ClaimUntil.Sub(claim.Before) > 5*time.Minute {
		return fmt.Errorf("automation cron claim lease is invalid: %w", ErrAutomationInvalid)
	}
	if claim.Limit < 1 || claim.Limit > 1000 {
		return fmt.Errorf("automation cron claim limit is invalid: %w", ErrAutomationInvalid)
	}
	return nil
}

func validateAutomationBindingDispatch(dispatch AutomationBindingDispatch) error {
	guard := dispatch.EffectiveVersion
	if !automationWorkspaceKeyValid(dispatch.WorkspaceKey) || !automationCanonical(dispatch.BindingID) ||
		!automationIdempotencyKeyValid(dispatch.IdempotencyKey) {
		return fmt.Errorf("automation binding dispatch identity or effective version is invalid: %w", ErrAutomationInvalid)
	}
	if dispatch.ReplayOnly {
		if guard != (AutomationCatalogGuard{}) {
			return fmt.Errorf("automation binding replay-only dispatch must omit effective version: %w", ErrAutomationInvalid)
		}
	} else if guard.BindingID != dispatch.BindingID || !automationCanonical(guard.DriverID) ||
		!automationCanonical(guard.VersionID) || guard.DriverRevision == 0 ||
		!automationCanonical(guard.SourceDigest) || !automationCanonical(guard.BundleDigest) {
		return fmt.Errorf("automation binding dispatch identity or effective version is invalid: %w", ErrAutomationInvalid)
	}
	for _, value := range []string{dispatch.SubjectRef, dispatch.EpicID, dispatch.ActorRef, dispatch.RawPayloadRef} {
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("automation binding dispatch contains a non-canonical reference: %w", ErrAutomationInvalid)
		}
	}
	if len(dispatch.Payload) > 0 && !json.Valid(dispatch.Payload) {
		return fmt.Errorf("automation binding dispatch payload is not valid JSON: %w", ErrAutomationInvalid)
	}
	if len(dispatch.SubjectAttrs) > 64 {
		return fmt.Errorf("automation binding dispatch subject attributes exceed 64 entries: %w", ErrAutomationInvalid)
	}
	return nil
}

func validateAutomationBindingDispatchResult(result *AutomationBindingDispatchResult, dispatch AutomationBindingDispatch) error {
	if result == nil {
		return fmt.Errorf("automation binding dispatch returned an empty result: %w", ErrAutomationInvalid)
	}
	switch result.Outcome {
	case AutomationDeliveryDispatchBusy:
		if result.DriverRun != nil || len(result.DriverRunSnapshot) != 0 || !automationCanonical(result.BusyRunID) || result.RunReused || len(result.SupersededRunIDs) != 0 {
			return fmt.Errorf("automation binding dispatch returned a malformed busy result: %w", ErrAutomationInvalid)
		}
		return nil
	case AutomationDeliveryDispatchRun, AutomationDeliveryDispatchReused, AutomationDeliveryDispatchSuperseded:
	default:
		return fmt.Errorf("automation binding dispatch returned an invalid outcome %q: %w", result.Outcome, ErrAutomationInvalid)
	}
	if err := validateAutomationBindingDispatchRun(result, dispatch); err != nil {
		return err
	}
	return validateAutomationSupersededRunIDs(result.SupersededRunIDs)
}

func validateAutomationBindingDispatchRun(result *AutomationBindingDispatchResult, dispatch AutomationBindingDispatch) error {
	run := result.DriverRun
	if run == nil || run.WorkspaceKey != dispatch.WorkspaceKey || !automationCanonical(run.RunID) ||
		!automationCanonical(run.DriverID) || !automationCanonical(run.DriverVersionID) || result.BusyRunID != "" ||
		len(result.DriverRunSnapshot) == 0 || !json.Valid(result.DriverRunSnapshot) {
		return fmt.Errorf("automation binding dispatch returned a malformed or wrong-workspace run: %w", ErrAutomationInvalid)
	}
	if result.Outcome != AutomationDeliveryDispatchReused && run.TriggerBindingID != dispatch.BindingID {
		return fmt.Errorf("automation binding dispatch returned a wrong-binding run: %w", ErrAutomationInvalid)
	}
	if result.Outcome == AutomationDeliveryDispatchReused && !result.RunReused {
		return fmt.Errorf("automation binding dispatch returned an inconsistent reused outcome: %w", ErrAutomationInvalid)
	}
	return nil
}

func validateAutomationSupersededRunIDs(runIDs []string) error {
	seen := make(map[string]struct{}, len(runIDs))
	for _, runID := range runIDs {
		if !automationCanonical(runID) {
			return fmt.Errorf("automation binding dispatch returned a malformed superseded run id: %w", ErrAutomationInvalid)
		}
		if _, duplicate := seen[runID]; duplicate {
			return fmt.Errorf("automation binding dispatch returned duplicate superseded run ids: %w", ErrAutomationInvalid)
		}
		seen[runID] = struct{}{}
	}
	return nil
}

func validateAutomationCronOccurrence(occurrence AutomationCronOccurrence, workspace string) error {
	if occurrence.WorkspaceKey != workspace || !automationCanonical(occurrence.BindingID) ||
		!automationCanonical(occurrence.OccurrenceID) || !strings.HasPrefix(occurrence.OccurrenceID, "cron:") ||
		len(occurrence.OccurrenceID) > 512 || occurrence.OccurredAt.IsZero() {
		return fmt.Errorf("automation cron claim returned an invalid or wrong-workspace occurrence: %w", ErrAutomationInvalid)
	}
	if occurrence.RouteKey != "" && !automationCanonical(occurrence.RouteKey) {
		return fmt.Errorf("automation cron claim returned a non-canonical route: %w", ErrAutomationInvalid)
	}
	return nil
}

func validateAutomationCronCompletion(completion AutomationCronCompletion) error {
	if !automationWorkspaceKeyValid(completion.WorkspaceKey) || !automationCanonical(completion.BindingID) ||
		!automationCanonical(completion.OccurrenceID) || !strings.HasPrefix(completion.OccurrenceID, "cron:") ||
		len(completion.OccurrenceID) > 512 {
		return fmt.Errorf("automation cron completion identity is invalid: %w", ErrAutomationInvalid)
	}
	switch completion.Status {
	case AutomationCronCompletionAdmitted, AutomationCronCompletionDropped, AutomationCronCompletionFailed:
	default:
		return fmt.Errorf("automation cron completion status %q is invalid: %w", completion.Status, ErrAutomationInvalid)
	}
	if completion.ErrorClass != strings.TrimSpace(completion.ErrorClass) || len(completion.ErrorClass) > 256 ||
		(completion.Status == AutomationCronCompletionAdmitted && completion.ErrorClass != "") {
		return fmt.Errorf("automation cron completion error class is invalid: %w", ErrAutomationInvalid)
	}
	return nil
}

func automationCanonical(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

func automationIdempotencyKeyValid(value string) bool {
	if !automationCanonical(value) || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func automationWorkspaceKeyValid(value string) bool {
	if len(value) < 1 || len(value) > 32 || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	last := value[len(value)-1]
	return len(value) == 1 || last != '-'
}

func automationBindingBody(binding *domain.TriggerBinding, create bool) map[string]any {
	body := map[string]any{
		"name": binding.Name, "source_kind": binding.SourceKind, "source_ref": binding.SourceRef,
		"source_config_ref": binding.SourceConfigRef, "route_key": binding.RouteKey, "method": binding.Method,
		"path_template": binding.PathTemplate, "topic": binding.Topic,
		"event_type_patterns": append([]string(nil), binding.EventTypePatterns...), "filter_ref": binding.FilterRef,
		"driver_id": binding.DriverID, "driver_version_id": binding.DriverVersionID,
		"target_entrypoint": binding.TargetEntrypoint, "target_agent_service_id": binding.TargetAgentServiceID,
		"concurrency_policy": binding.ConcurrencyPolicy, "idempotency_policy": binding.IdempotencyPolicy,
		"auth_policy": binding.AuthPolicy, "subject_key_template": binding.SubjectKeyTemplate,
		"retry_max_attempts": binding.RetryMaxAttempts, "retry_backoff_seconds": binding.RetryBackoffSeconds,
		"schedule": binding.Schedule, "schedule_timezone": binding.ScheduleTimezone,
		"permissions": append([]string(nil), binding.Permissions...), "enabled": binding.Enabled,
	}
	if create {
		body["binding_id"] = binding.BindingID
	}
	if binding.ActorFilter != nil {
		body["actor_filter"] = binding.ActorFilter
	} else if !create {
		body["actor_filter"] = map[string][]string{}
	}
	return body
}

func queryValue(query url.Values, key, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

func queryLimit(query url.Values, limit int) {
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
}

func cloneAutomationAttrs(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
