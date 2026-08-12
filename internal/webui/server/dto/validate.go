package dto

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/types"
)

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

// isAPIStatus returns true if s is a user-facing issue status.
// Internal statuses (tombstone, pinned, hooked) are excluded, and so is "".
//
// The six accepted values are not spelled out here — they are exactly
// types.UserFacingStatuses(), and re-typing them is how the same nine-word
// vocabulary came to be maintained in four places at once. Note that this is a
// wider set than types.ValidateSettableStatus admits: that guards the PATCH
// route, which sends closed and in_progress to the close and claim endpoints,
// while this only asks whether the caller named a status the API talks about.
func isAPIStatus(s string) bool {
	st := types.Status(s)
	return st.IsValid() && !st.IsSystemManaged()
}

// apiStatusList renders the statuses isAPIStatus accepts, in canonical order,
// for the validation message. Derived from the same list the check uses so the
// message cannot promise a set the check does not honor.
func apiStatusList() string {
	accepted := types.UserFacingStatuses()
	names := make([]string, len(accepted))
	for i, s := range accepted {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
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
