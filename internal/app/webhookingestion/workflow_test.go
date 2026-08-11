package webhookingestion

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type verifierFunc func(context.Context, VerificationRequest) error

func (function verifierFunc) Verify(ctx context.Context, request VerificationRequest) error {
	return function(ctx, request)
}

type authorityProviderFunc func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error)

func (function authorityProviderFunc) AuthorityForVerifiedWebhook(ctx context.Context, request AuthorityRequest) (authority.WebhookAuthority, error) {
	return function(ctx, request)
}

type admissionFunc func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error)

func (function admissionFunc) AdmitEvent(ctx context.Context, eventAuthority automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	return function(ctx, eventAuthority, command)
}

func validIngestRequest() IngestRequest {
	return IngestRequest{
		WorkspaceKey:       "WS",
		SourceKind:         "github",
		SourceRef:          "connector/github-main",
		RouteKey:           "github.pull_request.opened",
		SourceEventID:      "delivery-42",
		EventType:          "pull_request",
		SubjectRef:         "acme/widgets#42",
		ActorRef:           "octocat",
		OccurredAt:         time.Date(2026, 7, 16, 12, 30, 0, 0, time.UTC),
		RawPayloadRef:      "artifact://webhooks/delivery-42",
		RawPayloadDigest:   "sha256:payload",
		Payload:            json.RawMessage(`{"action":"opened","number":42}`),
		SubjectAttrs:       map[string]string{"repo": "acme/widgets", "pr_number": "42"},
		PresentedSignature: "sha256=presented-proof",
	}
}

func TestIngestOrdersVerificationAuthorityAndAdmission(t *testing.T) {
	order := make([]string, 0, 3)
	workflow, err := New(
		verifierFunc(func(context.Context, VerificationRequest) error {
			order = append(order, "verify")
			return nil
		}),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			order = append(order, "authority")
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			order = append(order, "admit")
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := workflow.Ingest(t.Context(), validIngestRequest()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if want := []string{"verify", "authority", "admit"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("call order = %v, want %v", order, want)
	}
}

func TestIngestVerificationDenialStopsBeforeAuthority(t *testing.T) {
	denied := errors.New("signature mismatch")
	order := make([]string, 0, 2)
	workflow, err := New(
		verifierFunc(func(context.Context, VerificationRequest) error {
			order = append(order, "verify")
			return denied
		}),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			order = append(order, "authority")
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			order = append(order, "admit")
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Ingest(t.Context(), validIngestRequest())
	if result != nil || !errors.Is(err, denied) {
		t.Fatalf("Ingest = (%+v, %v), want nil result wrapping verification denial", result, err)
	}
	if want := []string{"verify"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("call order = %v, want %v", order, want)
	}
}

func TestIngestAuthorityDenialStopsBeforeAdmission(t *testing.T) {
	denied := authority.ErrAdmissionDenied
	order := make([]string, 0, 3)
	workflow, err := New(
		verifierFunc(func(context.Context, VerificationRequest) error {
			order = append(order, "verify")
			return nil
		}),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			order = append(order, "authority")
			return authority.WebhookAuthority{}, denied
		}),
		admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			order = append(order, "admit")
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Ingest(t.Context(), validIngestRequest())
	if result != nil || !errors.Is(err, denied) {
		t.Fatalf("Ingest = (%+v, %v), want nil result wrapping authority denial", result, err)
	}
	if want := []string{"verify", "authority"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("call order = %v, want %v", order, want)
	}
}

func TestIngestValidatesWorkspaceAndSourceBeforeVerification(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*IngestRequest)
	}{
		{name: "workspace", mutate: func(request *IngestRequest) { request.WorkspaceKey = "  " }},
		{name: "source kind", mutate: func(request *IngestRequest) { request.SourceKind = "  " }},
		{name: "internal source", mutate: func(request *IngestRequest) { request.SourceKind = "INTERNAL" }},
		{name: "cron source", mutate: func(request *IngestRequest) { request.SourceKind = automation.SourceKindCron }},
		{name: "route", mutate: func(request *IngestRequest) { request.RouteKey = "" }},
		{name: "source event id", mutate: func(request *IngestRequest) { request.SourceEventID = "" }},
		{name: "event type", mutate: func(request *IngestRequest) { request.EventType = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			workflow, err := New(
				verifierFunc(func(context.Context, VerificationRequest) error { calls++; return nil }),
				authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
					calls++
					return authority.WebhookAuthority{}, nil
				}),
				admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
					calls++
					return nil, nil
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			request := validIngestRequest()
			test.mutate(&request)
			if _, err := workflow.Ingest(t.Context(), request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Ingest error = %v, want %v", err, ErrInvalidRequest)
			}
			if calls != 0 {
				t.Fatalf("downstream calls = %d, want 0", calls)
			}
		})
	}
}

