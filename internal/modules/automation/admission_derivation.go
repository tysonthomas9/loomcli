package automation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type derivedAdmission struct {
	workspace        string
	sourceKind       string
	sourceRef        string
	routeKey         string
	sourceEventID    string
	eventType        string
	subjectRef       string
	actorRef         string
	epicID           string
	emittingRunID    string
	executionNodeID  string
	executionLeaseID string
	executionFence   int64
	parentEventID    string
	origin           EventOrigin
	hopDepth         int
	idempotencyKey   string
	signatureStatus  string
	occurredAt       time.Time
	rawPayloadRef    string
	rawPayloadDigest string
	payload          json.RawMessage
	subjectAttrs     map[string]string
}

const MaxEventPayloadBytes = 8 << 20

// admissionContent is Automation's private canonical content envelope. The
// public origin-specific ports map into it only after their distinct typed
// authority has been admitted.
type admissionContent struct {
	workspaceKey     string
	sourceEventID    string
	eventType        string
	subjectRef       string
	occurredAt       time.Time
	rawPayloadRef    string
	rawPayloadDigest string
	payload          json.RawMessage
	subjectAttrs     map[string]string
}

func newDerivedAdmission(content admissionContent, emptyPayloadObject bool) (*derivedAdmission, error) {
	workspace, err := normalizeRequired("workspace", content.workspaceKey)
	if err != nil {
		return nil, err
	}
	sourceEventID, err := requireCanonical("source event id", content.sourceEventID)
	if err != nil {
		return nil, err
	}
	eventType, err := normalizeRequired("event type", content.eventType)
	if err != nil {
		return nil, err
	}
	payload := cloneRawMessage(content.payload)
	if len(payload) > MaxEventPayloadBytes {
		return nil, fmt.Errorf("event payload exceeds %d bytes: %w", MaxEventPayloadBytes, ErrInvalid)
	}
	if len(bytes.TrimSpace(payload)) == 0 && emptyPayloadObject {
		payload = json.RawMessage(`{}`)
	}
	if len(payload) > 0 && !json.Valid(payload) {
		if emptyPayloadObject {
			return nil, fmt.Errorf("webhook payload must be valid JSON: %w", ErrInvalid)
		}
		return nil, fmt.Errorf("event payload must be valid JSON: %w", ErrInvalid)
	}
	derived := &derivedAdmission{
		workspace:     workspace,
		sourceEventID: sourceEventID,
		eventType:     eventType,
		subjectRef:    strings.TrimSpace(content.subjectRef),
		occurredAt:    content.occurredAt.UTC(),
		rawPayloadRef: strings.TrimSpace(content.rawPayloadRef),
		payload:       payload,
		subjectAttrs:  cloneStringMap(content.subjectAttrs),
	}
	if len(derived.payload) > 0 {
		sum := sha256.Sum256(derived.payload)
		derived.rawPayloadDigest = "sha256:" + hex.EncodeToString(sum[:])
	} else {
		derived.rawPayloadDigest = strings.TrimSpace(content.rawPayloadDigest)
	}
	return derived, nil
}

func deriveWebhookAdmission(auth authority.WebhookAuthority, event WebhookEvent) (*derivedAdmission, error) {
	derived, err := newDerivedAdmission(admissionContent{
		workspaceKey: event.WorkspaceKey, sourceEventID: event.SourceEventID, eventType: event.EventType,
		subjectRef: event.SubjectRef, occurredAt: event.OccurredAt, rawPayloadRef: event.RawPayloadRef,
		rawPayloadDigest: event.RawPayloadDigest, payload: event.Payload, subjectAttrs: event.SubjectAttrs,
	}, true)
	if err != nil {
		return nil, err
	}
	sourceKind, err := normalizeRequired("source kind", event.SourceKind)
	if err != nil {
		return nil, err
	}
	if sourceKind == SourceKindInternal || sourceKind == SourceKindCron {
		return nil, fmt.Errorf("webhook authority cannot emit source kind %q: %w", sourceKind, ErrInvalid)
	}
	routeKey, err := normalizeRequired("route key", event.RouteKey)
	if err != nil {
		return nil, err
	}
	derived.sourceKind = sourceKind
	derived.routeKey = routeKey
	derived.sourceRef = strings.TrimSpace(event.SourceRef)
	derived.actorRef = firstNonEmpty(event.ActorRef, auth.Subject())
	derived.origin = EventOriginExternal
	derived.signatureStatus = SignatureStatusVerified
	derived.idempotencyKey = sourceKind + ":" + derived.sourceEventID
	return derived, nil
}

