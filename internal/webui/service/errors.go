// Package service defines shared types for the webui service layer.
package service

import "fmt"

// ErrorKind categorizes service-layer errors into domain-level failure modes.
// The handler layer maps these to HTTP status codes; services never reference HTTP.
type ErrorKind string

const (
	KindNotFound        ErrorKind = "not_found"
	KindValidation      ErrorKind = "validation_error"
	KindUnavailable     ErrorKind = "unavailable"
	KindTimeout         ErrorKind = "timeout"
	KindConflict        ErrorKind = "conflict"
	KindInternal        ErrorKind = "internal"
	KindForbidden       ErrorKind = "forbidden"
	KindUnauthorized    ErrorKind = "unauthorized"
	KindLocked          ErrorKind = "locked"
	KindPayloadTooLarge ErrorKind = "payload_too_large"
	KindRateLimited     ErrorKind = "rate_limited"
	KindBadGateway      ErrorKind = "bad_gateway"
	KindNotImplemented  ErrorKind = "not_implemented"
)

// ServiceError represents a typed service-layer error.
// Services return *ServiceError to indicate categorized failures.
// Handlers use errors.As to extract the Kind for HTTP status mapping.
type ServiceError struct {
	Kind    ErrorKind
	Message string
	Cause   error
}

func (e *ServiceError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *ServiceError) Unwrap() error { return e.Cause }

// NewServiceError creates a ServiceError with the given kind, message, and optional cause.
func NewServiceError(kind ErrorKind, msg string, cause error) *ServiceError {
	return &ServiceError{Kind: kind, Message: msg, Cause: cause}
}

func ErrNotFound(msg string) *ServiceError {
	return &ServiceError{Kind: KindNotFound, Message: msg}
}

func ErrValidation(msg string) *ServiceError {
	return &ServiceError{Kind: KindValidation, Message: msg}
}

func ErrUnavailable(msg string) *ServiceError {
	return &ServiceError{Kind: KindUnavailable, Message: msg}
}

func ErrTimeout(msg string) *ServiceError {
	return &ServiceError{Kind: KindTimeout, Message: msg}
}

func ErrConflict(msg string) *ServiceError {
	return &ServiceError{Kind: KindConflict, Message: msg}
}

func ErrInternal(msg string, cause error) *ServiceError {
	return &ServiceError{Kind: KindInternal, Message: msg, Cause: cause}
}

func ErrForbidden(msg string) *ServiceError {
	return &ServiceError{Kind: KindForbidden, Message: msg}
}

func ErrUnauthorized(msg string) *ServiceError {
	return &ServiceError{Kind: KindUnauthorized, Message: msg}
}

func ErrLocked(msg string) *ServiceError {
	return &ServiceError{Kind: KindLocked, Message: msg}
}

func ErrPayloadTooLarge(msg string) *ServiceError {
	return &ServiceError{Kind: KindPayloadTooLarge, Message: msg}
}

func ErrRateLimited(msg string) *ServiceError {
	return &ServiceError{Kind: KindRateLimited, Message: msg}
}

func ErrBadGateway(msg string) *ServiceError {
	return &ServiceError{Kind: KindBadGateway, Message: msg}
}

func ErrNotImplemented(msg string) *ServiceError {
	return &ServiceError{Kind: KindNotImplemented, Message: msg}
}
