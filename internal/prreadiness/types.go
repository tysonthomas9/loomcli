// Package prreadiness classifies GitHub pull request readiness from typed,
// timestamped evidence. It is pure: no I/O, no clock reads (callers pass now).
//
// A Snapshot pins every fact to the PR key, head SHA, base SHA and the server
// time the evidence was observed. Evaluate turns facts into a verdict;
// NewView classes the snapshot's age so a last-known verdict can be shown as
// history but never as currently Ready; BuildPreview computes the read-only
// ordered ready prefix. The default everywhere is fail closed: unknown,
// computing, stale, partial or timed-out evidence never yields Ready.
//
// The model and truth table come from STACKED-PRS-26 (decision STACKED-PRS-18).
package prreadiness

import "time"

// FactStatus says how much a Fact can be trusted.
type FactStatus string

const (
	// FactKnown means GitHub reported a definite value.
	FactKnown FactStatus = "known"
	// FactComputing means GitHub is still computing the value (mergeable
	// null/UNKNOWN, mergeStateStatus UNKNOWN). Never ready, never blocked.
	FactComputing FactStatus = "computing"
	// FactUnknown means the value is missing or unrecognized.
	FactUnknown FactStatus = "unknown"
	// FactError means the read failed; Error names why.
	FactError FactStatus = "error"
)

// ErrorCode names why a fact or repository read is not known.
type ErrorCode string

const (
	ErrRateLimited          ErrorCode = "rate_limited"
	ErrTimeout              ErrorCode = "timeout"
	ErrForbidden            ErrorCode = "forbidden"
	ErrNotFound             ErrorCode = "not_found"
	ErrRepoUnregistered     ErrorCode = "repo_unregistered"
	ErrConnectorUnavailable ErrorCode = "connector_unavailable"
	ErrUpstream             ErrorCode = "upstream_error"
	ErrChecksTruncated      ErrorCode = "checks_truncated"
	ErrUnrecognizedValue    ErrorCode = "unrecognized_value"
)

// Verdict is the readiness classification of one PR.
type Verdict string

const (
	VerdictReady   Verdict = "ready"
	VerdictBlocked Verdict = "blocked"
	VerdictWaiting Verdict = "waiting"
	VerdictQueued  Verdict = "queued"
	VerdictMerged  Verdict = "merged"
	VerdictClosed  Verdict = "closed"
	VerdictUnknown Verdict = "unknown"
)

// Stable reason codes (snake_case; UI copy and tests key off them).
const (
	ReasonDraft                 = "draft"
	ReasonConflicts             = "conflicts"
	ReasonRequiredCheckFailed   = "required_check_failed"
	ReasonRequiredChecksPending = "required_checks_pending"
	ReasonReview                = "review"
	ReasonBehindBase            = "behind_base"
	ReasonRuleUnsatisfied       = "rule_unsatisfied"
	ReasonGitHubComputing       = "github_computing"
	ReasonInMergeQueue          = "in_merge_queue"
	ReasonOptionalCheckFailing  = "optional_check_failing"
	ReasonNoReviewRequired      = "no_review_required"
	ReasonPredecessorRetarget   = "predecessor_retarget"
	ReasonBaseWillMove          = "base_will_move"
	ReasonOrderConflict         = "order_conflict"
	ReasonRepoError             = "repo_error"
	ReasonNotObserved           = "not_observed"
	ReasonStale                 = "stale"
	ReasonAging                 = "readiness_aging"
	ReasonAfterStop             = "after_stop"
)

