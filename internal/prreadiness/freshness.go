package prreadiness

import "time"

// Freshness classes a snapshot's age.
type Freshness string

const (
	FreshnessFresh   Freshness = "fresh"
	FreshnessAging   Freshness = "aging"
	FreshnessStale   Freshness = "stale"
	FreshnessUnknown Freshness = "unknown"
)

// Freshness thresholds (STACKED-PRS-18 R6 proposals; server clock only).
const (
	// FreshFor is how long a snapshot may enable Ready and a preview.
	FreshFor = 60 * time.Second
	// StaleAfter is the age after which a last-known verdict is history.
	StaleAfter = 10 * time.Minute
)

// Invalidation reasons: evidence that voids a snapshot before it ages out.
const (
	InvalidatedHeadMoved   = "head_moved"
	InvalidatedBaseChanged = "base_changed"
)

// ClassifyFreshness classes observedAt against now. An invalidated snapshot
// is stale regardless of age; a zero observedAt was never observed.
func ClassifyFreshness(observedAt, now time.Time, invalidated bool) Freshness {
	if observedAt.IsZero() {
		return FreshnessUnknown
	}
	if invalidated {
		return FreshnessStale
	}
	age := now.Sub(observedAt)
	switch {
	case age <= FreshFor:
		return FreshnessFresh
	case age <= StaleAfter:
		return FreshnessAging
	default:
		return FreshnessStale
	}
}

// ReadError is the last failed read for a PR, kept beside the last-known
// snapshot so the UI can say why the evidence is old.
type ReadError struct {
	Code              ErrorCode `json:"code"`
	RetryAfterSeconds int       `json:"retry_after_s,omitempty"`
	At                time.Time `json:"at"`
}

// View is what a readiness surface renders for one PR: the last-known
// snapshot (history once it is not fresh) plus the verdict that may be shown
// as current right now.
type View struct {
	PRKey string `json:"pr_key"`
	// Snapshot is the last successful observation, or nil if none.
	Snapshot   *Snapshot `json:"snapshot,omitempty"`
	Freshness  Freshness `json:"freshness"`
	AgeSeconds int       `json:"age_seconds"`
	// Invalidated names evidence that voided Snapshot (head_moved,
	// base_changed); empty when only age applies.
	Invalidated string `json:"invalidated,omitempty"`
	// LastError is the most recent failed read, when newer than Snapshot.
	LastError *ReadError `json:"last_error,omitempty"`
	// CurrentVerdict is the verdict a surface may present as current. It is
	// never ready unless Snapshot is fresh.
	CurrentVerdict Verdict  `json:"current_verdict"`
	CurrentReasons []string `json:"current_reasons"`
}

// NewView classes snap at now. invalidated is an invalidation reason ("" when
// none); lastErr is the most recent failed read (nil when none or older than
// snap).
//
// Current verdict rules: fresh → the snapshot's verdict; merged and closed
// stay sticky at any age; aging keeps non-ready verdicts but demotes ready to
// unknown(readiness_aging); stale or invalidated → unknown(stale); never
// observed → unknown(not_observed or the read error).
func NewView(prKey string, snap *Snapshot, now time.Time, invalidated string, lastErr *ReadError) View {
	v := View{PRKey: prKey, Snapshot: snap, Invalidated: invalidated, LastError: lastErr}
	if lastErr != nil && snap != nil && !lastErr.At.After(snap.ObservedAt) {
		v.LastError = nil
	}
	if snap == nil {
		v.Freshness = FreshnessUnknown
		v.CurrentVerdict = VerdictUnknown
		v.CurrentReasons = []string{ReasonNotObserved}
		if lastErr != nil {
			v.CurrentReasons = []string{ReasonRepoError + ":" + string(lastErr.Code)}
		}
		return v
	}
	v.AgeSeconds = int(max(now.Sub(snap.ObservedAt), 0) / time.Second)
	v.Freshness = ClassifyFreshness(snap.ObservedAt, now, invalidated != "")
	switch {
	case snap.Verdict == VerdictMerged || snap.Verdict == VerdictClosed:
		v.CurrentVerdict, v.CurrentReasons = snap.Verdict, append([]string{}, snap.Reasons...)
	case v.Freshness == FreshnessFresh:
		v.CurrentVerdict, v.CurrentReasons = snap.Verdict, append([]string{}, snap.Reasons...)
	case v.Freshness == FreshnessAging && snap.Verdict != VerdictReady:
		v.CurrentVerdict, v.CurrentReasons = snap.Verdict, append([]string{}, snap.Reasons...)
	case v.Freshness == FreshnessAging:
		v.CurrentVerdict, v.CurrentReasons = VerdictUnknown, []string{ReasonAging}
	default:
		v.CurrentVerdict, v.CurrentReasons = VerdictUnknown, []string{ReasonStale}
	}
	// A read that failed after the snapshot could not confirm it: a ready
	// verdict is no longer current, and other verdicts carry the error.
	if v.LastError != nil && v.CurrentVerdict != VerdictMerged && v.CurrentVerdict != VerdictClosed {
		errReason := ReasonRepoError + ":" + string(v.LastError.Code)
		if v.CurrentVerdict == VerdictReady {
			v.CurrentVerdict, v.CurrentReasons = VerdictUnknown, []string{errReason}
		} else {
			v.CurrentReasons = append(v.CurrentReasons, errReason)
		}
	}
	return v
}
