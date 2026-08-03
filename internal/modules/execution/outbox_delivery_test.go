package execution

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type outboxDeliveryPortStub struct {
	listCalls   int
	listQuery   OutboxDeliveryQuery
	listResult  []OutboxDelivery
	listErr     error
	recordCalls int
	recordInput OutboxDeliveryResult
	recordValue *OutboxDelivery
	recordErr   error
}

func (stub *outboxDeliveryPortStub) ListDueOutboxDeliveries(
	_ context.Context,
	query OutboxDeliveryQuery,
) ([]OutboxDelivery, error) {
	stub.listCalls++
	stub.listQuery = query
	return stub.listResult, stub.listErr
}

func (stub *outboxDeliveryPortStub) RecordOutboxDeliveryResult(
	_ context.Context,
	result OutboxDeliveryResult,
) (*OutboxDelivery, error) {
	stub.recordCalls++
	stub.recordInput = result
	return stub.recordValue, stub.recordErr
}

func TestListDueOutboxDeliveriesRequiresExactSystemAuthorityAndValidCommand(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 30, 0, 0, time.UTC)
	port := &outboxDeliveryPortStub{}
	service, issuer := newTestService(t, Dependencies{OutboxDeliveries: port})
	valid := ListDueOutboxDeliveriesCommand{WorkspaceKey: " TEST ", Now: now, Limit: 17}

	for name, test := range map[string]struct {
		auth    authority.SystemAuthority
		command ListDueOutboxDeliveriesCommand
		wantErr error
	}{
		"wrong action": {
			auth: issueSystem(t, issuer, ActionRecordOutboxDeliveryResult), command: valid,
			wantErr: authority.ErrAdmissionDenied,
		},
		"wrong workspace": {
			auth:    issueSystem(t, issuer, ActionListDueOutboxDeliveries),
			command: ListDueOutboxDeliveriesCommand{WorkspaceKey: "OTHER", Now: now, Limit: 17},
			wantErr: authority.ErrAdmissionDenied,
		},
		"empty workspace": {
			auth:    issueSystem(t, issuer, ActionListDueOutboxDeliveries),
			command: ListDueOutboxDeliveriesCommand{Now: now, Limit: 17},
			wantErr: ErrInvalid,
		},
		"zero time": {
			auth:    issueSystem(t, issuer, ActionListDueOutboxDeliveries),
			command: ListDueOutboxDeliveriesCommand{WorkspaceKey: "TEST", Limit: 17},
			wantErr: ErrInvalid,
		},
		"zero limit": {
			auth:    issueSystem(t, issuer, ActionListDueOutboxDeliveries),
			command: ListDueOutboxDeliveriesCommand{WorkspaceKey: "TEST", Now: now},
			wantErr: ErrInvalid,
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := port.listCalls
			if _, err := service.ListDueOutboxDeliveries(t.Context(), test.auth, test.command); !errors.Is(err, test.wantErr) {
				t.Fatalf("ListDueOutboxDeliveries error = %v, want %v", err, test.wantErr)
			}
			if port.listCalls != before {
				t.Fatalf("list port called for rejected command: before=%d after=%d", before, port.listCalls)
			}
		})
	}

	want := []OutboxDelivery{{
		WorkspaceKey: "TEST", OutboxID: "outbox-1", Kind: OutboxKindLeadTaskMessage,
		DriverRunID: "driver-1", TaskRunID: "task-1", TargetAgent: "lead", Body: "done",
		DedupeKey: "dedupe-1", Status: OutboxDeliveryStatusPending, Attempt: 2, NextRetryAt: &now,
	}}
	port.listResult = want
	got, err := service.ListDueOutboxDeliveries(
		t.Context(), issueSystem(t, issuer, ActionListDueOutboxDeliveries), valid,
	)
	if err != nil {
		t.Fatalf("ListDueOutboxDeliveries valid command: %v", err)
	}
	if port.listCalls != 1 || port.listQuery != (OutboxDeliveryQuery{WorkspaceKey: "TEST", Now: now, Limit: 17}) {
		t.Fatalf("list calls/query = %d / %#v", port.listCalls, port.listQuery)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("list result = %#v, want %#v", got, want)
	}
	got[0].OutboxID = "mutated"
	if port.listResult[0].OutboxID != "outbox-1" {
		t.Fatal("service returned the port-owned list backing array")
	}
}

