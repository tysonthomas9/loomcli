package loomagent

import (
	"context"
	"errors"
)

// Workspace is the port loomagent uses for an agent's working copy (Spec R32,
// design v2 §8.1.7). The host composition root supplies the implementation;
// loomagent never imports one and never runs git or gh itself.
type Workspace interface {
	// Ensure makes the working copy for s, or reuses it when s owns it.
	Ensure(ctx context.Context, s WorkspaceSpec) (WorkingCopy, error)
	// CheckBase reports whether ref resolves in repo as Ensure would resolve
	// a new working copy's BaseRef; it makes nothing.
	CheckBase(ctx context.Context, repo, ref string) error
	// Status reports uncommitted paths and the branch and head for task results.
	// A path that holds something s does not own (another branch checked
	// out, not a worktree root) fails with ErrWorkspaceNotOwned, which a
	// retry does not clear.
	Status(ctx context.Context, s WorkspaceSpec) (WorkspaceStatus, error)
	// Remove deletes the working copy for s and keeps its branch. It refuses
	// uncommitted work unless s.Confirm equals the current Status fingerprint.
	Remove(ctx context.Context, s WorkspaceSpec) error
	// Checkpoint saves the working copy for s, tracked and untracked files,
	// as the commit at ref, unless ref exists already. The branch, index and
	// log are left as they are.
	Checkpoint(ctx context.Context, s WorkspaceSpec, ref string) error
	// CheckpointDiff is the change in repo from checkpoint ref from to ref
	// to; a ref that does not exist fails with ErrNoCheckpoint.
	CheckpointDiff(ctx context.Context, repo, from, to string) (CheckpointDiff, error)
	// DropCheckpoints deletes every ref under prefix in repo.
	DropCheckpoints(ctx context.Context, repo, prefix string) error
	// Publish pushes the agent's branch and opens or updates its one PR.
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
}

// ErrWorkspaceNotOwned is the Status failure for a working copy path that
// holds something its spec does not own, such as a worktree the agent moved
// to another branch. It lasts until someone fixes the path by hand.
var ErrWorkspaceNotOwned = errors.New("loomagent: working copy not owned by its spec")

// ErrNoCheckpoint is the CheckpointDiff failure for a ref that does not exist.
var ErrNoCheckpoint = errors.New("loomagent: no such checkpoint")

// CheckpointDiff is the change between two checkpoints: each file added,
// modified, deleted or type_changed (no rename detection), and the patch.
type CheckpointDiff struct {
	Files []ChangedFile
	Patch string
}

// ChangedFile is one path a CheckpointDiff changes.
type ChangedFile struct {
	Path   string
	Status string
}

// WorkspaceSpec names the working copy an agent needs.
type WorkspaceSpec struct {
	Key      string // from the AgentID; one path segment
	Repo     string // the workspace's repo clone
	BaseRef  string // branch or SHA to start from; local or remote-tracking
	Branch   string // "loom/agent/<id>", or empty for detached
	Detached bool   // reviewers: detached at BaseRef (a head SHA)
	Confirm  string // Remove only: the Status fingerprint the user confirmed deleting
}

// WorkingCopy is an ensured working copy.
type WorkingCopy struct {
	Path   string
	Branch string // empty when detached
	HEAD   string
}

// WorkspaceStatus is a working copy's state for task results.
type WorkspaceStatus struct {
	Uncommitted []string // paths with uncommitted changes
	Fingerprint string   // changes whenever an uncommitted path or its content changes
	Branch      string
	HEAD        string
}

// PublishRequest asks to publish an agent's branch as its PR.
type PublishRequest struct {
	Workspace WorkspaceSpec
	Title     string
	Body      string
}

// PublishResult is the agent's PR after Publish.
type PublishResult struct {
	Number int
	URL    string
	HEAD   string
}
