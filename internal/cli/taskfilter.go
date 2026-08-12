package cli

// Shared issue categorization predicates.
// Used by automode.go (agent task selection) and monitor.go (dashboard counts).
// Frontend equivalent: frontend/src/utils/issueCategory.ts

import "github.com/tysonthomas9/loomcli/internal/modules/workitems"

// NeedsRevisionLabel is added when a plan review is rejected.
// Issues with this label are treated as needing re-planning even if they have a design.
const NeedsRevisionLabel = "needs-revision"

// --- Level 1: Simple predicates (no context needed) ---

// IsEpic returns true if the issue is an epic. Agents never work on epics directly.
func IsEpic(issue workitems.IssueSummary) bool {
	return issue.IssueType == "epic"
}

// IsNonWorkType returns true if the issue type is a non-work internal type that
// agents should never pick up. These are workflow/infrastructure records, not tasks.
// SYNC: Must stay aligned with ready.go SQL exclusion list (sqlite/ready.go:48)
// and memory storage (memory/memory.go:1174).
func IsNonWorkType(issue workitems.IssueSummary) bool {
	switch issue.IssueType {
	case "merge-request", "gate", "molecule", "message", "agent", "role", "rig":
		return true
	}
	return false
}

// IsOpen returns true if the issue has status "open".
func IsOpen(issue workitems.IssueSummary) bool {
	return issue.Status == "open"
}

// HasDesign supports both legacy inline bodies and artifact-backed collection
// projections, where the large body is deliberately omitted.
func HasDesign(issue workitems.IssueSummary) bool {
	return issue.HasDesign || issue.Design != "" || issue.DesignArtifactID != ""
}

// HasNeedsRevision returns true if the issue has the needs-revision label.
func HasNeedsRevision(issue workitems.IssueSummary) bool {
	for _, label := range issue.Labels {
		if label == NeedsRevisionLabel {
			return true
		}
	}
	return false
}

// --- Level 2: Workflow predicates (combining Level 1) ---

// NeedsPlan returns true if the issue needs planning:
// either no design, or has the needs-revision label (plan was rejected).
// SYNC: Must match issueCategory.ts getOpenStatus()
func NeedsPlan(issue workitems.IssueSummary) bool {
	return !HasDesign(issue) || HasNeedsRevision(issue)
}

// ReadyToImplement returns true if the issue has an approved design
// ready for implementation (has design AND no needs-revision label).
// SYNC: Must match issueCategory.ts getOpenStatus() returning 'ready'
func ReadyToImplement(issue workitems.IssueSummary) bool {
	return HasDesign(issue) && !HasNeedsRevision(issue)
}

// IsWorkableTask returns true if the issue can be picked up by an agent:
// status is open, not an epic, and not a non-work type.
func IsWorkableTask(issue workitems.IssueSummary) bool {
	return IsOpen(issue) && !IsEpic(issue) && !IsNonWorkType(issue)
}

// --- Level 3: Agent predicates ---

// IsAvailableForPlanning returns true if the issue should be picked up by a
// planning agent: workable and needs a plan. Ready issues are pre-filtered
// by the backend to exclude blocked issues.
func IsAvailableForPlanning(issue workitems.IssueSummary) bool {
	return IsWorkableTask(issue) && NeedsPlan(issue)
}

// IsAvailableForImplementation returns true if the issue should be picked up by
// an implementation agent: workable and has an approved design. Ready issues are
// pre-filtered by the backend to exclude blocked issues.
func IsAvailableForImplementation(issue workitems.IssueSummary) bool {
	return IsWorkableTask(issue) && ReadyToImplement(issue)
}

// IsAvailableForAny returns true if the issue can be picked up by any agent
// regardless of design status: workable. Ready issues are pre-filtered by
// the backend to exclude blocked issues.
func IsAvailableForAny(issue workitems.IssueSummary) bool {
	return IsWorkableTask(issue)
}
