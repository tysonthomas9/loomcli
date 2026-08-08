package workitems

import (
	"errors"
	"fmt"
	"strings"
)

// Request limits are owned with Work Items validation so every transport and
// compatibility adapter enforces the same aggregate admission policy.
const (
	MaxTitleLength  = 500
	MaxLabels       = 50
	MaxDependencies = 100
)

// TitleValidationKind lets transports and compatibility adapters retain their
// public error vocabulary without reimplementing Work Items title policy.
type TitleValidationKind string

const (
	TitleRequired TitleValidationKind = "required"
	TitleTooLong  TitleValidationKind = "too_long"
)

type titleValidationError struct {
	kind   TitleValidationKind
	length int
}

func (validation *titleValidationError) Error() string {
	switch validation.kind {
	case TitleRequired:
		return fmt.Sprintf("title is required: %v", ErrInvalid)
	case TitleTooLong:
		return fmt.Sprintf("title must be %d characters or less (got %d): %v", MaxTitleLength, validation.length, ErrInvalid)
	default:
		return fmt.Sprintf("title is invalid: %v", ErrInvalid)
	}
}

func (*titleValidationError) Unwrap() error { return ErrInvalid }

// CanonicalTitle returns the value persisted after Work Items admission.
func CanonicalTitle(title string) string { return strings.TrimSpace(title) }

// ValidateTitle is the single Work Items title policy. Validation applies to
// the canonical value so admission and persistence cannot disagree about
// leading or trailing whitespace.
func ValidateTitle(title string) error {
	canonical := CanonicalTitle(title)
	if canonical == "" {
		return &titleValidationError{kind: TitleRequired}
	}
	if len(canonical) > MaxTitleLength {
		return &titleValidationError{kind: TitleTooLong, length: len(canonical)}
	}
	return nil
}

// TitleValidationKindOf classifies a ValidateTitle error for adapters that
// present field-specific validation responses.
func TitleValidationKindOf(err error) (TitleValidationKind, bool) {
	var validation *titleValidationError
	if !errors.As(err, &validation) {
		return "", false
	}
	return validation.kind, true
}

// Status is the Work Items-owned lifecycle classification shared by its ports
// and compatibility adapters.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusDeferred   Status = "deferred"
	StatusReview     Status = "review"
	StatusClosed     Status = "closed"
	StatusTombstone  Status = "tombstone"
	StatusPinned     Status = "pinned"
	StatusHooked     Status = "hooked"
)

// IsBuiltIn reports whether status is one of Loom's built-in lifecycle states.
// Empty values are intentionally not built in; compatibility adapters decide
// whether their boundary permits an omitted status before defaulting.
func (status Status) IsBuiltIn() bool {
	switch status {
	case StatusOpen, StatusInProgress, StatusBlocked, StatusDeferred,
		StatusReview, StatusClosed, StatusTombstone, StatusPinned, StatusHooked:
		return true
	default:
		return false
	}
}

// IsValidWithCustom reports whether status is built in or exactly matches an
// operator-configured custom status. Matching is deliberately case-sensitive.
func (status Status) IsValidWithCustom(custom []string) bool {
	if status.IsBuiltIn() {
		return true
	}
	for _, candidate := range custom {
		if string(status) == candidate {
			return true
		}
	}
	return false
}

// IsCreateStatus reports whether status is accepted when creating a work item.
// The empty value is accepted because the application service defaults it to
// open; other lifecycle states must be reached through an explicit transition.
func (status Status) IsCreateStatus() bool {
	return status == "" || status == StatusOpen || status == StatusDeferred
}

// IsUserFacing reports whether callers may explicitly transition a work item
// into status. Internal persistence states remain unavailable through public
// patch requests.
func (status Status) IsUserFacing() bool {
	switch status {
	case StatusOpen, StatusInProgress, StatusBlocked, StatusDeferred, StatusReview, StatusClosed:
		return true
	default:
		return false
	}
}

// IssueType is the Work Items-owned built-in work classification shared by its
// ports and compatibility adapters.
type IssueType string

const (
	TypeBug     IssueType = "bug"
	TypeFeature IssueType = "feature"
	TypeTask    IssueType = "task"
	TypeEpic    IssueType = "epic"
	TypeChore   IssueType = "chore"
)

// IsBuiltIn reports whether issueType is one of Loom's built-in work types.
// Other values require explicit custom-type configuration.
func (issueType IssueType) IsBuiltIn() bool {
	switch issueType {
	case TypeBug, TypeFeature, TypeTask, TypeEpic, TypeChore:
		return true
	default:
		return false
	}
}

// IsValidWithCustom reports whether issueType is built in or exactly matches
// an operator-configured custom type. Matching is deliberately case-sensitive.
func (issueType IssueType) IsValidWithCustom(custom []string) bool {
	if issueType.IsBuiltIn() {
		return true
	}
	for _, candidate := range custom {
		if string(issueType) == candidate {
			return true
		}
	}
	return false
}

// Normalize maps accepted aliases to their canonical work-item type.
func (issueType IssueType) Normalize() IssueType {
	switch strings.ToLower(string(issueType)) {
	case "enhancement", "feat":
		return TypeFeature
	default:
		return issueType
	}
}

// AgentState is the Work Items-owned validation policy for the legacy
// agent-state field exposed on issue patch/import contracts.
type AgentState string

const (
	AgentStateIdle     AgentState = "idle"
	AgentStateSpawning AgentState = "spawning"
	AgentStateRunning  AgentState = "running"
	AgentStateWorking  AgentState = "working"
	AgentStateStuck    AgentState = "stuck"
	AgentStateDone     AgentState = "done"
	AgentStateStopped  AgentState = "stopped"
	AgentStateDead     AgentState = "dead"
)

// IsValid permits an omitted state for non-agent work items.
func (state AgentState) IsValid() bool {
	switch state {
	case "", AgentStateIdle, AgentStateSpawning, AgentStateRunning,
		AgentStateWorking, AgentStateStuck, AgentStateDone, AgentStateStopped, AgentStateDead:
		return true
	default:
		return false
	}
}