// Fact values. Each fact documents its closed value set in Facts.
const (
	LifecycleOpen   = "open"
	LifecycleDraft  = "draft"
	LifecycleClosed = "closed"
	LifecycleMerged = "merged"

	ConflictsNone        = "none"
	ConflictsConflicting = "conflicting"

	MergeStateClean    = "clean"
	MergeStateHasHooks = "has_hooks"
	MergeStateUnstable = "unstable"
	MergeStateBlocked  = "blocked"
	MergeStateBehind   = "behind"
	MergeStateDirty    = "dirty"
	MergeStateDraft    = "draft"

	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewRequired         = "review_required"
	ReviewNotReported      = "not_reported"

	ChecksPassing      = "passing"
	ChecksPending      = "pending"
	ChecksFailing      = "failing"
	ChecksNoneRequired = "none_required"
	ChecksNone         = "none"

	QueueNotQueued      = "not_queued"
	QueueQueued         = "queued"
	QueueAwaitingChecks = "awaiting_checks"
	QueueLocked         = "locked"
	QueueMergeable      = "mergeable"
	QueueUnmergeable    = "unmergeable"
)

// Fact is one piece of readiness evidence with its own trust status.
type Fact struct {
	Status            FactStatus `json:"status"`
	Value             string     `json:"value,omitempty"`
	Error             ErrorCode  `json:"error,omitempty"`
	RetryAfterSeconds int        `json:"retry_after_s,omitempty"`
}

// Known builds a known fact.
func Known(value string) Fact { return Fact{Status: FactKnown, Value: value} }

// Computing builds a fact GitHub is still computing.
func Computing() Fact { return Fact{Status: FactComputing} }

// Errored builds a failed fact.
func Errored(code ErrorCode, retryAfter time.Duration) Fact {
	return Fact{Status: FactError, Error: code, RetryAfterSeconds: ceilSeconds(retryAfter)}
}

func (f Fact) known(value string) bool { return f.Status == FactKnown && f.Value == value }

// CheckSummary counts check contexts on the head commit. Names list the
// failing and pending contexts (capped) so the UI can say which check stops
// a PR without another read.
type CheckSummary struct {
	Passed       int      `json:"passed"`
	Pending      int      `json:"pending"`
	Failed       int      `json:"failed"`
	Total        int      `json:"total"`
	FailingNames []string `json:"failing_names,omitempty"`
	PendingNames []string `json:"pending_names,omitempty"`
}

// Facts is the typed evidence for one PR at one head SHA.
type Facts struct {
	// Lifecycle: open | draft | closed | merged.
	Lifecycle Fact `json:"lifecycle"`
	// Conflicts (GraphQL mergeable, conflicts only): none | conflicting.
	Conflicts Fact `json:"conflicts"`
	// MergeState (GraphQL mergeStateStatus; includes branch protection and
	// rulesets): clean | has_hooks | unstable | blocked | behind | dirty | draft.
	MergeState Fact `json:"merge_state"`
	// Review (reviewDecision): approved | changes_requested |
	// review_required | not_reported (null: the repo requires no review, or
	// GitHub did not say).
	Review Fact `json:"review"`
	// RequiredChecks: passing | pending | failing | none_required.
	RequiredChecks Fact         `json:"required_checks"`
	RequiredCounts CheckSummary `json:"required_check_counts"`
	// OptionalChecks: passing | pending | failing | none.
	OptionalChecks Fact         `json:"optional_checks"`
	OptionalCounts CheckSummary `json:"optional_check_counts"`
	// Queue: not_queued | queued | awaiting_checks | locked | mergeable | unmergeable.
	Queue Fact `json:"queue"`
}

// Snapshot is the readiness evidence for one PR, pinned to the observed head
// and base and to the server time the evidence arrived.
type Snapshot struct {
	PRKey       string    `json:"pr_key"`
	HeadSHA     string    `json:"head_sha"`
	HeadRef     string    `json:"head_ref"`
	BaseRef     string    `json:"base_ref"`
	BaseSHA     string    `json:"base_sha"`
	ObservedAt  time.Time `json:"observed_at"`
	Facts       Facts     `json:"facts"`
	Verdict     Verdict   `json:"verdict"`
	Reasons     []string  `json:"reasons"`
	Fingerprint string    `json:"fingerprint"`
}

func ceilSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}
