// Package stacklineage is the pure domain layer for Loom's stack-aware PR
// publisher: the types, deterministic branch naming, lineage ordering, and base
// selection. It performs no I/O and depends only on the standard library, so it
// is fully unit-testable and could later be backed by either a local file store
// or a fleet-db store without change.
//
// See docs/design/2026-06-18-stack-aware-pr-publisher.md.
package stacklineage

import (
	"errors"
	"time"
)

// StackID identifies a stack. Conventionally "<kind>:<value>", e.g.
// "epic:EPIC-1", "manual:parser-followups", "auto:loomcli/flaky-tests".
type StackID string

// CommitMode controls how a task's work becomes the commit on its output branch.
type CommitMode string

const (
	// CommitModeLoom (default): Loom synthesizes one commit from the task's tree
	// onto the predecessor (via git commit-tree).
	CommitModeLoom CommitMode = "loom_commit"
	// CommitModeAgent: preserve the agent's own base..HEAD commits.
	CommitModeAgent CommitMode = "agent_commit"
	// CommitModeSquash: agent commits preserved locally, squashed at publish.
	// Treated as agent_commit until a squash step lands.
	CommitModeSquash CommitMode = "squash_on_publish"
)

// NodeState is the lifecycle state of a unit in the stack.
//
//	pending    → published | empty
//	published  → conflicted | merged | closed
//	conflicted → published | closed
//	closed     → published   (re-add: new PR by default)
//	merged     is TERMINAL — never reopened or retargeted.
type NodeState string

const (
	NodeStatePending    NodeState = "pending"    // in store; no branch/PR yet
	NodeStatePublished  NodeState = "published"  // branch pushed + PR open
	NodeStateConflicted NodeState = "conflicted" // PR open but GitHub reports a conflict
	NodeStateEmpty      NodeState = "empty"      // base..HEAD empty → no PR
	NodeStateMerged     NodeState = "merged"     // PR merged on GitHub (terminal)
	NodeStateClosed     NodeState = "closed"     // PR closed by Loom (unit dropped)
)

// Terminal reports whether the node is in a state the reconciler must never
// transition out of automatically.
func (s NodeState) Terminal() bool { return s == NodeStateMerged }

// Stack is the top-level record for a group of lineage-linked tasks in one repo.
type Stack struct {
	ID                StackID    `json:"id"`
	WorkspaceKey      string     `json:"workspaceKey"`
	RepoName          string     `json:"repoName"`
	RootBase          string     `json:"rootBase"` // branch name (not a SHA) the root unit builds on
	DefaultCommitMode CommitMode `json:"defaultCommitMode,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// Node is one task's slot in a stack. Lineage is a parent pointer (BaseTaskID),
// git-town style: BaseTaskID == "" marks the root unit. OutputBranch is assigned
// once at registration time (stable forever, collision-resolved) — not derived
// on the fly — so a re-run never changes a task's branch.
type Node struct {
	StackID         StackID    `json:"stackId"`
	TaskID          string     `json:"taskId"`
	BaseTaskID      string     `json:"baseTaskId,omitempty"`
	OutputBranch    string     `json:"outputBranch"`
	CommitMode      CommitMode `json:"commitMode,omitempty"`
	State           NodeState  `json:"state"`
	PRNumber        int        `json:"prNumber,omitempty"`
	PRURL           string     `json:"prUrl,omitempty"`
	OutputSHA       string     `json:"outputSha,omitempty"`
	LastPublishedAt *time.Time `json:"lastPublishedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// Domain sentinel errors. Callers compare with errors.Is.
var (
	ErrNoRoot             = errors.New("stacklineage: stack has no root unit")
	ErrCycle              = errors.New("stacklineage: lineage cycle detected")
	ErrMissingPredecessor = errors.New("stacklineage: base task not found in stack")
	ErrBranching          = errors.New("stacklineage: a unit has multiple successors (chains must stay linear)")
	ErrNoOutputBranch     = errors.New("stacklineage: predecessor has no assigned output branch")
)
