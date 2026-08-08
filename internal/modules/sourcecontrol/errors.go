package sourcecontrol

import "errors"

var (
	// ErrInvalid means the materialization scope, repository reference, remote,
	// or checkout path is malformed.
	ErrInvalid = errors.New("source control: invalid request")
	// ErrUnavailable means a required credential broker, checkout inspector,
	// or admission registry was not composed.
	ErrUnavailable = errors.New("source control: unavailable")
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

// RefChangedError reports that a fetched immutable subject no longer matches
// the commit observed through the provider API.
type RefChangedError struct {
	ExpectedCommit string
	FetchedCommit  string
}

func (e *RefChangedError) Error() string {
	return "source control: fetched ref changed"
}
