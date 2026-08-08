package automation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type approvalEventStoreFake struct {
	event *Event
	err   error
}

func (store *approvalEventStoreFake) AppendTriggerEvent(_ context.Context, event *Event) (*Event, error) {
	if store.err != nil {
		return nil, store.err
	}
	store.event = cloneEvent(event)
	return cloneEvent(event), nil
}

func TestJournalApprovalOwnsSessionEnvelopeAndAuthority(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(authority.OperatorOnly(ActionJournalApproval))
	if err != nil {
		t.Fatal(err)
	}
	auth := approvalAuthority(t, issuer, "WS", "reviewer@example.test")
	store := &approvalEventStoreFake{}
	service := New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, admission,
		WithClock(func() time.Time { return now }), WithApprovalEventStore(store))
	payload := json.RawMessage(`{"decision":"approved"}`)

	result, err := service.JournalApproval(t.Context(), auth, JournalApprovalCommand{
		WorkspaceKey: "WS", EventID: "approval-1", EventType: ApprovalEventType,
		SubjectRef: "deploy-1", ActorRef: "reviewer@example.test", Payload: payload,
	})
	if err != nil {
		t.Fatalf("JournalApproval: %v", err)
	}
	if result == nil || store.event == nil || result.EventID != "approval-1" ||
		result.SourceKind != ApprovalSourceKind || result.SourceEventID != "approval-1" ||
		result.EventType != ApprovalEventType || result.SubjectRef != "deploy-1" ||
		result.ActorRef != "reviewer@example.test" || result.Origin != EventOriginExternal ||
		result.SignatureStatus != SignatureStatusSession || result.IdempotencyKey != "approval:WS:approval-1" ||
		result.RawPayloadDigest == "" || !result.OccurredAt.Equal(now) || !result.ReceivedAt.Equal(now) ||
		string(result.Payload) != string(payload) {
		t.Fatalf("approval event = %#v", result)
	}
	result.Payload[0] = 'x'
	if string(store.event.Payload) != string(payload) {
		t.Fatal("caller mutated stored approval payload")
	}
}

func TestJournalApprovalFailsClosed(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(authority.OperatorOnly(ActionJournalApproval))
	if err != nil {
		t.Fatal(err)
	}
	auth := approvalAuthority(t, issuer, "WS", "reviewer@example.test")
	base := JournalApprovalCommand{
		WorkspaceKey: "WS", EventID: "approval-1", EventType: ApprovalEventType,
		SubjectRef: "deploy-1", ActorRef: "reviewer@example.test", Payload: json.RawMessage(`{}`),
	}
	tests := []struct {
		name    string
		service *Service
		mutate  func(*JournalApprovalCommand)
		want    error
	}{
		{name: "missing store", service: New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, admission), want: ErrUnavailable},
		{name: "forged actor", service: New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, admission, WithApprovalEventStore(&approvalEventStoreFake{})), mutate: func(command *JournalApprovalCommand) { command.ActorRef = "other@example.test" }, want: authority.ErrAdmissionDenied},
		{name: "wrong event type", service: New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, admission, WithApprovalEventStore(&approvalEventStoreFake{})), mutate: func(command *JournalApprovalCommand) { command.EventType = "run.finished" }, want: ErrInvalid},
		{name: "oversized payload", service: New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, admission, WithApprovalEventStore(&approvalEventStoreFake{})), mutate: func(command *JournalApprovalCommand) { command.Payload = make([]byte, MaxApprovalPayloadBytes+1) }, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := base
			if test.mutate != nil {
				test.mutate(&command)
			}
			if _, err := test.service.JournalApproval(t.Context(), auth, command); !errors.Is(err, test.want) {
				t.Fatalf("JournalApproval error = %v, want %v", err, test.want)
			}
		})
	}
}

func approvalAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace,
	actor string,
) authority.OperatorAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: actor, Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{ActionJournalApproval}, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := issuer.IssueOperator(principal, workspace, ActionJournalApproval)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
