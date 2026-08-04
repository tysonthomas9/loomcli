package types

import "strings"

// Status represents the current state of an issue
type Status string

// Issue status constants
const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusDeferred   Status = "deferred" // Deliberately put on ice for later
	StatusReview     Status = "review"   // Needs human attention (plan approval, code review)
	StatusClosed     Status = "closed"
	StatusTombstone  Status = "tombstone" // Soft-deleted issue
	StatusPinned     Status = "pinned"    // Persistent context item that stays open indefinitely
	StatusHooked     Status = "hooked"    // Work attached to an agent's hook (GUPP)
)

// IsValid checks if the status value is valid (built-in statuses only)
func (s Status) IsValid() bool {
	switch s {
	case StatusOpen, StatusInProgress, StatusBlocked, StatusDeferred, StatusReview, StatusClosed, StatusTombstone, StatusPinned, StatusHooked:
		return true
	}
	return false
}

// IsValidWithCustom checks if the status is valid, including custom statuses.
// Custom statuses are user-defined via runtime configuration.
func (s Status) IsValidWithCustom(customStatuses []string) bool {
	// First check built-in statuses
	if s.IsValid() {
		return true
	}
	// Then check custom statuses
	for _, custom := range customStatuses {
		if string(s) == custom {
			return true
		}
	}
	return false
}

// IssueType categorizes the kind of work
type IssueType string

// Core work type constants.
// All other types require configuration via types.custom in config.yaml.
const (
	TypeBug     IssueType = "bug"
	TypeFeature IssueType = "feature"
	TypeTask    IssueType = "task"
	TypeEpic    IssueType = "epic"
	TypeChore   IssueType = "chore"
)

// Note: Gas Town types (molecule, gate, convoy, merge-request, slot, agent, role, rig, event, message)
// are custom types with no built-in constants.
// Use string literals like types.IssueType("molecule") if needed, and configure types.custom.

// IsValid checks if the issue type is a core work type.
// Only core work types (bug, feature, task, epic, chore) are built-in.
// Other types (molecule, gate, convoy, etc.) require types.custom configuration.
func (t IssueType) IsValid() bool {
	switch t {
	case TypeBug, TypeFeature, TypeTask, TypeEpic, TypeChore:
		return true
	}
	return false
}

// IsBuiltIn returns true if the type is a built-in type (same as IsValid).
// Used during multi-repo hydration to determine trust:
// - Built-in types: validate (catch typos)
// - Custom types (!IsBuiltIn): trust from source repo
func (t IssueType) IsBuiltIn() bool {
	return t.IsValid()
}

// IsValidWithCustom checks if the issue type is valid, including custom types.
// Custom types are user-defined via runtime configuration.
func (t IssueType) IsValidWithCustom(customTypes []string) bool {
	// First check built-in types
	if t.IsValid() {
		return true
	}
	// Then check custom types
	for _, custom := range customTypes {
		if string(t) == custom {
			return true
		}
	}
	return false
}

// Normalize maps issue type aliases to their canonical form.
// For example, "enhancement" -> "feature".
// Case-insensitive to match util.NormalizeIssueType behavior.
func (t IssueType) Normalize() IssueType {
	switch strings.ToLower(string(t)) {
	case "enhancement", "feat":
		return TypeFeature
	default:
		return t
	}
}

// RequiredSection describes a recommended section for an issue type.
// Used for template validation.
type RequiredSection struct {
	Heading string // Markdown heading, e.g., "## Steps to Reproduce"
	Hint    string // Guidance for what to include
}

// RequiredSections returns the recommended sections for this issue type.
// Returns nil for types with no specific section requirements.
func (t IssueType) RequiredSections() []RequiredSection {
	switch t {
	case TypeBug:
		return []RequiredSection{
			{Heading: "## Steps to Reproduce", Hint: "Describe how to reproduce the bug"},
			{Heading: "## Acceptance Criteria", Hint: "Define criteria to verify the fix"},
		}
	case TypeTask, TypeFeature:
		return []RequiredSection{
			{Heading: "## Acceptance Criteria", Hint: "Define criteria to verify completion"},
		}
	case TypeEpic:
		return []RequiredSection{
			{Heading: "## Success Criteria", Hint: "Define high-level success criteria"},
		}
	default:
		// Chore and custom types have no required sections
		return nil
	}
}

// AgentState represents the self-reported state of an agent
type AgentState string

// Agent state constants
const (
	StateIdle     AgentState = "idle"     // Agent is waiting for work
	StateSpawning AgentState = "spawning" // Agent is starting up
	StateRunning  AgentState = "running"  // Agent is executing (general)
	StateWorking  AgentState = "working"  // Agent is actively working on a task
	StateStuck    AgentState = "stuck"    // Agent is blocked and needs help
	StateDone     AgentState = "done"     // Agent completed its current work
	StateStopped  AgentState = "stopped"  // Agent has cleanly shut down
	StateDead     AgentState = "dead"     // Agent died without clean shutdown (timeout detection)
)

// IsValid checks if the agent state value is valid
func (s AgentState) IsValid() bool {
	switch s {
	case StateIdle, StateSpawning, StateRunning, StateWorking, StateStuck, StateDone, StateStopped, StateDead, "":
		return true // empty is valid for non-agent records
	}
	return false
}

// MolType categorizes the molecule type for swarm coordination
type MolType string

// MolType constants
const (
	MolTypeSwarm  MolType = "swarm"  // Swarm molecule: coordinated multi-polecat work
	MolTypePatrol MolType = "patrol" // Patrol molecule: recurring operational work (Witness, Deacon, etc.)
	MolTypeWork   MolType = "work"   // Work molecule: regular polecat work (default)
)

// IsValid checks if the mol type value is valid
func (m MolType) IsValid() bool {
	switch m {
	case MolTypeSwarm, MolTypePatrol, MolTypeWork, "":
		return true // empty is valid (defaults to work)
	}
	return false
}

// WorkType categorizes how work assignment operates for a bead (Decision 006)
type WorkType string

// WorkType constants
const (
	WorkTypeMutex           WorkType = "mutex"            // One worker, exclusive assignment (default)
	WorkTypeOpenCompetition WorkType = "open_competition" // Many submit, buyer picks
)

// IsValid checks if the work type value is valid
func (w WorkType) IsValid() bool {
	switch w {
	case WorkTypeMutex, WorkTypeOpenCompetition, "":
		return true // empty is valid (defaults to mutex)
	}
	return false
}

// SortPolicy determines how ready work is ordered
type SortPolicy string

// Sort policy constants
const (
	// SortPolicyHybrid prioritizes recent issues by priority, older by age
	// Recent = created within 48 hours
	// Default sort policy.
	SortPolicyHybrid SortPolicy = "hybrid"

	// SortPolicyPriority always sorts by priority first, then creation date
	// Use for autonomous execution, CI/CD, priority-driven workflows
	SortPolicyPriority SortPolicy = "priority"

	// SortPolicyOldest always sorts by creation date (oldest first)
	// Use for backlog clearing, preventing issue starvation
	SortPolicyOldest SortPolicy = "oldest"
)

// IsValid checks if the sort policy value is valid
func (s SortPolicy) IsValid() bool {
	switch s {
	case SortPolicyHybrid, SortPolicyPriority, SortPolicyOldest, "":
		return true
	}
	return false
}
