package prreadiness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// NewSnapshot evaluates facts and pins them to the PR identity, refs and
// observation time.
func NewSnapshot(prKey, headSHA, headRef, baseRef, baseSHA string, observedAt time.Time, facts Facts) Snapshot {
	verdict, reasons := Evaluate(facts)
	s := Snapshot{
		PRKey:      prKey,
		HeadSHA:    headSHA,
		HeadRef:    headRef,
		BaseRef:    baseRef,
		BaseSHA:    baseSHA,
		ObservedAt: observedAt.UTC(),
		Facts:      facts,
		Verdict:    verdict,
		Reasons:    reasons,
	}
	s.Fingerprint = Fingerprint(s)
	return s
}

// Evaluate maps facts to a verdict and ordered reasons (the first reason is
// the headline). Rules apply in order; the first verdict wins, and later
// blocking reasons are appended so the UI can explain every known blocker.
//
// Ready requires merge_state clean/has_hooks/unstable, no conflicts, required
// checks passing or none required, and not queued — all known. Approval alone
// never produces Ready, and no Loom policy floor is added beyond GitHub's
// rules (STACKED-PRS-18 R2): a PR with no review requirement is Ready with
// reason no_review_required.
func Evaluate(f Facts) (Verdict, []string) {
	for _, rule := range []func(Facts) (Verdict, []string){
		evaluateLifecycle, evaluateTrust, evaluateQueue, evaluateBlockers,
	} {
		if v, reasons := rule(f); v != "" {
			return v, reasons
		}
	}
	return evaluateReady(f)
}

// evaluateLifecycle is rule 1: merged, closed and draft PRs.
func evaluateLifecycle(f Facts) (Verdict, []string) {
	if f.Lifecycle.Status != FactKnown {
		return VerdictUnknown, []string{unknownReason("lifecycle", f.Lifecycle)}
	}
	switch f.Lifecycle.Value {
	case LifecycleMerged:
		return VerdictMerged, []string{}
	case LifecycleClosed:
		return VerdictClosed, []string{}
	case LifecycleDraft:
		return VerdictBlocked, []string{ReasonDraft}
	case LifecycleOpen:
	default:
		return VerdictUnknown, []string{unknownReason("lifecycle", f.Lifecycle)}
	}
	return "", nil
}

// evaluateTrust is rules 2-3: unknown or failed gating facts, then facts
// GitHub is still computing.
func evaluateTrust(f Facts) (Verdict, []string) {
	gating := []struct {
		name string
		fact Fact
	}{
		{"conflicts", f.Conflicts},
		{"merge_state", f.MergeState},
		{"required_checks", f.RequiredChecks},
		{"queue", f.Queue},
	}
	var unknown []string
	for _, g := range gating {
		if g.fact.Status == FactUnknown || g.fact.Status == FactError || g.fact.Status == "" {
			unknown = append(unknown, unknownReason(g.name, g.fact))
		}
	}
	if len(unknown) > 0 {
		return VerdictUnknown, unknown
	}

	// 3. GitHub still computing mergeability.
	if f.Conflicts.Status == FactComputing || f.MergeState.Status == FactComputing {
		return VerdictWaiting, []string{ReasonGitHubComputing}
	}
	if f.RequiredChecks.Status == FactComputing || f.Queue.Status == FactComputing {
		return VerdictWaiting, []string{ReasonGitHubComputing}
	}
	return "", nil
}

// evaluateQueue is rule 4: GitHub owns queued PRs.
func evaluateQueue(f Facts) (Verdict, []string) {
	if f.Queue.Value != QueueNotQueued {
		return VerdictQueued, []string{ReasonInMergeQueue}
	}
	return "", nil
}

// evaluateBlockers is rules 5-9: blockers and waits, collected in rule order.
func evaluateBlockers(f Facts) (Verdict, []string) {
	verdict := Verdict("")
	var reasons []string
	add := func(v Verdict, reason string) {
		if verdict == "" {
			verdict = v
		}
		reasons = append(reasons, reason)
	}
	if f.Conflicts.Value == ConflictsConflicting || f.MergeState.Value == MergeStateDirty {
		add(VerdictBlocked, ReasonConflicts)
	}
	switch f.RequiredChecks.Value {
	case ChecksFailing:
		add(VerdictBlocked, ReasonRequiredCheckFailed)
	case ChecksPending:
		add(VerdictWaiting, ReasonRequiredChecksPending)
	}
	if f.Review.known(ReviewChangesRequested) || f.Review.known(ReviewRequired) {
		add(VerdictBlocked, ReasonReview)
	}
	switch f.MergeState.Value {
	case MergeStateBehind:
		add(VerdictBlocked, ReasonBehindBase)
	case MergeStateDraft:
		add(VerdictBlocked, ReasonDraft)
	case MergeStateBlocked:
		if verdict == "" {
			add(VerdictBlocked, ReasonRuleUnsatisfied)
		}
	}
	return verdict, reasons
}

// evaluateReady is rules 10-12: the ready states.
func evaluateReady(f Facts) (Verdict, []string) {
	switch f.MergeState.Value {
	case MergeStateClean, MergeStateHasHooks, MergeStateUnstable:
		reasons := []string{}
		if f.MergeState.Value == MergeStateUnstable {
			// STACKED-PRS-18 R1: GitHub allows the merge; warn.
			reasons = append(reasons, ReasonOptionalCheckFailing)
		}
		if f.Review.known(ReviewNotReported) {
			reasons = append(reasons, ReasonNoReviewRequired)
		}
		return VerdictReady, reasons
	}
	return VerdictUnknown, []string{unknownReason("merge_state", Fact{Status: FactUnknown, Error: ErrUnrecognizedValue})}
}

// unknownReason renders "<fact>_unknown:<error|status>".
func unknownReason(name string, f Fact) string {
	detail := string(f.Error)
	if detail == "" {
		detail = string(f.Status)
	}
	if detail == "" {
		detail = string(FactUnknown)
	}
	return name + "_unknown:" + detail
}

// Fingerprint hashes everything a verdict depends on — identity, head, base
// and every fact value and status — but not observed_at, so re-reading
// unchanged evidence keeps the fingerprint stable.
func Fingerprint(s Snapshot) string {
	payload := struct {
		PRKey   string `json:"k"`
		HeadSHA string `json:"h"`
		HeadRef string `json:"hr"`
		BaseRef string `json:"br"`
		BaseSHA string `json:"b"`
		Facts   Facts  `json:"f"`
	}{s.PRKey, s.HeadSHA, s.HeadRef, s.BaseRef, s.BaseSHA, s.Facts}
	raw, _ := json.Marshal(payload) //nolint:errchkjson // plain structs always marshal
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