func (s *Service) deriveWorkflowAdmission(ctx context.Context, auth authority.ExecutionAuthority, event WorkflowEvent) (*derivedAdmission, error) {
	if s.execution == nil {
		return nil, authority.ErrAdmissionDenied
	}
	derived, err := newDerivedAdmission(admissionContent{
		workspaceKey: event.WorkspaceKey, sourceEventID: event.SourceEventID, eventType: event.EventType,
		subjectRef: event.SubjectRef, payload: event.Payload, subjectAttrs: event.SubjectAttrs,
	}, false)
	if err != nil {
		return nil, err
	}
	emission, err := s.loadExecutionEmissionContext(ctx, auth)
	if err != nil {
		return nil, err
	}
	commandNodeID, commandLeaseID, err := validateExecutionOwner(event, emission)
	if err != nil {
		return nil, err
	}
	if err := validateWorkspace(emission.WorkspaceKey, derived.workspace); err != nil {
		return nil, err
	}
	eventType, err := normalizeInternalEventType(derived.eventType)
	if err != nil {
		return nil, err
	}
	runID := strings.TrimSpace(emission.RunID)
	derived.eventType = eventType
	derived.sourceKind = SourceKindInternal
	derived.sourceRef = runID
	derived.emittingRunID = runID
	derived.executionNodeID = commandNodeID
	derived.executionLeaseID = commandLeaseID
	derived.executionFence = event.ExecutionFencingToken
	derived.routeKey = "internal." + eventType
	derived.actorRef = firstNonEmpty(emission.ActorRef, auth.Subject())
	derived.epicID = strings.TrimSpace(emission.EpicID)
	derived.origin = EventOriginWorkflow
	derived.signatureStatus = SignatureStatusInternal
	derived.idempotencyKey = InternalEventIdempotencyKey(derived.workspace, derived.sourceEventID)
	derived.parentEventID = strings.TrimSpace(emission.ParentEventID)
	if derived.parentEventID == "" {
		derived.hopDepth = 1
		return derived, nil
	}
	hopDepth, err := s.parentHopDepth(ctx, derived.workspace, derived.parentEventID)
	if err != nil {
		return nil, err
	}
	derived.hopDepth = hopDepth
	return derived, nil
}

func (s *Service) loadExecutionEmissionContext(ctx context.Context, auth authority.ExecutionAuthority) (*ExecutionEmissionContext, error) {
	emission, err := s.execution.EmissionContext(ctx, auth)
	if err != nil {
		return nil, fmt.Errorf("derive execution emission context: %w", err)
	}
	if emission == nil || strings.TrimSpace(emission.RunID) == "" || strings.TrimSpace(emission.NodeID) == "" ||
		strings.TrimSpace(emission.LeaseID) == "" || emission.FencingToken <= 0 {
		return nil, ErrInvalidPersistedState
	}
	return emission, nil
}

func validateExecutionOwner(event WorkflowEvent, emission *ExecutionEmissionContext) (string, string, error) {
	commandNodeID := strings.TrimSpace(event.ExecutionNodeID)
	commandLeaseID := strings.TrimSpace(event.ExecutionLeaseID)
	if commandNodeID == "" || commandNodeID != event.ExecutionNodeID || commandLeaseID == "" || commandLeaseID != event.ExecutionLeaseID ||
		event.ExecutionFencingToken <= 0 || commandNodeID != emission.NodeID || commandLeaseID != emission.LeaseID ||
		event.ExecutionFencingToken != emission.FencingToken {
		return "", "", fmt.Errorf("verified execution owner changed before admission: %w", ErrConflict)
	}
	return commandNodeID, commandLeaseID, nil
}

