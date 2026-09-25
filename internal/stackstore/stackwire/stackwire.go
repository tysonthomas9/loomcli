// Package stackwire is the wire contract of fleet-db's stack lineage API
// (/api/v1/{workspace}/stacks): its JSON shapes, its error form, and the API
// interface the fleet-db HTTP client implements. It has no dependencies so
// stackstore can adapt the API without importing the fleet-db client — the
// client is reached only through the store.Store handle bootstrap opens.
package stackwire

import (
	"context"
	"time"
)

// API is fleet-db's stack lineage API.
type API interface {
	// Ensure creates the stack or updates its header.
	Ensure(ctx context.Context, ws, id string, in Ensure) (*Stack, error)
	// Get returns a live stack with its nodes in lineage order.
	Get(ctx context.Context, ws, id string) (*Stack, error)
	// List returns every live stack in the workspace, sorted by ID.
	List(ctx context.Context, ws string) ([]Stack, error)
	// Delete tombstones the stack and all its nodes.
	Delete(ctx context.Context, ws, id string) error
	// ListNodes returns the stack's nodes in lineage order.
	ListNodes(ctx context.Context, ws, id string) ([]Node, error)
	// AddNode registers taskID on baseTaskID ("" = root unit).
	AddNode(ctx context.Context, ws, id, taskID, baseTaskID, mode string) (*Node, error)
	// SetBase repoints taskID's predecessor ("" = root unit).
	SetBase(ctx context.Context, ws, id, taskID, baseTaskID string) (*Node, error)
	// MoveNode atomically splices taskID to sit immediately after
	// req.AfterTaskID. When req.ExpectedRevision is set, a stale stack
	// document revision returns 412 precondition_failed.
	MoveNode(ctx context.Context, ws, id, taskID string, req MoveRequest) (*MoveResult, error)
	// RemoveNode drops taskID, reparenting its successor onto its predecessor.
	RemoveNode(ctx context.Context, ws, id, taskID string) error
	// UpdateNode applies a publish-state patch to taskID.
	UpdateNode(ctx context.Context, ws, id, taskID string, patch NodePatch) (*Node, error)
}

// Provider is implemented by stores that can reach fleet-db's stack API.
type Provider interface {
	Stacks() API
}

// Stack mirrors fleet-db's models.Stack JSON shape.
type Stack struct {
	WorkspaceKey      string    `json:"workspace_key"`
	ID                string    `json:"id"`
	RepoName          string    `json:"repo_name"`
	RootBase          string    `json:"root_base"`
	DefaultCommitMode string    `json:"default_commit_mode,omitempty"`
	Revision          int64     `json:"revision"`
	Nodes             []Node    `json:"nodes"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Node mirrors fleet-db's models.StackNode JSON shape.
type Node struct {
	TaskID          string     `json:"task_id"`
	BaseTaskID      string     `json:"base_task_id,omitempty"`
	OutputBranch    string     `json:"output_branch"`
	CommitMode      string     `json:"commit_mode,omitempty"`
	State           string     `json:"state"`
	PRNumber        int        `json:"pr_number,omitempty"`
	PRURL           string     `json:"pr_url,omitempty"`
	OutputSHA       string     `json:"output_sha,omitempty"`
	LastPublishedAt *time.Time `json:"last_published_at,omitempty"`
	Revision        int64      `json:"revision"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Ensure is the body of PUT /stacks/{stack_id}.
type Ensure struct {
	RepoName          string `json:"repo_name"`
	RootBase          string `json:"root_base"`
	DefaultCommitMode string `json:"default_commit_mode,omitempty"`
}

// NodePatch is the body of PATCH /stacks/{stack_id}/nodes/{task_id}. Nil
// fields are left unchanged; ExpectedRevision makes the write a compare-and-set
// on the node's revision (412 on mismatch).
type NodePatch struct {
	State            *string    `json:"state,omitempty"`
	CommitMode       *string    `json:"commit_mode,omitempty"`
	PRNumber         *int       `json:"pr_number,omitempty"`
	PRURL            *string    `json:"pr_url,omitempty"`
	OutputSHA        *string    `json:"output_sha,omitempty"`
	LastPublishedAt  *time.Time `json:"last_published_at,omitempty"`
	ExpectedRevision *int64     `json:"expected_revision,omitempty"`
}

// MoveRequest is the body of POST /stacks/{stack_id}/nodes/{task_id}/move.
// AfterTaskID is the node that task_id should sit immediately after.
// ExpectedRevision, when set, fences the move to one stack document revision
// (412 when another writer already advanced it).
type MoveRequest struct {
	AfterTaskID      string `json:"after_task_id"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

// MoveResult is the response of an atomic MoveNode: the stack revision after
// the splice (unchanged on no-op), the moved node, and the ordered topology.
type MoveResult struct {
	Revision int64  `json:"revision"`
	Node     Node   `json:"node"`
	Nodes    []Node `json:"nodes"`
}

// APIError is a non-2xx stack API response. It keeps the status, the
// structured error code, and the message so callers can tell "stack not found"
// from "stack node not found" (both 404 not_found) and name the violated
// lineage rule of a 422. Err is the client's generic classification (wrapping
// a domain sentinel such as domain.ErrNotFound); Unwrap yields it so errors.Is
// keeps working.
type APIError struct {
	Status  int
	Code    string
	Message string
	Err     error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
