package service

// ServiceError represents a typed error from the service layer.
// Handlers inspect Kind to select the appropriate HTTP status code.
type ServiceError struct {
	Kind    string // NotFound, ValidationError, Unavailable, Timeout, Conflict, Internal
	Message string
	Cause   error
}

func (e *ServiceError) Error() string { return e.Message }
func (e *ServiceError) Unwrap() error { return e.Cause }

// Kind constants for ServiceError.
const (
	KindNotFound    = "NotFound"
	KindValidation  = "ValidationError"
	KindUnavailable = "Unavailable"
	KindTimeout     = "Timeout"
	KindConflict    = "Conflict"
	KindInternal    = "Internal"
	KindForbidden   = "Forbidden"
)

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