func TestRecordOutboxDeliveryResultRequiresExactAuthorityAndValidGeneration(t *testing.T) {
	retryAt := time.Date(2026, 8, 2, 12, 31, 0, 0, time.UTC)
	port := &outboxDeliveryPortStub{}
	service, issuer := newTestService(t, Dependencies{OutboxDeliveries: port})
	valid := RecordOutboxDeliveryResultCommand{
		WorkspaceKey: " TEST ", OutboxID: " outbox-1 ", Status: OutboxDeliveryStatusPending,
		Attempt: 3, NextRetryAt: &retryAt, LastError: "busy", InboxMessageID: "message-1",
	}

	for name, test := range map[string]struct {
		auth    authority.SystemAuthority
		command RecordOutboxDeliveryResultCommand
		wantErr error
	}{
		"wrong action": {
			auth: issueSystem(t, issuer, ActionListDueOutboxDeliveries), command: valid,
			wantErr: authority.ErrAdmissionDenied,
		},
		"wrong workspace": {
			auth:    issueSystem(t, issuer, ActionRecordOutboxDeliveryResult),
			command: RecordOutboxDeliveryResultCommand{WorkspaceKey: "OTHER", OutboxID: "outbox-1", Status: OutboxDeliveryStatusDelivered, Attempt: 1},
			wantErr: authority.ErrAdmissionDenied,
		},
		"empty outbox id": {
			auth:    issueSystem(t, issuer, ActionRecordOutboxDeliveryResult),
			command: RecordOutboxDeliveryResultCommand{WorkspaceKey: "TEST", Status: OutboxDeliveryStatusDelivered, Attempt: 1},
			wantErr: ErrInvalid,
		},
		"zero attempt": {
			auth:    issueSystem(t, issuer, ActionRecordOutboxDeliveryResult),
			command: RecordOutboxDeliveryResultCommand{WorkspaceKey: "TEST", OutboxID: "outbox-1", Status: OutboxDeliveryStatusDelivered},
			wantErr: ErrInvalid,
		},
		"unknown status": {
			auth:    issueSystem(t, issuer, ActionRecordOutboxDeliveryResult),
			command: RecordOutboxDeliveryResultCommand{WorkspaceKey: "TEST", OutboxID: "outbox-1", Status: "unknown", Attempt: 1},
			wantErr: ErrInvalid,
		},
		"pending without retry time": {
			auth:    issueSystem(t, issuer, ActionRecordOutboxDeliveryResult),
			command: RecordOutboxDeliveryResultCommand{WorkspaceKey: "TEST", OutboxID: "outbox-1", Status: OutboxDeliveryStatusPending, Attempt: 1},
			wantErr: ErrInvalid,
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := port.recordCalls
			if _, err := service.RecordOutboxDeliveryResult(t.Context(), test.auth, test.command); !errors.Is(err, test.wantErr) {
				t.Fatalf("RecordOutboxDeliveryResult error = %v, want %v", err, test.wantErr)
			}
			if port.recordCalls != before {
				t.Fatalf("record port called for rejected command: before=%d after=%d", before, port.recordCalls)
			}
		})
	}

	want := &OutboxDelivery{
		WorkspaceKey: "TEST", OutboxID: "outbox-1", Kind: OutboxKindLeadAssignment,
		Status: OutboxDeliveryStatusPending, Attempt: 3, NextRetryAt: &retryAt, LastError: "busy",
	}
	port.recordValue = want
	got, err := service.RecordOutboxDeliveryResult(
		t.Context(), issueSystem(t, issuer, ActionRecordOutboxDeliveryResult), valid,
	)
	if err != nil {
		t.Fatalf("RecordOutboxDeliveryResult valid command: %v", err)
	}
	if port.recordCalls != 1 {
		t.Fatalf("record calls = %d, want 1", port.recordCalls)
	}
	wantInput := OutboxDeliveryResult{
		WorkspaceKey: "TEST", OutboxID: "outbox-1", Status: OutboxDeliveryStatusPending,
		Attempt: 3, NextRetryAt: &retryAt, LastError: "busy", InboxMessageID: "message-1",
	}
	if !reflect.DeepEqual(port.recordInput, wantInput) || got != want {
		t.Fatalf("record input/result = %#v / %#v, want %#v / %#v", port.recordInput, got, wantInput, want)
	}

	for _, status := range []OutboxDeliveryStatus{
		OutboxDeliveryStatusDelivered, OutboxDeliveryStatusUnsupported, OutboxDeliveryStatusFailed,
	} {
		if _, err := service.RecordOutboxDeliveryResult(
			t.Context(), issueSystem(t, issuer, ActionRecordOutboxDeliveryResult),
			RecordOutboxDeliveryResultCommand{WorkspaceKey: "TEST", OutboxID: "outbox-1", Status: status, Attempt: 4},
		); err != nil {
			t.Fatalf("terminal status %q rejected: %v", status, err)
		}
	}
}

func TestOutboxDeliveryCommandsRequireConfiguredPort(t *testing.T) {
	service, issuer := newTestService(t, Dependencies{})
	if _, err := service.ListDueOutboxDeliveries(
		t.Context(), issueSystem(t, issuer, ActionListDueOutboxDeliveries),
		ListDueOutboxDeliveriesCommand{WorkspaceKey: "TEST", Now: time.Now().UTC(), Limit: 1},
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ListDueOutboxDeliveries without port error = %v, want ErrUnavailable", err)
	}
	if _, err := service.RecordOutboxDeliveryResult(
		t.Context(), issueSystem(t, issuer, ActionRecordOutboxDeliveryResult),
		RecordOutboxDeliveryResultCommand{WorkspaceKey: "TEST", OutboxID: "outbox-1", Status: OutboxDeliveryStatusDelivered, Attempt: 1},
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("RecordOutboxDeliveryResult without port error = %v, want ErrUnavailable", err)
	}
}
