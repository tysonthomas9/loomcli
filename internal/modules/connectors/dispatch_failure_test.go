package connectors

import (
	"errors"
	"fmt"
	"testing"
)

type classifiedDispatchError struct {
	failure DispatchFailure
}

func (err *classifiedDispatchError) Error() string { return "classified dispatch error" }

func (err *classifiedDispatchError) ConnectorFailure() DispatchFailure { return err.failure }

func TestClassifyDispatchError(t *testing.T) {
	upstream := &classifiedDispatchError{failure: DispatchFailure{
		Kind: DispatchFailureUpstream, Retryable: true, ErrorClass: "network",
	}}
	got, ok := ClassifyDispatchError(fmt.Errorf("wrapped: %w", upstream))
	if !ok || got != upstream.failure {
		t.Fatalf("classification = %+v, %v, want %+v", got, ok, upstream.failure)
	}

	denied := &GrantDenied{Reason: GrantDenyNoGrants}
	got, ok = ClassifyDispatchError(errors.Join(errors.New("legacy compatibility"), denied))
	if !ok || got.Kind != DispatchFailureGrantDenied || got.Retryable {
		t.Fatalf("grant classification = %+v, %v", got, ok)
	}

	if got, ok = ClassifyDispatchError(errors.New("unknown")); ok || got != (DispatchFailure{}) {
		t.Fatalf("unknown classification = %+v, %v", got, ok)
	}
}

func TestDecisionForDispatchError(t *testing.T) {
	tests := []struct {
		kind DispatchFailureKind
		want ConnectorCallDecision
	}{
		{DispatchFailureStaleSubject, ConnectorCallStaleSubject},
		{DispatchFailurePreconditionRequired, ConnectorCallPreconditionRequired},
		{DispatchFailureRateLimited, ConnectorCallUpstreamError},
		{DispatchFailureUpstream, ConnectorCallUpstreamError},
	}
	for _, test := range tests {
		err := &classifiedDispatchError{failure: DispatchFailure{Kind: test.kind}}
		if got := DecisionForDispatchError(err); got != test.want {
			t.Errorf("kind %q decision = %q, want %q", test.kind, got, test.want)
		}
	}
	if got := DecisionForDispatchError(errors.New("unknown")); got != ConnectorCallUpstreamError {
		t.Fatalf("unknown decision = %q", got)
	}
}
