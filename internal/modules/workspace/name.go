package workspace

import (
	"errors"
	"fmt"
)

// MaxNameLength is the capability-owned upper bound for Workspace names.
const MaxNameLength = 64

// NameValidationKind lets transport and compatibility adapters preserve their
// public error vocabulary without reimplementing Workspace naming policy.
type NameValidationKind string

const (
	NameRequired          NameValidationKind = "required"
	NameTooLong           NameValidationKind = "too_long"
	NameInvalidCharacters NameValidationKind = "invalid_characters"
)

type nameValidationError struct {
	kind NameValidationKind
}

func (err *nameValidationError) Error() string {
	switch err.kind {
	case NameRequired:
		return "workspace name is required"
	case NameTooLong:
		return fmt.Sprintf("workspace name is too long (max %d characters)", MaxNameLength)
	case NameInvalidCharacters:
		return "workspace name must contain only alphanumeric characters, hyphens, and underscores"
	default:
		return "workspace name is invalid"
	}
}

func (*nameValidationError) Unwrap() error { return ErrInvalid }

// NameValidationKindOf classifies a ValidateName error for adapter-specific
// presentation. It returns false for errors outside Workspace name policy.
func NameValidationKindOf(err error) (NameValidationKind, bool) {
	var validation *nameValidationError
	if !errors.As(err, &validation) {
		return "", false
	}
	return validation.kind, true
}

// ValidateName is the single Workspace naming policy used by commands and all
// inbound/persistence adapters.
func ValidateName(name string) error {
	if name == "" {
		return &nameValidationError{kind: NameRequired}
	}
	if len(name) > MaxNameLength {
		return &nameValidationError{kind: NameTooLong}
	}
	for _, value := range name {
		if !((value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || value == '-' || value == '_') {
			return &nameValidationError{kind: NameInvalidCharacters}
		}
	}
	return nil
}
