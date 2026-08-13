package sourcecontrol

import (
	"errors"
	"fmt"
)

var (
	ErrPayloadTooLarge      = errors.New("source control: payload too large")
	ErrPreconditionFailed   = errors.New("source control: stale version")
	ErrPreconditionRequired = errors.New("source control: version required")
	ErrTimeout              = errors.New("source control: timeout")
)

type FailureKind string

const (
	FailureInvalid              FailureKind = "invalid"
	FailureNotFound             FailureKind = "not_found"
	FailureForbidden            FailureKind = "forbidden"
	FailureConflict             FailureKind = "conflict"
	FailureUnavailable          FailureKind = "unavailable"
	FailureInternal             FailureKind = "internal"
	FailurePayloadTooLarge      FailureKind = "payload_too_large"
	FailurePreconditionFailed   FailureKind = "precondition_failed"
	FailurePreconditionRequired FailureKind = "precondition_required"
	FailureTimeout              FailureKind = "timeout"
)

// Failure is Source Control's typed, transport-neutral file-operation error.
// Delivery adapters map its public category and message; causes stay private.
type Failure struct {
	Kind    FailureKind
	Message string
	Cause   error
}

func (failure *Failure) Error() string {
	if failure == nil {
		return "source control operation failed"
	}
	if failure.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", failure.Kind, failure.Message, failure.Cause)
	}
	return fmt.Sprintf("%s: %s", failure.Kind, failure.Message)
}

func (failure *Failure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

func (failure *Failure) Is(target error) bool {
	if failure == nil {
		return false
	}
	switch failure.Kind {
	case FailureInvalid:
		return target == ErrInvalid
	case FailureNotFound:
		return target == ErrNotFound
	case FailureForbidden:
		return target == ErrForbidden
	case FailureConflict:
		return target == ErrCheckoutConflict
	case FailureUnavailable:
		return target == ErrUnavailable
	case FailurePayloadTooLarge:
		return target == ErrPayloadTooLarge
	case FailurePreconditionFailed:
		return target == ErrPreconditionFailed
	case FailurePreconditionRequired:
		return target == ErrPreconditionRequired
	case FailureTimeout:
		return target == ErrTimeout
	default:
		return false
	}
}

func newFailure(kind FailureKind, message string, cause error) *Failure {
	return &Failure{Kind: kind, Message: message, Cause: cause}
}

func newInvalid(message string) *Failure {
	return newFailure(FailureInvalid, message, nil)
}

func newNotFound(message string) *Failure {
	return newFailure(FailureNotFound, message, nil)
}

func newForbidden(message string) *Failure {
	return newFailure(FailureForbidden, message, nil)
}

func newConflict(message string) *Failure {
	return newFailure(FailureConflict, message, nil)
}

func newInternal(message string, cause error) *Failure {
	return newFailure(FailureInternal, message, cause)
}

func newPayloadTooLarge(message string) *Failure {
	return newFailure(FailurePayloadTooLarge, message, nil)
}

func newPreconditionFailed(message string) *Failure {
	return newFailure(FailurePreconditionFailed, message, nil)
}

func newPreconditionRequired(message string) *Failure {
	return newFailure(FailurePreconditionRequired, message, nil)
}

func newTimeout(message string) *Failure {
	return newFailure(FailureTimeout, message, nil)
}
