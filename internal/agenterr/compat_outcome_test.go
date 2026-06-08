package agenterr

import "github.com/olesho/harness-wrapper/pkg/wrapper"

// Transitional test scaffolding for the ErrorClass→Outcome migration: the
// historical bare names used throughout this package's tests, retyped as
// Outcome values (and the old type name aliased) so the existing assertions
// stay byte-identical. New tests should use OutcomeFromHarness /
// OutcomeFromDomain directly; this file is deleted with the final cleanup
// step of the refactor.
type ErrorClass = Outcome

var (
	RateLimited        = OutcomeFromHarness(wrapper.ErrRateLimited)
	AuthFailure        = OutcomeFromHarness(wrapper.ErrAuth)
	BillingError       = OutcomeFromHarness(wrapper.ErrBilling)
	ModelNotFound      = OutcomeFromHarness(wrapper.ErrModelNotFound)
	ContextOverflow    = OutcomeFromHarness(wrapper.ErrContextOverflow)
	Timeout            = OutcomeFromHarness(wrapper.ErrTimeout)
	Transient          = OutcomeFromHarness(wrapper.ErrTransient)
	Unknown            = OutcomeFromHarness(wrapper.ErrUnknown)
	NoWork             = OutcomeFromDomain(NoWorkOutcome)
	SpawnFailure       = OutcomeFromDomain(SpawnFailureOutcome)
	BackendUnavailable = OutcomeFromDomain(BackendUnavailableOutcome)
	LockConflict       = OutcomeFromDomain(LockConflictOutcome)
)
