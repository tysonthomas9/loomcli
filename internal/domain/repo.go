package domain

import "github.com/tysonthomas9/loomcli/internal/modules/workspace"

// Repo is a source-code repository within a Workspace. Multiple repos per
// workspace is the normal case (multi-repo workflows).
//
// Name is the workspace-scoped identifier (unique within WorkspaceKey).
// RemoteURL is the canonical clone URL. Local checkout paths live in the
// per-machine state cache, not here — Repo is shared state.
//
// SourceRepoID is the stable identifier server-side code uses for
// filtering issues by repo (Issue.Repo matches SourceRepoID). When
// unset, callers should default it to Name.
type Repo = workspace.Repository
