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
