package automation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ApprovalEventType       = "approval"
	ApprovalSourceKind      = "approval"
	MaxApprovalPayloadBytes = 64 << 10
)

// JournalApproval persists one session-attested approval before the caller
// dispatches it to Execution's await matcher. It intentionally does not match
// Automation bindings: an approval must be durable even when no binding is
// configured, and the approval route owns its existing await-only fanout.
func (s *Service) JournalApproval(
	ctx context.Context,
	auth authority.OperatorAuthority,
	command JournalApprovalCommand,
) (*Event, error) {
	if ctx == nil {
		return nil, fmt.Errorf("approval context is required: %w", ErrInvalid)
	}
	if s == nil || s.authority == nil {
		return nil, authority.ErrAdmissionDenied
	}
	workspace, err := normalizeRequired("workspace", command.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	if err := s.authority.RequireOperator(ActionJournalApproval, workspace, auth); err != nil {
		return nil, err
	}
	if s.approvalEvents == nil {
		return nil, ErrUnavailable
	}

	eventID, err := requireCanonical("approval event id", command.EventID)
	if err != nil {
		return nil, err
	}
	if command.EventType != ApprovalEventType {
		return nil, fmt.Errorf("approval event type must be %q: %w", ApprovalEventType, ErrInvalid)
	}
	subjectRef, err := requireCanonical("approval subject", command.SubjectRef)
	if err != nil {
		return nil, err
	}
	actorRef, err := requireCanonical("approval actor ref", command.ActorRef)
	if err != nil {
		return nil, err
	}
	if actorRef != auth.Subject() {
		return nil, authority.ErrAdmissionDenied
	}
	if len(command.Payload) > MaxApprovalPayloadBytes {
		return nil, fmt.Errorf("approval payload exceeds %d bytes: %w", MaxApprovalPayloadBytes, ErrInvalid)
	}
	occurredAt := command.OccurredAt.UTC()
	if occurredAt.IsZero() {
		now := time.Now
		if s.now != nil {
			now = s.now
		}
		occurredAt = now().UTC()
	}
	payload := cloneRawMessage(command.Payload)
	payloadDigest := ""
	if len(payload) > 0 {
		sum := sha256.Sum256(payload)
		payloadDigest = "sha256:" + hex.EncodeToString(sum[:])
	}
	event := &Event{
		WorkspaceKey: workspace, EventID: eventID,
		SourceKind: ApprovalSourceKind, SourceEventID: eventID,
		EventType: ApprovalEventType, SubjectRef: subjectRef, ActorRef: actorRef,
		Origin: EventOriginExternal, OccurredAt: occurredAt, ReceivedAt: occurredAt,
		IdempotencyKey:   ApprovalSourceKind + ":" + workspace + ":" + eventID,
		RawPayloadDigest: payloadDigest, SignatureStatus: SignatureStatusSession,
		Payload: payload,
	}
	committed, err := s.approvalEvents.AppendTriggerEvent(ctx, event)
	if err != nil {
		return nil, fmt.Errorf("journal approval event: %w", err)
	}
	if err := validateApprovalEvent(committed, event); err != nil {
		return nil, err
	}
	return cloneEvent(committed), nil
}

func validateApprovalEvent(committed, expected *Event) error {
	if committed == nil || expected == nil || committed.WorkspaceKey != expected.WorkspaceKey ||
		committed.EventID != expected.EventID || committed.SourceEventID != expected.SourceEventID ||
		committed.SourceKind != ApprovalSourceKind || committed.EventType != ApprovalEventType ||
		committed.SubjectRef != expected.SubjectRef || committed.ActorRef != expected.ActorRef ||
		committed.Origin != EventOriginExternal || committed.SignatureStatus != SignatureStatusSession ||
		committed.IdempotencyKey != expected.IdempotencyKey ||
		committed.RawPayloadDigest != expected.RawPayloadDigest ||
		!committed.OccurredAt.Equal(expected.OccurredAt) || !committed.ReceivedAt.Equal(expected.ReceivedAt) ||
		!bytes.Equal(committed.Payload, expected.Payload) || strings.TrimSpace(committed.IdempotencyKey) == "" {
		return ErrInvalidPersistedState
	}
	return nil
}
