package webhookingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

const (
	// MaxPayloadBytes retains the established inbound webhook limit while
	// protecting non-HTTP callers of the application workflow as well.
	MaxPayloadBytes = 8 << 20
	// MaxPresentedSignatureBytes bounds untrusted proof material passed to a
	// connector verifier. Real webhook signatures are far smaller than this.
	MaxPresentedSignatureBytes = 8 << 10
)

var (
	ErrInvalidRequest = errors.New("webhook ingestion: invalid request")
	ErrUnavailable    = errors.New("webhook ingestion: unavailable")
)

// IngestRequest is normalized webhook data supplied by a transport adapter.
// It intentionally has no origin, hop depth, signature status, idempotency,
// parent-event, epic, or driver-version fields. Automation derives all of
// those security- and execution-sensitive values from typed authority and its
// own durable state.
type IngestRequest struct {
	WorkspaceKey       string
	SourceKind         string
	SourceRef          string
	RouteKey           string
	SourceEventID      string
	EventType          string
	SubjectRef         string
	ActorRef           string
	OccurredAt         time.Time
	RawPayloadRef      string
	RawPayloadDigest   string
	Payload            json.RawMessage
	SubjectAttrs       map[string]string
	PresentedSignature string
}

// Workflow is the named application workflow for external webhook ingestion.
type Workflow struct {
	verifier  Verifier
	authority AuthorityProvider
	admission automation.EventAdmission
}

// New constructs a webhook ingestion workflow. Every dependency is required;
// an incomplete composition fails closed before it can receive traffic.
func New(verifier Verifier, authorityProvider AuthorityProvider, admission automation.EventAdmission) (*Workflow, error) {
	switch {
	case verifier == nil:
		return nil, fmt.Errorf("%w: verifier is required", ErrUnavailable)
	case authorityProvider == nil:
		return nil, fmt.Errorf("%w: authority provider is required", ErrUnavailable)
	case admission == nil:
		return nil, fmt.Errorf("%w: automation admission is required", ErrUnavailable)
	default:
		return &Workflow{verifier: verifier, authority: authorityProvider, admission: admission}, nil
	}
}

// Ingest verifies the webhook, derives typed webhook authority, and only then
// enters Automation's single event-admission use case.
func (w *Workflow) Ingest(ctx context.Context, request IngestRequest) (*automation.AdmissionResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if w == nil || w.verifier == nil || w.authority == nil || w.admission == nil {
		return nil, ErrUnavailable
	}

	request, err := validateRequest(request)
	if err != nil {
		return nil, err
	}

	verification := VerificationRequest{
		WorkspaceKey:       request.WorkspaceKey,
		SourceKind:         request.SourceKind,
		SourceRef:          request.SourceRef,
		RouteKey:           request.RouteKey,
		PresentedSignature: request.PresentedSignature,
		Payload:            cloneRawMessage(request.Payload),
	}
	if err := w.verifier.Verify(ctx, verification); err != nil {
		return nil, fmt.Errorf("verify webhook: %w", err)
	}

	// Preserve the established transport contract: verification covers the
	// exact bytes received, then an empty body is admitted as an empty JSON
	// object and malformed JSON is rejected before authority derivation or
	// durable admission. In particular, an invalid unsigned request remains a
	// verification denial rather than becoming a payload-validation oracle.
	payload := strings.TrimSpace(string(request.Payload))
	if payload == "" {
		request.Payload = json.RawMessage(`{}`)
	} else if !json.Valid(request.Payload) {
		return nil, fmt.Errorf("%w: webhook payload must be valid JSON", ErrInvalidRequest)
	}

	webhookAuthority, err := w.authority.AuthorityForVerifiedWebhook(ctx, AuthorityRequest{
		WorkspaceKey: request.WorkspaceKey,
		SourceKind:   request.SourceKind,
		SourceRef:    request.SourceRef,
		RouteKey:     request.RouteKey,
	})
	if err != nil {
		return nil, fmt.Errorf("derive webhook authority: %w", err)
	}

	result, err := w.admission.AdmitEvent(ctx, automation.NewWebhookEventAuthority(webhookAuthority), admissionCommand(request))
	if err != nil {
		return result, fmt.Errorf("admit webhook event: %w", err)
	}
	return result, nil
}

func admissionCommand(request IngestRequest) automation.AdmitEventCommand {
	return automation.AdmitEventCommand{
		WorkspaceKey:     request.WorkspaceKey,
		SourceKind:       request.SourceKind,
		SourceRef:        request.SourceRef,
		RouteKey:         request.RouteKey,
		SourceEventID:    request.SourceEventID,
		EventType:        request.EventType,
		SubjectRef:       request.SubjectRef,
		ActorRef:         request.ActorRef,
		OccurredAt:       request.OccurredAt,
		RawPayloadRef:    request.RawPayloadRef,
		RawPayloadDigest: request.RawPayloadDigest,
		Payload:          cloneRawMessage(request.Payload),
		SubjectAttrs:     cloneStringMap(request.SubjectAttrs),
	}
}

func validateRequest(request IngestRequest) (IngestRequest, error) {
	var err error
	request.WorkspaceKey, err = required("workspace", request.WorkspaceKey)
	if err != nil {
		return IngestRequest{}, err
	}
	request.SourceKind, err = required("source kind", request.SourceKind)
	if err != nil {
		return IngestRequest{}, err
	}
	request.SourceKind = strings.ToLower(request.SourceKind)
	if request.SourceKind == automation.SourceKindInternal || request.SourceKind == automation.SourceKindCron {
		return IngestRequest{}, fmt.Errorf("%w: webhook source kind %q is reserved", ErrInvalidRequest, request.SourceKind)
	}
	request.RouteKey, err = required("route key", request.RouteKey)
	if err != nil {
		return IngestRequest{}, err
	}
	request.SourceEventID, err = required("source event id", request.SourceEventID)
	if err != nil {
		return IngestRequest{}, err
	}
	request.EventType, err = required("event type", request.EventType)
	if err != nil {
		return IngestRequest{}, err
	}

	request.SourceRef = strings.TrimSpace(request.SourceRef)
	request.SubjectRef = strings.TrimSpace(request.SubjectRef)
	request.ActorRef = strings.TrimSpace(request.ActorRef)
	request.RawPayloadRef = strings.TrimSpace(request.RawPayloadRef)
	request.RawPayloadDigest = strings.TrimSpace(request.RawPayloadDigest)
	if len(request.Payload) > MaxPayloadBytes {
		return IngestRequest{}, fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidRequest, MaxPayloadBytes)
	}
	if len(request.PresentedSignature) > MaxPresentedSignatureBytes {
		return IngestRequest{}, fmt.Errorf("%w: presented signature exceeds %d bytes", ErrInvalidRequest, MaxPresentedSignatureBytes)
	}
	request.Payload = cloneRawMessage(request.Payload)
	request.SubjectAttrs = cloneStringMap(request.SubjectAttrs)
	return request, nil
}

func required(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidRequest, field)
	}
	return value, nil
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}
