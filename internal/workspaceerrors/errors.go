package workspaceerrors

import "fmt"

// Code categorizes workspace creation failures.
type Code int

const (
	AlreadyExists     Code = iota // Workspace already exists at target path
	PathNotFound                  // Parent directory does not exist
	NotGitRepo                    // Source path is not a git repository
	GitFailed                     // Git operation (clone, worktree add, etc.) failed
	ConfigFailed                  // Workspace config write/read failed
	SecurityViolation             // Path traversal or escaping workspace root
)

func (c Code) String() string {
	switch c {
	case AlreadyExists:
		return "AlreadyExists"
	case PathNotFound:
		return "PathNotFound"
	case NotGitRepo:
		return "NotGitRepo"
	case GitFailed:
		return "GitFailed"
	case ConfigFailed:
		return "ConfigFailed"
	case SecurityViolation:
		return "SecurityViolation"
	default:
		return "Unknown"
	}
}

// CreateError represents a structured workspace creation failure.
type CreateError struct {
	Code    Code   // Classified error type
	Message string // Human-readable error message
	Cause   error  // Underlying error, may be nil
}

// New returns a new CreateError. cause may be nil.
func New(code Code, msg string, cause error) *CreateError {
	return &CreateError{Code: code, Message: msg, Cause: cause}
}

// CreateMessage returns the user-facing error message.
func (e *CreateError) CreateMessage() string {
	return e.Message
}

func (e *CreateError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("workspaceerrors [%s]: %s: %s", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("workspaceerrors [%s]: %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause, enabling errors.Is/errors.As chain traversal.
func (e *CreateError) Unwrap() error {
	return e.Cause
}