func (s *Service) deriveSystemAdmission(ctx context.Context, auth authority.SystemAuthority, event SystemEvent) (*derivedAdmission, error) {
	derived, err := newDerivedAdmission(admissionContent{
		workspaceKey: event.WorkspaceKey, sourceEventID: event.SourceEventID, eventType: event.EventType,
		subjectRef: event.SubjectRef, occurredAt: event.OccurredAt, payload: event.Payload, subjectAttrs: event.SubjectAttrs,
	}, false)
	if err != nil {
		return nil, err
	}
	derived.origin = EventOriginSystem
	derived.actorRef = auth.Subject()
	derived.epicID = strings.TrimSpace(event.EpicID)
	derived.signatureStatus = SignatureStatusInternal
	derived.sourceKind = SourceKindInternal
	if err := deriveSystemSource(event.SourceRef, "", derived); err != nil {
		return nil, err
	}
	derived.parentEventID = strings.TrimSpace(event.ParentEventID)
	if derived.parentEventID == "" {
		return derived, nil
	}
	hopDepth, err := s.parentHopDepth(ctx, derived.workspace, derived.parentEventID)
	if err != nil {
		return nil, err
	}
	derived.hopDepth = hopDepth
	return derived, nil
}

func deriveCronAdmission(auth authority.SystemAuthority, occurrence CronOccurrence, payload json.RawMessage) (*derivedAdmission, error) {
	derived, err := newDerivedAdmission(admissionContent{
		workspaceKey: occurrence.WorkspaceKey, sourceEventID: occurrence.OccurrenceID,
		eventType: CronEventType, subjectRef: occurrence.BindingID,
		occurredAt: occurrence.OccurredAt, payload: payload,
	}, false)
	if err != nil {
		return nil, err
	}
	derived.origin = EventOriginSystem
	derived.actorRef = auth.Subject()
	derived.signatureStatus = SignatureStatusInternal
	derived.sourceKind = SourceKindCron
	if err := deriveSystemSource(occurrence.BindingID, occurrence.RouteKey, derived); err != nil {
		return nil, err
	}
	return derived, nil
}

func deriveSystemSource(sourceRef, routeKey string, derived *derivedAdmission) error {
	switch derived.sourceKind {
	case SourceKindCron:
		routeKey, err := normalizeRequired("route key", routeKey)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(derived.sourceEventID, "cron:") {
			return fmt.Errorf("cron source event id must start with cron:: %w", ErrInvalid)
		}
		derived.routeKey = routeKey
		derived.sourceRef = strings.TrimSpace(sourceRef)
		derived.idempotencyKey = derived.sourceEventID
		return nil
	case SourceKindInternal:
		eventType, err := normalizeInternalEventType(derived.eventType)
		if err != nil {
			return err
		}
		derived.eventType = eventType
		derived.routeKey = "internal." + eventType
		derived.sourceRef = strings.TrimSpace(sourceRef)
		derived.idempotencyKey = InternalEventIdempotencyKey(derived.workspace, derived.sourceEventID)
		return nil
	default:
		return fmt.Errorf("system authority cannot emit source kind %q: %w", derived.sourceKind, ErrInvalid)
	}
}

func (s *Service) parentHopDepth(ctx context.Context, workspace, parentID string) (int, error) {
	parent, err := s.loadEvent(ctx, workspace, parentID)
	if err == nil {
		return parent.HopDepth + 1, nil
	}
	if errors.Is(err, ErrNotFound) {
		return 0, errors.Join(ErrParentEventNotFound, err)
	}
	return 0, err
}
