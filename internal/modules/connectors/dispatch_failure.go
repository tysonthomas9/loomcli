package connectors

import (
	"errors"
	"strings"
)

type DispatchFailureKind string

const (
	DispatchFailureGrantDenied          DispatchFailureKind = "grant_denied"
	DispatchFailurePreconditionRequired DispatchFailureKind = "precondition_required"
	DispatchFailureStaleSubject         DispatchFailureKind = "stale_subject"
	DispatchFailureRateLimited          DispatchFailureKind = "rate_limited"
	DispatchFailureUpstream             DispatchFailureKind = "upstream_error"
)

// DispatchFailure is the transport-neutral classification exposed by the
// Connectors owner. Provider adapters contribute this classification through
// ConnectorFailure without leaking adapter error types to inbound surfaces.
type DispatchFailure struct {
	Kind       DispatchFailureKind
	Retryable  bool
	ErrorClass string
}

type dispatchFailureSource interface {
	ConnectorFailure() DispatchFailure
}

type PreconditionRequired struct {
	Action string
	Fields []string
}

func (failure *PreconditionRequired) Error() string {
	return "connectors: " + failure.Action + " requires precondition field(s) " + strings.Join(failure.Fields, ", ")
}

func (failure *PreconditionRequired) Unwrap() error { return ErrInvalid }

func (failure *PreconditionRequired) ConnectorFailure() DispatchFailure {
	return DispatchFailure{Kind: DispatchFailurePreconditionRequired}
}

// ClassifyDispatchError recognizes owner policy denials and structured
// provider failures anywhere in an error chain, including joined errors.
func ClassifyDispatchError(err error) (DispatchFailure, bool) {
	if errors.Is(err, ErrGrantDenied) {
		return DispatchFailure{Kind: DispatchFailureGrantDenied}, true
	}
	var source dispatchFailureSource
	if errors.As(err, &source) {
		failure := source.ConnectorFailure()
		if failure.Kind != "" {
			return failure, true
		}
	}
	return DispatchFailure{}, false
}

// DecisionForDispatchError maps provider failures to the durable connector
// call decision vocabulary. Unknown failures remain upstream_error so audit
// records never contain an invalid or empty decision.
func DecisionForDispatchError(err error) ConnectorCallDecision {
	failure, ok := ClassifyDispatchError(err)
	if !ok {
		return ConnectorCallUpstreamError
	}
	switch failure.Kind {
	case DispatchFailureStaleSubject:
		return ConnectorCallStaleSubject
	case DispatchFailurePreconditionRequired:
		return ConnectorCallPreconditionRequired
	case DispatchFailureGrantDenied:
		return ConnectorCallDenied
	default:
		return ConnectorCallUpstreamError
	}
}
