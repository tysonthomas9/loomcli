package prreadiness

import (
	"strings"
	"time"
)

// maxCheckNames caps the failing/pending check names carried per summary.
const maxCheckNames = 10

// GitHubPR is the raw GraphQL readiness read for one PR, as the connector's
// github.pull_request.readiness.read action returns it (camelCase, GitHub
// enum strings untouched).
type GitHubPR struct {
	Number           int           `json:"number"`
	NodeID           string        `json:"nodeId"`
	State            string        `json:"state"`
	IsDraft          bool          `json:"isDraft"`
	Merged           bool          `json:"merged"`
	HeadRefName      string        `json:"headRefName"`
	HeadRefOid       string        `json:"headRefOid"`
	BaseRefName      string        `json:"baseRefName"`
	BaseRefOid       string        `json:"baseRefOid"`
	Mergeable        string        `json:"mergeable"`
	MergeStateStatus string        `json:"mergeStateStatus"`
	ReviewDecision   string        `json:"reviewDecision"`
	IsInMergeQueue   bool          `json:"isInMergeQueue"`
	MergeQueueState  string        `json:"mergeQueueState"`
	ChecksTruncated  bool          `json:"checksTruncated"`
	Checks           []GitHubCheck `json:"checks"`
}

// GitHubCheck is one head-commit check context (CheckRun or StatusContext).
type GitHubCheck struct {
	Name string `json:"name"`
	// Kind is "check_run" or "status_context".
	Kind string `json:"kind"`
	// Status is the CheckRun status (QUEUED, IN_PROGRESS, COMPLETED, ...).
	Status string `json:"status"`
	// Conclusion is the CheckRun conclusion, or the StatusContext state.
	Conclusion string `json:"conclusion"`
	IsRequired bool   `json:"isRequired"`
}

// FactsFromGitHub maps one GraphQL readiness read to typed facts. GitHub's
// UNKNOWN/null mergeability maps to computing; unrecognized enum values map
// to unknown rather than being guessed.
func FactsFromGitHub(pr GitHubPR) Facts {
	required, requiredCounts := checkFacts(pr, true)
	optional, optionalCounts := checkFacts(pr, false)
	return Facts{
		Lifecycle:      lifecycleFact(pr),
		Conflicts:      conflictsFact(pr.Mergeable),
		MergeState:     mergeStateFact(pr.MergeStateStatus),
		Review:         reviewFact(pr.ReviewDecision),
		RequiredChecks: required,
		RequiredCounts: requiredCounts,
		OptionalChecks: optional,
		OptionalCounts: optionalCounts,
		Queue:          queueFact(pr),
	}
}

// SnapshotFromGitHub builds the snapshot for one GraphQL read.
func SnapshotFromGitHub(prKey string, pr GitHubPR, observedAt time.Time) Snapshot {
	return NewSnapshot(prKey, pr.HeadRefOid, pr.HeadRefName, pr.BaseRefName, pr.BaseRefOid, observedAt, FactsFromGitHub(pr))
}

func lifecycleFact(pr GitHubPR) Fact {
	switch {
	case pr.Merged || strings.EqualFold(pr.State, "MERGED"):
		return Known(LifecycleMerged)
	case strings.EqualFold(pr.State, "CLOSED"):
		return Known(LifecycleClosed)
	case strings.EqualFold(pr.State, "OPEN") && pr.IsDraft:
		return Known(LifecycleDraft)
	case strings.EqualFold(pr.State, "OPEN"):
		return Known(LifecycleOpen)
	default:
		return unrecognized(pr.State)
	}
}

func conflictsFact(mergeable string) Fact {
	switch strings.ToUpper(mergeable) {
	case "MERGEABLE":
		return Known(ConflictsNone)
	case "CONFLICTING":
		return Known(ConflictsConflicting)
	case "UNKNOWN", "":
		return Computing()
	default:
		return unrecognized(mergeable)
	}
}

func mergeStateFact(status string) Fact {
	switch strings.ToUpper(status) {
	case "CLEAN", "HAS_HOOKS", "UNSTABLE", "BLOCKED", "BEHIND", "DIRTY", "DRAFT":
		return Known(strings.ToLower(status))
	case "UNKNOWN", "":
		return Computing()
	default:
		return unrecognized(status)
	}
}

func reviewFact(decision string) Fact {
	switch strings.ToUpper(decision) {
	case "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED":
		return Known(strings.ToLower(decision))
	case "":
		return Known(ReviewNotReported)
	default:
		return unrecognized(decision)
	}
}

func queueFact(pr GitHubPR) Fact {
	state := strings.ToUpper(pr.MergeQueueState)
	switch state {
	case "":
		if pr.IsInMergeQueue {
			return Known(QueueQueued)
		}
		return Known(QueueNotQueued)
	case "QUEUED", "AWAITING_CHECKS", "LOCKED", "MERGEABLE", "UNMERGEABLE":
		return Known(strings.ToLower(state))
	default:
		return unrecognized(pr.MergeQueueState)
	}
}

// checkFacts summarizes required (or optional) head-commit checks. Truncated
// contexts are never reported as known: an unseen context could be failing.
func checkFacts(pr GitHubPR, required bool) (Fact, CheckSummary) {
	var sum CheckSummary
	for _, c := range pr.Checks {
		if c.IsRequired != required {
			continue
		}
		sum.Total++
		switch checkOutcome(c) {
		case ChecksPassing:
			sum.Passed++
		case ChecksFailing:
			sum.Failed++
			if len(sum.FailingNames) < maxCheckNames {
				sum.FailingNames = append(sum.FailingNames, c.Name)
			}
		default:
			sum.Pending++
			if len(sum.PendingNames) < maxCheckNames {
				sum.PendingNames = append(sum.PendingNames, c.Name)
			}
		}
	}
	if pr.ChecksTruncated {
		return Errored(ErrChecksTruncated, 0), sum
	}
	switch {
	case sum.Failed > 0:
		return Known(ChecksFailing), sum
	case sum.Pending > 0:
		return Known(ChecksPending), sum
	case sum.Total > 0:
		return Known(ChecksPassing), sum
	case required:
		return Known(ChecksNoneRequired), sum
	default:
		return Known(ChecksNone), sum
	}
}

// checkOutcome folds a check context into passing/pending/failing. Success,
// neutral and skipped satisfy a requirement; EXPECTED and anything
// unfinished are pending; everything else fails.
func checkOutcome(c GitHubCheck) string {
	if c.Kind == "check_run" && !strings.EqualFold(c.Status, "COMPLETED") {
		return ChecksPending
	}
	switch strings.ToUpper(c.Conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return ChecksPassing
	case "PENDING", "EXPECTED", "":
		return ChecksPending
	default:
		return ChecksFailing
	}
}

func unrecognized(raw string) Fact {
	return Fact{Status: FactUnknown, Value: strings.ToLower(raw), Error: ErrUnrecognizedValue}
}
