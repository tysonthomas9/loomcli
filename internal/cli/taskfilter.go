package cli

// Shared issue categorization predicates.
// Used by automode.go (agent task selection) and monitor.go (dashboard counts).
// Frontend equivalent: frontend/src/utils/issueCategory.ts

// NeedsRevisionLabel is added when a plan review is rejected.
// Issues with this label are treated as needing re-planning even if they have a design.
const NeedsRevisionLabel = "needs-revision"

// --- Level 1: Simple predicates (no context needed) ---

// IsEpic returns true if the issue is an epic. Agents never work on epics directly.
func IsEpic(issue BdIssue) bool {
	return issue.IssueType == "epic"
}

// IsOpen returns true if the issue has status "open".
func IsOpen(issue BdIssue) bool {
	return issue.Status == "open"
}

// HasDesign returns true if the issue has a non-empty design field.
func HasDesign(issue BdIssue) bool {
	return issue.Design != ""
}

// HasNeedsRevision returns true if the issue has the needs-revision label.
func HasNeedsRevision(issue BdIssue) bool {
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
func NeedsPlan(issue BdIssue) bool {
	return !HasDesign(issue) || HasNeedsRevision(issue)
}

// ReadyToImplement returns true if the issue has an approved design
// ready for implementation (has design AND no needs-revision label).
// SYNC: Must match issueCategory.ts getOpenStatus() returning 'ready'
func ReadyToImplement(issue BdIssue) bool {
	return HasDesign(issue) && !HasNeedsRevision(issue)
}

// IsWorkableTask returns true if the issue can be picked up by an agent:
// status is open and not an epic.
func IsWorkableTask(issue BdIssue) bool {
	return IsOpen(issue) && !IsEpic(issue)
}

// --- Level 3: Agent predicates (with context from full issue list) ---

// HasOpenBlockersInReadyList returns true if any dependency is a blocking
// relationship where the blocker is still unresolved.
//
// IMPORTANT: allIssues must come from "bd ready" which only returns open/ready
// issues. A blocker NOT in the list is assumed resolved (closed/tombstone).
// Only blockers present and still open block work.
func HasOpenBlockersInReadyList(deps []Dependency, allIssues []BdIssue) bool {
	openIDs := make(map[string]bool, len(allIssues))
	for _, issue := range allIssues {
		openIDs[issue.ID] = true
	}
	for _, dep := range deps {
		if dep.Type == "blocks" && openIDs[dep.DependsOnID] {
			return true
		}
	}
	return false
}

// IsAvailableForPlanning returns true if the issue should be picked up by a
// planning agent: workable, no open blockers, and needs a plan.
func IsAvailableForPlanning(issue BdIssue, allIssues []BdIssue) bool {
	return IsWorkableTask(issue) &&
		!HasOpenBlockersInReadyList(issue.Dependencies, allIssues) &&
		NeedsPlan(issue)
}

// IsAvailableForImplementation returns true if the issue should be picked up by
// an implementation agent: workable, no open blockers, and has an approved design.
func IsAvailableForImplementation(issue BdIssue, allIssues []BdIssue) bool {
	return IsWorkableTask(issue) &&
		!HasOpenBlockersInReadyList(issue.Dependencies, allIssues) &&
		ReadyToImplement(issue)
}

// IsAvailableForAny returns true if the issue can be picked up by any agent
// regardless of design status: workable and no open blockers.
func IsAvailableForAny(issue BdIssue, allIssues []BdIssue) bool {
	return IsWorkableTask(issue) &&
		!HasOpenBlockersInReadyList(issue.Dependencies, allIssues)
}
