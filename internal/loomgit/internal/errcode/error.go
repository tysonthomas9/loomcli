package errcode

import "fmt"

// Code is a stable Loom Git failure identifier.
type Code string

const (
	ReviewRequired        Code = "review_required"
	Conflict              Code = "conflict"
	Stale                 Code = "stale"
	CaptureIncomplete     Code = "capture_incomplete"
	UnsavedWork           Code = "unsaved_work"
	Protected             Code = "protected"
	RepoSelectionRequired Code = "repo_selection_required"
	LineageUnresolved     Code = "lineage_unresolved"
	TaskCopyCreateFailed  Code = "task_copy_create_failed"
	BaseRefUnresolvable   Code = "base_ref_unresolvable"
	StaleSubject          Code = "stale_subject"
	StackLocked           Code = "stack_locked"
	StackNotLinear        Code = "stack_not_linear"
	StackDrift            Code = "stack_drift"
	RefNamespaceConflict  Code = "ref_namespace_conflict"
	ProviderStackLimit    Code = "provider_stack_limit"
	Diverged              Code = "diverged"
	DependencyAbandoned   Code = "dependency_abandoned"
	MergeNotAuthorized    Code = "merge_not_authorized"
	MergeBlocked          Code = "merge_blocked"
	ModeMismatch          Code = "mode_mismatch"
	IntegrityMissing      Code = "integrity_missing"
	AttentionRequired     Code = "attention_required"
	ApplyPending          Code = "apply_pending"
	SwapHeld              Code = "swap_held"
	RestackConflict       Code = "restack_conflict"
	RevisionSuperseded    Code = "revision_superseded"
	HashMismatch          Code = "hash_mismatch"
	CaptureFailed         Code = "capture_failed"
	SecretPathRefused     Code = "secret_path_refused" //nolint:gosec // Stable error code, not a credential.
	WorkspaceUnsupported  Code = "workspace_unsupported"
)

var All = []Code{
	ReviewRequired, Conflict, Stale, CaptureIncomplete, UnsavedWork, Protected,
	RepoSelectionRequired, LineageUnresolved, TaskCopyCreateFailed, BaseRefUnresolvable,
	StaleSubject, StackLocked, StackNotLinear, StackDrift, RefNamespaceConflict, ProviderStackLimit,
	Diverged, DependencyAbandoned, MergeNotAuthorized, MergeBlocked, ModeMismatch,
	IntegrityMissing, AttentionRequired, ApplyPending, SwapHeld, RestackConflict,
	RevisionSuperseded, HashMismatch, CaptureFailed, SecretPathRefused, WorkspaceUnsupported,
}

// Error carries a stable code, a useful message, and an optional cause.
type Error struct {
	Kind    Code
	Message string
	Err     error
}

func (e *Error) Code() string { return string(e.Kind) }

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error { return e.Err }

// Is matches the stable code, regardless of message or underlying cause.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Kind == other.Kind
}

func New(code Code, message string, cause error) *Error {
	return &Error{Kind: code, Message: message, Err: cause}
}
