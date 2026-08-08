package types

import "github.com/tysonthomas9/loomcli/internal/modules/workitems"

// Status represents the current state of an issue
type Status string

// Issue status constants
const (
	StatusOpen       Status = Status(workitems.StatusOpen)
	StatusInProgress Status = Status(workitems.StatusInProgress)
	StatusBlocked    Status = Status(workitems.StatusBlocked)
	StatusDeferred   Status = Status(workitems.StatusDeferred) // Deliberately put on ice for later
	StatusReview     Status = Status(workitems.StatusReview)   // Needs human attention (plan approval, code review)
	StatusClosed     Status = Status(workitems.StatusClosed)
	StatusTombstone  Status = Status(workitems.StatusTombstone) // Soft-deleted issue
	StatusPinned     Status = Status(workitems.StatusPinned)    // Persistent context item that stays open indefinitely
	StatusHooked     Status = Status(workitems.StatusHooked)    // Work attached to an agent's hook (GUPP)
)

// IsValid checks if the status value is valid (built-in statuses only)
func (s Status) IsValid() bool {
	return workitems.Status(s).IsBuiltIn()
}

// IsValidWithCustom checks if the status is valid, including custom statuses.
// Custom statuses are user-defined via runtime configuration.
func (s Status) IsValidWithCustom(customStatuses []string) bool {
	return workitems.Status(s).IsValidWithCustom(customStatuses)
}

// IssueType categorizes the kind of work
type IssueType string

// Core work type constants.
// All other types require configuration via types.custom in config.yaml.
const (
	TypeBug     IssueType = IssueType(workitems.TypeBug)
	TypeFeature IssueType = IssueType(workitems.TypeFeature)
	TypeTask    IssueType = IssueType(workitems.TypeTask)
	TypeEpic    IssueType = IssueType(workitems.TypeEpic)
	TypeChore   IssueType = IssueType(workitems.TypeChore)
)

// Note: Gas Town types (molecule, gate, convoy, merge-request, slot, agent, role, rig, event, message)
// are custom types with no built-in constants.
// Use string literals like types.IssueType("molecule") if needed, and configure types.custom.

// IsValid checks if the issue type is a core work type.
// Only core work types (bug, feature, task, epic, chore) are built-in.
// Other types (molecule, gate, convoy, etc.) require types.custom configuration.
func (t IssueType) IsValid() bool {
	return workitems.IssueType(t).IsBuiltIn()
}

// IsBuiltIn returns true if the type is a built-in type (same as IsValid).
// Used during multi-repo hydration to determine trust:
// - Built-in types: validate (catch typos)
// - Custom types (!IsBuiltIn): trust from source repo
func (t IssueType) IsBuiltIn() bool {
	return workitems.IssueType(t).IsBuiltIn()
}

// IsValidWithCustom checks if the issue type is valid, including custom types.
// Custom types are user-defined via runtime configuration.
func (t IssueType) IsValidWithCustom(customTypes []string) bool {
	return workitems.IssueType(t).IsValidWithCustom(customTypes)
}

// Normalize maps issue type aliases to their canonical form.
// For example, "enhancement" -> "feature".
// Case-insensitive to match util.NormalizeIssueType behavior.
func (t IssueType) Normalize() IssueType {
	return IssueType(workitems.IssueType(t).Normalize())
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
	StateIdle     AgentState = AgentState(workitems.AgentStateIdle)     // Agent is waiting for work
	StateSpawning AgentState = AgentState(workitems.AgentStateSpawning) // Agent is starting up
	StateRunning  AgentState = AgentState(workitems.AgentStateRunning)  // Agent is executing (general)
	StateWorking  AgentState = AgentState(workitems.AgentStateWorking)  // Agent is actively working on a task
	StateStuck    AgentState = AgentState(workitems.AgentStateStuck)    // Agent is blocked and needs help
	StateDone     AgentState = AgentState(workitems.AgentStateDone)     // Agent completed its current work
	StateStopped  AgentState = AgentState(workitems.AgentStateStopped)  // Agent has cleanly shut down
	StateDead     AgentState = AgentState(workitems.AgentStateDead)     // Agent died without clean shutdown (timeout detection)
)

// IsValid checks if the agent state value is valid
func (s AgentState) IsValid() bool {
	return workitems.AgentState(s).IsValid()
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
