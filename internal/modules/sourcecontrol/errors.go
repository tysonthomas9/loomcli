package sourcecontrol

import "errors"

var (
	// ErrInvalid means the materialization scope, repository reference, remote,
	// or checkout path is malformed.
	ErrInvalid = errors.New("source control: invalid request")
	// ErrNotFound means an opaque Workspace repository, agent, checkout, or
	// requested Source Control object does not exist.
	ErrNotFound = errors.New("source control: not found")
	// ErrForbidden means the caller cannot access a protected Source Control
	// path or operation.
	ErrForbidden = errors.New("source control: forbidden")
	// ErrUnavailable means a required credential broker, checkout inspector,
	// or admission registry was not composed.
	ErrUnavailable = errors.New("source control: unavailable")
	// ErrRemote means a configured Git remote or forge rejected or failed a
	// bounded Source Control operation.
	ErrRemote = errors.New("source control: remote operation failed")
	// ErrDiffBaseNotFound means no candidate base ref shares an ancestor with
	// the checkout head.
	ErrDiffBaseNotFound = errors.New("source control: diff base not found")
	// ErrCheckoutConflict means the target exists but is not a checkout of the
	// exact token-free remote requested by this operation.
	ErrCheckoutConflict = errors.New("source control: checkout conflict")
	// ErrInvalidBrokerReceipt means the credential broker returned coordinates
	// outside the exact materialization request.
	ErrInvalidBrokerReceipt = errors.New("source control: invalid broker receipt")
	// ErrInvalidMaterialization means the bounded Git operation returned
	// successfully without producing the exact requested checkout.
	ErrInvalidMaterialization = errors.New("source control: invalid materialization")
	// ErrIdempotencyConflict means a materialization ID was already bound to a
	// different immutable repository/checkout coordinate tuple.
	ErrIdempotencyConflict = errors.New("source control: idempotency conflict")
	// ErrRepositoryAdmissionNotFound means the durable machine-local half of
	// an admission is unavailable. Admission materialization never falls back
	// to an ordinary committed repository lookup.
	ErrRepositoryAdmissionNotFound = errors.New("source control: repository admission not found")
	// ErrNoRoot means a non-empty stack has no root task.
	ErrNoRoot = errors.New("source control: stack has no root task")
	// ErrCycle means stack parent pointers contain a cycle.
	ErrCycle = errors.New("source control: stack lineage cycle detected")
	// ErrMissingPredecessor means a task names a base task absent from its stack.
	ErrMissingPredecessor = errors.New("source control: stack predecessor not found")
	// ErrBranching means one task has multiple successors in a linear stack.
	ErrBranching = errors.New("source control: stack task has multiple successors")
	// ErrNoOutputBranch means a predecessor has no stable output branch.
	ErrNoOutputBranch = errors.New("source control: stack predecessor has no output branch")
)

// PublicErrorMessage returns a sanitized message suitable for delivery
// adapters. Dependency errors and local paths never cross this interface.
func PublicErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "invalid source control request"
	case errors.Is(err, ErrNotFound):
		return "source control resource not found"
	case errors.Is(err, ErrForbidden):
		return "source control access denied"
	case errors.Is(err, ErrPayloadTooLarge):
		return "source control payload too large"
	case errors.Is(err, ErrPreconditionFailed):
		return "source control version conflict"
	case errors.Is(err, ErrPreconditionRequired):
		return "source control version required"
	case errors.Is(err, ErrTimeout):
		return "source control operation timed out"
	case errors.Is(err, ErrCheckoutConflict), errors.Is(err, ErrIdempotencyConflict):
		return "source control conflict"
	case errors.Is(err, ErrUnavailable):
		return "source control unavailable"
	case errors.Is(err, ErrRemote):
		return "source control remote operation failed"
	default:
		return "source control operation failed"
	}
}

// RefChangedError reports that a fetched immutable subject no longer matches
// the commit observed through the provider API.
type RefChangedError struct {
	ExpectedCommit string
	FetchedCommit  string
}

func (e *RefChangedError) Error() string {
	return "source control: fetched ref changed"
}
