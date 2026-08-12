package backend

import (
	"context"
	"time"
)

// IssueBackend is the pluggable data access interface for issue tracking.
// It abstracts the underlying storage (fleet-db, Redis, etc.) behind
// a uniform set of query, mutation, and subscription operations.
//
// IssueBackend operates at the data access level, below the service layer but
// above raw RPC. It accepts backend-specific option/param structs and returns
// backend-specific wire types. The mapping layer converts wire types to entity
// types. Business logic (validation, permissions) stays in the service layer.
//
// All methods return error; implementations return *BackendError (see errors.go)
// which callers extract via errors.As to inspect the ErrorKind.
type IssueBackend interface {
	// --- Query operations ---

	// Get returns the full detail projection for a single issue, including
	// dependencies, dependents, and comments. Returns KindNotFound if the
	// issue does not exist.
	Get(ctx context.Context, id string) (*IssueDetailData, error)

	// List returns a slim projection of issues matching the given filters.
	// An empty ListOpts returns all issues up to the backend's default limit.
	List(ctx context.Context, opts ListOpts) ([]IssueData, error)

	// --- Mutation operations ---

	// Create creates a new issue and returns the slim projection of the
	// created issue. If CreateParams.ID is empty, the backend generates an ID.
	Create(ctx context.Context, params CreateParams) (*IssueData, error)

	// Update applies partial updates to an existing issue. Only non-nil
	// pointer fields in UpdateParams are applied. Returns KindNotFound if
	// the issue does not exist, KindConflict if Claim is true and the issue
	// is already claimed.
	Update(ctx context.Context, id string, params UpdateParams) error

	// ClaimIssue atomically claims an issue for the current agent. The
	// assignee identity comes from the configured agent name at the backend
	// level, not from the caller. lockTTL configures TTL-based lock expiry
	// for backends that support it. Pass 0 to use the backend's default TTL.
	// Returns KindConflict if the issue is
	// already claimed by another agent.
	ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error

	// ReleaseIssueLock releases only the distributed claim lock on the
	// issue without changing its status or assignee. Idempotent: returns
	// nil if no lock exists. Returns KindConflict if the lock is held by
	// a different actor. Backends without a lock concept return
	// KindNotImplemented.
	//
	// This is the supervisor-driven counterpart to claim release used on
	// agent exit when the agent has already transitioned the task status
	// to review/closed/etc. via Update/Close (which leave the operational
	// lock untouched).
	ReleaseIssueLock(ctx context.Context, id string, actor string) error

	// Close marks an issue as closed and returns the closed issue along with
	// any issues that became unblocked as a result. Returns KindNotFound if
	// the issue does not exist.
	Close(ctx context.Context, id string, params CloseParams) (*CloseResult, error)

	// Reopen transitions a closed issue back to open status. If
	// ReopenParams.Reason is non-empty, it is recorded as a comment on the
	// issue. Returns KindNotFound if the issue does not exist.
	Reopen(ctx context.Context, id string, params ReopenParams) error

	// Delete permanently removes one or more issues. Returns KindValidation
	// if DeleteParams.IDs is empty, KindNotFound if any ID does not exist
	// (unless Force is true).
	Delete(ctx context.Context, params DeleteParams) error

	// --- Dependency operations ---

	// AddDependency creates a dependency relationship between two issues.
	// Returns KindNotFound if either issue does not exist, KindConflict if
	// the dependency already exists.
	AddDependency(ctx context.Context, params DepAddParams) error

	// RemoveDependency removes a dependency relationship between two issues.
	// Returns KindNotFound if the dependency does not exist.
	RemoveDependency(ctx context.Context, params DepRemoveParams) error

	// --- Comment operations ---

	// ListComments returns all comments for an issue, ordered by creation time.
	// Returns KindNotFound if the issue does not exist.
	ListComments(ctx context.Context, id string) ([]CommentData, error)

	// AddComment adds a comment to an issue and returns the created comment.
	// Returns KindNotFound if the issue does not exist.
	AddComment(ctx context.Context, params CommentAddParams) (*CommentData, error)

	// --- Event operations ---

	// ListEvents returns the most recent events for an issue, up to limit.
	// If limit is 0, the backend uses its default. Returns KindNotFound if
	// the issue does not exist.
	ListEvents(ctx context.Context, id string, limit int) ([]EventData, error)

	// --- Metadata ---

	// BackendName returns a string identifying this backend implementation
	// (e.g., "fleet-db"). The value is immutable after construction.
	BackendName() string
}

// DeferredIssueBackend is an optional extension for backends that expose the
// canonical deferred view: explicit deferred status or a future defer_until.
type DeferredIssueBackend interface {
	Deferred(ctx context.Context, opts DeferredOpts) ([]IssueData, error)
}

// ClaimReleaser is an optional interface implemented by backends that maintain
// an explicit claim lock distinct from issue status (e.g., the fleet-db
// backend). Callers type-assert to release a completed agent's claim using the
// agent's stable actor identity.
//
// Implementations must be idempotent: return nil if the actor no longer owns
// the issue, or if the lock is already released, expired, or never held. The
// actor must be supplied by the caller's authenticated/session identity; it must
// not be derived from mutable issue state, or a stale completion could release a
// newer agent's lock.
//
// If the issue is still in_progress and assigned to actor, implementations
// should transition it back to open/unassigned so the task is claimable again.
// If the issue already moved to review/closed/etc., implementations should
// release only the operational lock. Backends that don't model an explicit claim
// lock should simply not implement this interface; callers detect support via
// type assertion.
type ClaimReleaser interface {
	ReleaseClaim(ctx context.Context, id, actor string) error
}
