package workitems

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalid               = errors.New("invalid work item command")
	ErrNotFound              = errors.New("work item not found")
	ErrConflict              = errors.New("work item conflict")
	ErrUnavailable           = errors.New("work items unavailable")
	ErrTimeout               = errors.New("work item operation timed out")
	ErrAlreadyClosed         = errors.New("work item already closed")
	ErrNotImplemented        = errors.New("work item operation not implemented")
	ErrInvalidPersistedState = errors.New("invalid persisted work item state")
	ErrFilterNotSupported    = errors.New("work item filter not supported")
)

// ErrorKind classifies failures produced by durable Work Items adapters.
// Callers normally use errors.Is with the owner sentinels above; Kind and Meta
// exist for the few operational paths that need structured conflict evidence.
type ErrorKind string

const (
	KindNotFound       ErrorKind = "not_found"
	KindValidation     ErrorKind = "validation_error"
	KindConflict       ErrorKind = "conflict"
	KindUnavailable    ErrorKind = "unavailable"
	KindTimeout        ErrorKind = "timeout"
	KindNotImplemented ErrorKind = "not_implemented"
	KindInternal       ErrorKind = "internal"
	KindCanceled       ErrorKind = "canceled"
)

// OperationError preserves adapter operation context without exposing a
// transport-specific error vocabulary to Work Items consumers.
type OperationError struct {
	Kind    ErrorKind
	Op      string
	Message string
	Cause   error
	Meta    map[string]string
}

func (e *OperationError) Error() string {
	if e == nil {
		return "<nil Work Items error>"
	}
	prefix := "work items"
	if e.Kind != "" {
		prefix += " [" + string(e.Kind) + "]"
	}
	if e.Op != "" {
		prefix += " " + e.Op
	}
	switch {
	case e.Message != "" && e.Cause != nil:
		return fmt.Sprintf("%s: %s: %v", prefix, e.Message, e.Cause)
	case e.Message != "":
		return prefix + ": " + e.Message
	case e.Cause != nil:
		return fmt.Sprintf("%s: %v", prefix, e.Cause)
	default:
		return prefix
	}
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *OperationError) Is(target error) bool {
	if e == nil {
		return false
	}
	return target == sentinelForKind(e.Kind)
}

func sentinelForKind(kind ErrorKind) error {
	switch kind {
	case KindNotFound:
		return ErrNotFound
	case KindValidation:
		return ErrInvalid
	case KindConflict:
		return ErrConflict
	case KindUnavailable:
		return ErrUnavailable
	case KindTimeout, KindCanceled:
		return ErrTimeout
	case KindNotImplemented:
		return ErrNotImplemented
	default:
		return nil
	}
}

func NewOperationError(kind ErrorKind, op, message string, cause error) *OperationError {
	return &OperationError{Kind: kind, Op: op, Message: message, Cause: cause}
}

func IsKind(err error, kind ErrorKind) bool {
	var value *OperationError
	return errors.As(err, &value) && value.Kind == kind
}

func IsAlreadyClosedConflict(err error) bool {
	if !errors.Is(err, ErrConflict) {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "blocker") || strings.Contains(message, "blocked") || strings.Contains(message, "dependenc") {
		return false
	}
	return strings.Contains(message, "already closed") || strings.Contains(message, "is closed")
}

func AdapterNotFound(op, message string) *OperationError {
	return NewOperationError(KindNotFound, op, message, nil)
}

func AdapterInvalid(op, message string) *OperationError {
	return NewOperationError(KindValidation, op, message, nil)
}

func AdapterConflict(op, message string) *OperationError {
	return NewOperationError(KindConflict, op, message, nil)
}

func AdapterNotImplemented(op, message string) *OperationError {
	return NewOperationError(KindNotImplemented, op, message, nil)
}

func AdapterUnavailable(op, message string, cause error) *OperationError {
	return NewOperationError(KindUnavailable, op, message, cause)
}

func AdapterTimeout(op, message string, cause error) *OperationError {
	return NewOperationError(KindTimeout, op, message, cause)
}

func AdapterInternal(op, message string, cause error) *OperationError {
	return NewOperationError(KindInternal, op, message, cause)
}

func AdapterCanceled(op, message string, cause error) *OperationError {
	return NewOperationError(KindCanceled, op, message, cause)
}

// PublicErrorMessage removes the classification sentinel from a public error
// while preserving its safe, capability-owned explanation.
func PublicErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, sentinel := range []error{ErrInvalid, ErrNotFound, ErrConflict, ErrUnavailable, ErrTimeout, ErrNotImplemented} {
		if errors.Is(err, sentinel) {
			return strings.TrimSuffix(message, ": "+sentinel.Error())
		}
	}
	return "internal server error"
}