func TestIngestRejectsMissingSourceRef(t *testing.T) {
	request := validIngestRequest()
	request.SourceRef = ""
	var verified VerificationRequest
	var derived AuthorityRequest
	workflow, err := New(
		verifierFunc(func(_ context.Context, request VerificationRequest) error {
			verified = request
			return nil
		}),
		authorityProviderFunc(func(_ context.Context, request AuthorityRequest) (authority.WebhookAuthority, error) {
			derived = request
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Ingest(t.Context(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Ingest missing SourceRef error = %v, want ErrInvalidRequest", err)
	}
	if verified.WorkspaceKey != "" || derived.WorkspaceKey != "" {
		t.Fatalf("missing SourceRef reached verification or authority: verification:%+v authority:%+v", verified, derived)
	}
}

func TestIngestMapsExactAutomationCommand(t *testing.T) {
	request := validIngestRequest()
	var gotVerification VerificationRequest
	var gotAuthority AuthorityRequest
	var gotCommand automation.AdmitEventCommand
	wantResult := &automation.AdmissionResult{Replayed: true}
	workflow, err := New(
		verifierFunc(func(_ context.Context, verification VerificationRequest) error {
			gotVerification = verification
			return nil
		}),
		authorityProviderFunc(func(_ context.Context, request AuthorityRequest) (authority.WebhookAuthority, error) {
			gotAuthority = request
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(_ context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			gotCommand = command
			return wantResult, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Ingest(t.Context(), request)
	if err != nil || result != wantResult {
		t.Fatalf("Ingest = (%+v, %v), want (%+v, nil)", result, err, wantResult)
	}

	wantVerification := VerificationRequest{
		WorkspaceKey:       request.WorkspaceKey,
		SourceKind:         request.SourceKind,
		SourceRef:          request.SourceRef,
		RouteKey:           request.RouteKey,
		PresentedSignature: request.PresentedSignature,
		Payload:            request.Payload,
	}
	if !reflect.DeepEqual(gotVerification, wantVerification) {
		t.Errorf("verification request = %#v, want %#v", gotVerification, wantVerification)
	}
	wantAuthority := AuthorityRequest{
		WorkspaceKey: request.WorkspaceKey,
		SourceKind:   request.SourceKind,
		SourceRef:    request.SourceRef,
		RouteKey:     request.RouteKey,
	}
	if !reflect.DeepEqual(gotAuthority, wantAuthority) {
		t.Errorf("authority request = %#v, want %#v", gotAuthority, wantAuthority)
	}
	wantCommand := automation.AdmitEventCommand{
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
		Payload:          request.Payload,
		SubjectAttrs:     request.SubjectAttrs,
	}
	if !reflect.DeepEqual(gotCommand, wantCommand) {
		t.Errorf("admission command = %#v, want %#v", gotCommand, wantCommand)
	}
	if gotCommand.ParentEventID != "" || gotCommand.EpicID != "" {
		t.Errorf("caller-controlled provenance reached admission: %+v", gotCommand)
	}

	for _, forbidden := range []string{"Origin", "HopDepth", "SignatureStatus", "IdempotencyKey", "ParentEventID", "EpicID", "DriverVersionID"} {
		if _, exists := reflect.TypeOf(IngestRequest{}).FieldByName(forbidden); exists {
			t.Errorf("IngestRequest exposes forbidden caller field %q", forbidden)
		}
	}
	for _, forbidden := range []string{"Secret", "WebhookSecret", "ResolvedSecret"} {
		if _, exists := reflect.TypeOf(IngestRequest{}).FieldByName(forbidden); exists {
			t.Errorf("IngestRequest exposes server-side secret field %q", forbidden)
		}
		if _, exists := reflect.TypeOf(VerificationRequest{}).FieldByName(forbidden); exists {
			t.Errorf("VerificationRequest exposes server-side secret field %q", forbidden)
		}
	}
}

func TestIngestDefensivelyCopiesPayloadAndAttributes(t *testing.T) {
	request := validIngestRequest()
	wantPayload := append(json.RawMessage(nil), request.Payload...)
	wantAttrs := map[string]string{"repo": "acme/widgets", "pr_number": "42"}
	workflow, err := New(
		verifierFunc(func(_ context.Context, verification VerificationRequest) error {
			verification.Payload[0] = '['
			return nil
		}),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(_ context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			if !reflect.DeepEqual(command.Payload, wantPayload) || !reflect.DeepEqual(command.SubjectAttrs, wantAttrs) {
				t.Fatalf("command payload/attrs = (%s, %v), want (%s, %v)", command.Payload, command.SubjectAttrs, wantPayload, wantAttrs)
			}
			command.Payload[0] = '['
			command.SubjectAttrs["repo"] = "mutated"
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Ingest(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Payload, wantPayload) || !reflect.DeepEqual(request.SubjectAttrs, wantAttrs) {
		t.Fatalf("caller input was mutated: payload=%s attrs=%v", request.Payload, request.SubjectAttrs)
	}
}

func TestNewAndRequestBoundsFailClosed(t *testing.T) {
	noopVerifier := verifierFunc(func(context.Context, VerificationRequest) error { return nil })
	noopAuthority := authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
		return authority.WebhookAuthority{}, nil
	})
	noopAdmission := admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
		return &automation.AdmissionResult{}, nil
	})
	if _, err := New(nil, noopAuthority, noopAdmission); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New nil verifier error = %v, want %v", err, ErrUnavailable)
	}
	if _, err := New(noopVerifier, nil, noopAdmission); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New nil authority error = %v, want %v", err, ErrUnavailable)
	}
	if _, err := New(noopVerifier, noopAuthority, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New nil admission error = %v, want %v", err, ErrUnavailable)
	}

	workflow, err := New(noopVerifier, noopAuthority, noopAdmission)
	if err != nil {
		t.Fatal(err)
	}
	oversizedPayload := validIngestRequest()
	oversizedPayload.Payload = make(json.RawMessage, MaxPayloadBytes+1)
	if _, err := workflow.Ingest(t.Context(), oversizedPayload); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized payload error = %v, want %v", err, ErrInvalidRequest)
	}
	oversizedSignature := validIngestRequest()
	oversizedSignature.PresentedSignature = string(make([]byte, MaxPresentedSignatureBytes+1))
	if _, err := workflow.Ingest(t.Context(), oversizedSignature); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized signature error = %v, want %v", err, ErrInvalidRequest)
	}
}

func TestIngestPreservesAdmissionResultAlongsideError(t *testing.T) {
	admissionErr := errors.New("one delivery dispatch failed")
	wantResult := &automation.AdmissionResult{Event: &automation.Event{EventID: "event-1"}}
	workflow, err := New(
		verifierFunc(func(context.Context, VerificationRequest) error { return nil }),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			return wantResult, admissionErr
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := workflow.Ingest(t.Context(), validIngestRequest())
	if result != wantResult || !errors.Is(err, admissionErr) {
		t.Fatalf("Ingest = (%+v, %v), want result preserved with wrapped error", result, err)
	}
}

func TestIngestVerifiesExactBytesBeforeNormalizingEmptyPayload(t *testing.T) {
	request := validIngestRequest()
	request.Payload = json.RawMessage(" \n\t")
	wantSignedBytes := append(json.RawMessage(nil), request.Payload...)
	verified := false
	workflow, err := New(
		verifierFunc(func(_ context.Context, verification VerificationRequest) error {
			verified = true
			if !reflect.DeepEqual(verification.Payload, wantSignedBytes) {
				t.Fatalf("verified payload = %q, want exact signed bytes %q", verification.Payload, wantSignedBytes)
			}
			return nil
		}),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(_ context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			if string(command.Payload) != `{}` {
				t.Fatalf("admitted payload = %q, want normalized empty object", command.Payload)
			}
			return &automation.AdmissionResult{}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Ingest(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("verifier was not called")
	}
}

func TestIngestVerifiesBeforeRejectingMalformedJSON(t *testing.T) {
	request := validIngestRequest()
	request.Payload = json.RawMessage(`{"broken"`)
	denied := errors.New("signature mismatch")

	workflow, err := New(
		verifierFunc(func(context.Context, VerificationRequest) error { return denied }),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			t.Fatal("malformed unverified payload reached authority derivation")
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			t.Fatal("malformed unverified payload reached admission")
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Ingest(t.Context(), request); !errors.Is(err, denied) {
		t.Fatalf("unverified malformed payload error = %v, want verification denial", err)
	}

	authorityCalls := 0
	workflow, err = New(
		verifierFunc(func(context.Context, VerificationRequest) error { return nil }),
		authorityProviderFunc(func(context.Context, AuthorityRequest) (authority.WebhookAuthority, error) {
			authorityCalls++
			return authority.WebhookAuthority{}, nil
		}),
		admissionFunc(func(context.Context, automation.EventAuthority, automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
			t.Fatal("malformed verified payload reached admission")
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.Ingest(t.Context(), request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("verified malformed payload error = %v, want %v", err, ErrInvalidRequest)
	}
	if authorityCalls != 0 {
		t.Fatalf("malformed payload authority calls = %d, want 0", authorityCalls)
	}
}
