package automation

import (
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

func (s *Service) deriveAdmission(ctx context.Context, eventAuth EventAuthority, command AdmitEventCommand) (*derivedAdmission, error) {
	derived, err := newDerivedAdmission(command)
	if err != nil {
		return nil, err
	}
	switch auth := eventAuth.value.(type) {
	case authority.WebhookAuthority:
		err = deriveWebhookAdmission(eventAuth, auth, command, derived)
	case authority.ExecutionAuthority:
		err = s.deriveExecutionAdmission(ctx, eventAuth, auth, command, derived)
	case authority.SystemAuthority:
		err = s.deriveSystemAdmission(ctx, eventAuth, auth, command, derived)
	default:
		err = authority.ErrAdmissionDenied
	}
	if err != nil {
		return nil, err
	}
	return derived, nil
}

func newDerivedAdmission(command AdmitEventCommand) (*derivedAdmission, error) {
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	sourceEventID, err := requireCanonical("source event id", command.SourceEventID)
	if err != nil {
		return nil, err
	}
	eventType, err := normalizeRequired("event type", command.EventType)
	if err != nil {
		return nil, err
	}
	derived := &derivedAdmission{
		workspace:     workspace,
		sourceEventID: sourceEventID,
		eventType:     eventType,
		subjectRef:    strings.TrimSpace(command.SubjectRef),
		occurredAt:    command.OccurredAt.UTC(),
		rawPayloadRef: strings.TrimSpace(command.RawPayloadRef),
		payload:       cloneRawMessage(command.Payload),
		subjectAttrs:  cloneStringMap(command.SubjectAttrs),
	}
	if len(derived.payload) > 0 {
		sum := sha256.Sum256(derived.payload)
		derived.rawPayloadDigest = "sha256:" + hex.EncodeToString(sum[:])
	} else {
		derived.rawPayloadDigest = strings.TrimSpace(command.RawPayloadDigest)
	}
	return derived, nil
}

func deriveWebhookAdmission(eventAuth EventAuthority, auth authority.WebhookAuthority, command AdmitEventCommand, derived *derivedAdmission) error {
	if eventAuth.origin != EventOriginExternal {
		return authority.ErrAdmissionDenied
	}
	sourceKind, err := normalizeRequired("source kind", command.SourceKind)
	if err != nil {
		return err
	}
	if sourceKind == SourceKindInternal || sourceKind == SourceKindCron {
		return fmt.Errorf("webhook authority cannot emit source kind %q: %w", sourceKind, ErrInvalid)
	}
	routeKey, err := normalizeRequired("route key", command.RouteKey)
	if err != nil {
		return err
	}
	derived.sourceKind = sourceKind
	derived.routeKey = routeKey
	derived.sourceRef = strings.TrimSpace(command.SourceRef)
	derived.actorRef = firstNonEmpty(command.ActorRef, auth.Subject())
	derived.origin = EventOriginExternal
	derived.signatureStatus = SignatureStatusVerified
	derived.idempotencyKey = sourceKind + ":" + derived.sourceEventID
	return nil
}

func (s *Service) deriveExecutionAdmission(ctx context.Context, eventAuth EventAuthority, auth authority.ExecutionAuthority, command AdmitEventCommand, derived *derivedAdmission) error {
	if eventAuth.origin != EventOriginWorkflow || s.execution == nil {
		return authority.ErrAdmissionDenied
	}
	emission, err := s.loadExecutionEmissionContext(ctx, auth)
	if err != nil {
		return err
	}
	commandNodeID, commandLeaseID, err := validateExecutionOwner(command, emission)
	if err != nil {
		return err
	}
	if err := validateWorkspace(emission.WorkspaceKey, derived.workspace); err != nil {
		return err
	}
	eventType, err := normalizeInternalEventType(derived.eventType)
	if err != nil {
		return err
	}
	runID := strings.TrimSpace(emission.RunID)
	derived.eventType = eventType
	derived.sourceKind = SourceKindInternal
	derived.sourceRef = runID
	derived.emittingRunID = runID
	derived.executionNodeID = commandNodeID
	derived.executionLeaseID = commandLeaseID
	derived.executionFence = command.ExecutionFencingToken
	derived.routeKey = "internal." + eventType
	derived.actorRef = firstNonEmpty(emission.ActorRef, auth.Subject())
	derived.epicID = strings.TrimSpace(emission.EpicID)
	derived.origin = EventOriginWorkflow
	derived.signatureStatus = SignatureStatusInternal
	derived.idempotencyKey = internalEventIdempotencyKey(derived.workspace, derived.sourceEventID)
	derived.parentEventID = strings.TrimSpace(emission.ParentEventID)
	if derived.parentEventID == "" {
		derived.hopDepth = 1
		return nil
	}
	hopDepth, err := s.parentHopDepth(ctx, derived.workspace, derived.parentEventID)
	if err != nil {
		return err
	}
	derived.hopDepth = hopDepth
	return nil
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

func validateExecutionOwner(command AdmitEventCommand, emission *ExecutionEmissionContext) (string, string, error) {
	commandNodeID := strings.TrimSpace(command.ExecutionNodeID)
	commandLeaseID := strings.TrimSpace(command.ExecutionLeaseID)
	if commandNodeID == "" || commandNodeID != command.ExecutionNodeID || commandLeaseID == "" || commandLeaseID != command.ExecutionLeaseID ||
		command.ExecutionFencingToken <= 0 || commandNodeID != emission.NodeID || commandLeaseID != emission.LeaseID ||
		command.ExecutionFencingToken != emission.FencingToken {
		return "", "", fmt.Errorf("verified execution owner changed before admission: %w", ErrConflict)
	}
	return commandNodeID, commandLeaseID, nil
}

func (s *Service) deriveSystemAdmission(ctx context.Context, eventAuth EventAuthority, auth authority.SystemAuthority, command AdmitEventCommand, derived *derivedAdmission) error {
	if eventAuth.origin != EventOriginSystem {
		return authority.ErrAdmissionDenied
	}
	derived.origin = EventOriginSystem
	derived.actorRef = auth.Subject()
	derived.epicID = strings.TrimSpace(command.EpicID)
	derived.signatureStatus = SignatureStatusInternal
	derived.sourceKind = strings.ToLower(strings.TrimSpace(command.SourceKind))
	if derived.sourceKind == "" {
		derived.sourceKind = SourceKindInternal
	}
	if err := deriveSystemSource(command, derived); err != nil {
		return err
	}
	derived.parentEventID = strings.TrimSpace(command.ParentEventID)
	if derived.parentEventID == "" {
		return nil
	}
	hopDepth, err := s.parentHopDepth(ctx, derived.workspace, derived.parentEventID)
	if err != nil {
		return err
	}
	derived.hopDepth = hopDepth
	return nil
}

func deriveSystemSource(command AdmitEventCommand, derived *derivedAdmission) error {
	switch derived.sourceKind {
	case SourceKindCron:
		routeKey, err := normalizeRequired("route key", command.RouteKey)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(derived.sourceEventID, "cron:") {
			return fmt.Errorf("cron source event id must start with cron:: %w", ErrInvalid)
		}
		derived.routeKey = routeKey
		derived.sourceRef = strings.TrimSpace(command.SourceRef)
		derived.idempotencyKey = derived.sourceEventID
		return nil
	case SourceKindInternal:
		eventType, err := normalizeInternalEventType(derived.eventType)
		if err != nil {
			return err
		}
		derived.eventType = eventType
		derived.routeKey = "internal." + eventType
		derived.sourceRef = strings.TrimSpace(command.SourceRef)
		derived.idempotencyKey = internalEventIdempotencyKey(derived.workspace, derived.sourceEventID)
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
