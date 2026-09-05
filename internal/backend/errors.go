package backend

import (
	"errors"
	"fmt"
	"strings"
)

// ErrFilterNotSupported is a sentinel error indicating that a backend does not
// support one or more requested filter fields. Callers check with
// errors.Is(err, ErrFilterNotSupported). The wrapping error message lists the
// specific unsupported field names.
var ErrFilterNotSupported = errors.New("filter not supported by backend")

// ErrMutationCursorExpired marks a mutation cursor that fell behind the
// backend's retention floor. A wrapping BackendError may carry that floor in
// Meta["cursor"].
var ErrMutationCursorExpired = errors.New("mutation cursor expired")

// ErrorKind categorizes backend-layer errors into domain-level failure modes.
// The service layer maps these to service.ErrorKind for HTTP status mapping.
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

// BackendError represents a typed backend-layer error.
// Implementations of IssueBackend return *BackendError to indicate
// categorized failures. The service layer extracts the Kind via
// errors.As for mapping to service.ErrorKind.
type BackendError struct {
	Kind    ErrorKind
	Op      string // The backend operation that failed (e.g., "Get", "List").
	Message string
	Cause   error
	// Meta carries optional structured details from the underlying transport.
	// fleet-db responses populate it from the JSON error envelope's "meta"
	// field (e.g., {"existing_owner": "..."} on a claim conflict). Empty when
	// no structured details are available.
	Meta map[string]string
}

// Error returns a formatted error string. It is safe to call on a nil receiver.
func (e *BackendError) Error() string {
	if e == nil {
		return "<nil BackendError>"
	}
	switch {
	case e.Op != "" && e.Message != "" && e.Cause != nil:
		return fmt.Sprintf("backend [%s] %s: %s: %v", e.Kind, e.Op, e.Message, e.Cause)
	case e.Op != "" && e.Message != "":
		return fmt.Sprintf("backend [%s] %s: %s", e.Kind, e.Op, e.Message)
	case e.Op != "" && e.Cause != nil:
		return fmt.Sprintf("backend [%s] %s: %v", e.Kind, e.Op, e.Cause)
	case e.Op != "":
		return fmt.Sprintf("backend [%s] %s", e.Kind, e.Op)
	case e.Message != "" && e.Cause != nil:
		return fmt.Sprintf("backend [%s]: %s: %v", e.Kind, e.Message, e.Cause)
	case e.Message != "":
		return fmt.Sprintf("backend [%s]: %s", e.Kind, e.Message)
	case e.Cause != nil:
		return fmt.Sprintf("backend [%s]: %v", e.Kind, e.Cause)
	default:
		return fmt.Sprintf("backend [%s]", e.Kind)
	}
}

// Unwrap returns the underlying cause, enabling errors.Is/As chain traversal.
func (e *BackendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewBackendError creates a BackendError with the given kind, operation, message, and optional cause.
func NewBackendError(kind ErrorKind, op, msg string, cause error) *BackendError {
	return &BackendError{Kind: kind, Op: op, Message: msg, Cause: cause}
}

// IsKind reports whether err (or any error in its chain) is a *BackendError
// with the given ErrorKind. Returns false for nil errors.
func IsKind(err error, kind ErrorKind) bool {
	var be *BackendError
	if errors.As(err, &be) {
		return be.Kind == kind
	}
	return false
}

// IsAlreadyClosedConflict reports whether err is the "issue is already
// closed" close conflict — the one close failure that means the desired
// state is already true, so callers may treat it as an idempotent success.
// Deliberately narrow: other KindConflict closes (open blockers,
// dependencies, claim races) must keep failing.
func IsAlreadyClosedConflict(err error) bool {
	if !IsKind(err, KindConflict) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "blocker") || strings.Contains(msg, "blocked") || strings.Contains(msg, "dependenc") {
		return false
	}
	return strings.Contains(msg, "already closed") || strings.Contains(msg, "is closed")
}

// Convenience constructors — one per ErrorKind.
// Constructors for kinds that typically originate from known conditions
// (ErrNotFound, ErrValidation, ErrConflict, ErrNotImplemented) take only (op, msg).
// Constructors for kinds that wrap transport/system errors
// (ErrUnavailable, ErrTimeout, ErrInternal, ErrCanceled) also take a cause.

// ErrNotFound creates a KindNotFound error for missing resources.
func ErrNotFound(op, msg string) *BackendError {
	return &BackendError{Kind: KindNotFound, Op: op, Message: msg}
}

// ErrValidation creates a KindValidation error for invalid input.
func ErrValidation(op, msg string) *BackendError {
	return &BackendError{Kind: KindValidation, Op: op, Message: msg}
}

// ErrConflict creates a KindConflict error for state conflicts (e.g., claim races).
func ErrConflict(op, msg string) *BackendError {
	return &BackendError{Kind: KindConflict, Op: op, Message: msg}
}

// ErrNotImplemented creates a KindNotImplemented error for unsupported operations.
func ErrNotImplemented(op, msg string) *BackendError {
	return &BackendError{Kind: KindNotImplemented, Op: op, Message: msg}
}

// ErrUnavailable creates a KindUnavailable error when the backend is unreachable.
func ErrUnavailable(op, msg string, cause error) *BackendError {
	return &BackendError{Kind: KindUnavailable, Op: op, Message: msg, Cause: cause}
}

// ErrTimeout creates a KindTimeout error when an operation exceeds its deadline.
func ErrTimeout(op, msg string, cause error) *BackendError {
	return &BackendError{Kind: KindTimeout, Op: op, Message: msg, Cause: cause}
}

// ErrInternal creates a KindInternal error for unexpected backend failures.
func ErrInternal(op, msg string, cause error) *BackendError {
	return &BackendError{Kind: KindInternal, Op: op, Message: msg, Cause: cause}
}

// ErrCanceled creates a KindCanceled error when an operation is canceled via context.
func ErrCanceled(op, msg string, cause error) *BackendError {
	return &BackendError{Kind: KindCanceled, Op: op, Message: msg, Cause: cause}
}
