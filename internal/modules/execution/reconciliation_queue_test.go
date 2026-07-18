package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type reconciliationQueuePortStub struct {
	awaitValues   []AwaitEventNotification
	outcomeValues []DriverRunOutcome
	awaitLease    AwaitEventNotificationLease
	outcomeLease  DriverRunOutcomeLease
	awaitRetry    AwaitEventNotificationRetry
	outcomeRetry  DriverRunOutcomeRetry
	awaitDone     AwaitEventNotificationCompletion
	outcomeDone   DriverRunOutcomeCompletion
	claimCalls    int
}

func (stub *reconciliationQueuePortStub) ClaimAwaitEventNotifications(
	_ context.Context,
	lease AwaitEventNotificationLease,
) ([]AwaitEventNotification, error) {
	stub.claimCalls++
	stub.awaitLease = lease
	return stub.awaitValues, nil
}

func (stub *reconciliationQueuePortStub) CompleteAwaitEventNotification(
	_ context.Context,
	completion AwaitEventNotificationCompletion,
) error {
	stub.awaitDone = completion
	return nil
}

func (stub *reconciliationQueuePortStub) RetryAwaitEventNotification(
	_ context.Context,
	retry AwaitEventNotificationRetry,
) error {
	stub.awaitRetry = retry
	return nil
}

func (stub *reconciliationQueuePortStub) ClaimDriverRunOutcomes(
	_ context.Context,
	lease DriverRunOutcomeLease,
) ([]DriverRunOutcome, error) {
	stub.claimCalls++
	stub.outcomeLease = lease
	return stub.outcomeValues, nil
}

func (stub *reconciliationQueuePortStub) CompleteDriverRunOutcome(
	_ context.Context,
	completion DriverRunOutcomeCompletion,
) error {
	stub.outcomeDone = completion
	return nil
}

func (stub *reconciliationQueuePortStub) RetryDriverRunOutcome(
	_ context.Context,
	retry DriverRunOutcomeRetry,
) error {
	stub.outcomeRetry = retry
	return nil
}

func TestReconciliationQueueClaimDerivesLeaseAndDoesNotAliasPayload(t *testing.T) {
	now := time.Now().UTC()
	payload := []byte(`{"ok":true}`)
	port := &reconciliationQueuePortStub{awaitValues: []AwaitEventNotification{{
		Event: AwaitEvent{WorkspaceKey: "TEST", EventID: "event-1", Payload: payload}, Attempt: 1,
	}}}
	service, issuer := newTestService(t, Dependencies{AwaitEvents: port, RunOutcomes: port})
	values, err := service.ClaimAwaitEventNotifications(
		t.Context(),
		issueSystem(t, issuer, ActionClaimAwaitEventNotifications),
		ClaimAwaitEventNotificationsCommand{WorkspaceKey: "TEST", ClaimID: "claim-1", Before: now, Limit: 7},
	)
	if err != nil {
		t.Fatal(err)
	}
	if port.awaitLease.ClaimUntil.Sub(port.awaitLease.Before) != ReconciliationClaimLease ||
		port.awaitLease.Limit != 7 || len(values) != 1 {
		t.Fatalf("lease=%+v values=%+v", port.awaitLease, values)
	}
	values[0].Event.Payload[0] = 'x'
	if string(payload) != `{"ok":true}` {
		t.Fatalf("queue result aliased persistence payload: %q", payload)
	}
}

func TestReconciliationQueueRejectsWrongAuthorityAndUnboundedClaim(t *testing.T) {
	port := &reconciliationQueuePortStub{}
	service, issuer := newTestService(t, Dependencies{AwaitEvents: port})
	command := ClaimAwaitEventNotificationsCommand{
		WorkspaceKey: "TEST", ClaimID: "claim-1", Before: time.Now().UTC(), Limit: 1,
	}
	if _, err := service.ClaimAwaitEventNotifications(
		t.Context(), issueSystem(t, issuer, ActionClaimDriverRunOutcomes), command,
	); err == nil {
		t.Fatal("wrong-action system authority was accepted")
	}
	command.Limit = MaxReconciliationQueueLimit + 1
	if _, err := service.ClaimAwaitEventNotifications(
		t.Context(), issueSystem(t, issuer, ActionClaimAwaitEventNotifications), command,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded claim error = %v", err)
	}
	if port.claimCalls != 0 {
		t.Fatalf("invalid claims reached port %d times", port.claimCalls)
	}
}

func TestReconciliationQueueOwnsRetryBackoffAndBoundedError(t *testing.T) {
	now := time.Now().UTC()
	port := &reconciliationQueuePortStub{}
	service, issuer := newTestService(t, Dependencies{AwaitEvents: port, RunOutcomes: port})
	cause := strings.Repeat("é", reconciliationErrorLimit)
	if err := service.RetryAwaitEventNotification(
		t.Context(), issueSystem(t, issuer, ActionRetryAwaitEventNotification),
		RetryAwaitEventNotificationCommand{
			WorkspaceKey: "TEST", EventID: "event-1", ClaimID: "claim-1",
			Attempt: 1, FailedAt: now, Cause: cause,
		},
	); err != nil {
		t.Fatal(err)
	}
	if !port.awaitRetry.AvailableAt.Equal(now.Add(time.Second)) ||
		len(port.awaitRetry.Error) > reconciliationErrorLimit || !utf8.ValidString(port.awaitRetry.Error) {
		t.Fatalf("await retry = %+v", port.awaitRetry)
	}
	if err := service.RetryDriverRunOutcome(
		t.Context(), issueSystem(t, issuer, ActionRetryDriverRunOutcome),
		RetryDriverRunOutcomeCommand{
			WorkspaceKey: "TEST", RunID: "run-1", ClaimID: "claim-2",
			Attempt: 100, FailedAt: now, Cause: "still unavailable",
		},
	); err != nil {
		t.Fatal(err)
	}
	if !port.outcomeRetry.AvailableAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("capped retry = %+v", port.outcomeRetry)
	}
}

func TestReconciliationQueueCompletionIsExactWorkspaceAuthorized(t *testing.T) {
	now := time.Now().UTC()
	port := &reconciliationQueuePortStub{}
	service, issuer := newTestService(t, Dependencies{RunOutcomes: port})
	auth := issueSystem(t, issuer, ActionCompleteDriverRunOutcome)
	if err := service.CompleteDriverRunOutcome(t.Context(), auth, CompleteDriverRunOutcomeCommand{
		WorkspaceKey: "OTHER", RunID: "run-1", ClaimID: "claim-1", CompletedAt: now,
	}); err == nil {
		t.Fatal("cross-workspace completion authority was accepted")
	}
	if port.outcomeDone.RunID != "" {
		t.Fatalf("denied completion reached port: %+v", port.outcomeDone)
	}
}
