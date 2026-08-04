package domain

import "github.com/tysonthomas9/loomcli/internal/modules/workspace"

// WorkspaceState is the lifecycle state of a workspace from creation
// through readiness.
type WorkspaceState = workspace.State

const (
	WorkspaceStateCreating     = workspace.StateCreating
	WorkspaceStateCloning      = workspace.StateCloning
	WorkspaceStateInitializing = workspace.StateInitializing
	WorkspaceStateReady        = workspace.StateReady
	WorkspaceStateError        = workspace.StateError
)

// Workspace is the top-level container for a multi-repo project.
//
// Key is the immutable fleet-db workspace identifier (uppercase
// alphanumeric+hyphens, 1–32 chars). Name is the human-readable display
// label and may change over a workspace's lifetime.
//
// Local-machine-specific data (filesystem path of checkouts, etc.) does
// NOT live here — see the bootstrap state cache. Workspace is shared
// across users in cloud mode and must stay machine-agnostic.
type Workspace = workspace.Workspace
