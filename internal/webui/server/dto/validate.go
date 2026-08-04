package dto

import "fmt"

// Validation limit constants. Values match authoritative sources:
//   - MaxTitleLength: entity/issue.go:169
//   - MaxLabels: service/issue_impl.go:28
//   - MaxDependencies: service/issue_impl.go:29
const (
	MaxTitleLength  = 500
	MaxLabels       = 50
	MaxDependencies = 100
)

// ValidationError holds multiple field-level validation errors collected in
// a single pass. Callers can use errors.As to extract it from the returned error.
type ValidationError struct {
	Errors []FieldError
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return fmt.Sprintf("validation failed: %s: %s", e.Errors[0].Field, e.Errors[0].Message)
	}
	return fmt.Sprintf("validation failed: %d field errors", len(e.Errors))
}

// FieldMap returns a map of field name to error message, suitable for
// ErrorResponse.Details. If multiple errors exist for the same field,
// only the first is included.
func (e *ValidationError) FieldMap() map[string]any {
	m := make(map[string]any, len(e.Errors))
	for _, fe := range e.Errors {
		if _, ok := m[fe.Field]; !ok {
			m[fe.Field] = fe.Message
		}
	}
	return m
}

// FieldError represents a single validation error on a specific field.
type FieldError struct {
	Field   string // JSON field name (e.g., "title", "issue_type")
	Message string // Human-readable error (e.g., "is required")
}

// isAPIStatus returns true if s is a user-settable issue status.
// Internal statuses (tombstone, pinned, hooked) are excluded.
func isAPIStatus(s string) bool {
	switch s {
	case "open", "in_progress", "blocked", "deferred", "review", "closed":
		return true
	}
	return false
}

// validationBuilder collects field errors during a single validation pass.
type validationBuilder struct {
	errors []FieldError
}

func (b *validationBuilder) add(field, message string) {
	b.errors = append(b.errors, FieldError{Field: field, Message: message})
}

func (b *validationBuilder) build() error {
	if len(b.errors) == 0 {
		return nil
	}
	return &ValidationError{Errors: b.errors}
}
