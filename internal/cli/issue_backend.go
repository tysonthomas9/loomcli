// Issue tracking interfaces.
//
// This file defines IssueBackend and IssueTracker for abstracting issue
// data operations (ready, list, update, close, etc.) across backends
// (beads bd CLI, fleet-db). These are distinct from Backend and
// StreamingBackend (defined in backend.go and backend_capabilities.go)
// which handle AI agent invocation (Claude, etc.).

package cli

import (
	"context"
	"sync"
)

// IssueBackend is the backward-compatible interface for Phase 1 migration.
// It wraps raw bd-style command execution so existing callers don't change.
// Phase 1 callers use only this interface via RunCommand.
type IssueBackend interface {
	// RunCommand executes a bd-style command and returns stdout or error.
	// The dir parameter specifies the working directory (typically GetBeadsDir()).
	// Example: RunCommand(dir, "ready", "--json", "--limit", "50")
	RunCommand(dir string, args ...string) (string, error)
}

// IssueTracker extends IssueBackend with typed methods for direct data access.
// Phase 2+ consumers should prefer these over RunCommand to avoid JSON
// serialization overhead. This interface is defined upfront to establish
// the full contract that bdBackend (task .2) and fleetDBBackend (task .3)
// must satisfy.
type IssueTracker interface {
	IssueBackend

	// Query operations
	Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error)
	List(ctx context.Context, opts ListOpts) ([]BdIssue, error)
	Blocked(ctx context.Context) ([]BdIssue, error)
	Stats(ctx context.Context) (*BdStats, error)
	GetIssue(ctx context.Context, id string) (*BdIssue, error)
	GetIssueText(ctx context.Context, id string) (string, error)

	// Mutation operations
	UpdateIssue(ctx context.Context, id string, opts UpdateOpts) error
	UpdateExternalRef(ctx context.Context, id, ref string) error
	CloseIssue(ctx context.Context, id, reason string) error

	// Metadata
	BackendName() string
}

// ReadyOpts configures the Ready query.
type ReadyOpts struct {
	ParentID    string   // filter by parent epic ID (empty = no filter)
	Limit       int      // max results (0 = backend default)
	Labels      []string // filter by labels (empty = no filter); e.g. ["repo:frontend"]
	SourceRepos []string // filter by source repos (empty = no filter); maps to --source-repos
}

// ListOpts configures the List query.
type ListOpts struct {
	Status   string // filter by status (empty = all)
	Assignee string // filter by assignee (empty = all)
	Type     string // filter by issue_type (empty = all)
	Limit    int    // max results (0 = backend default)
}

// UpdateOpts configures issue field updates. Zero-value fields are not sent.
type UpdateOpts struct {
	Status   string  // new status (empty = don't change)
	Assignee *string // new assignee (nil = don't change, pointer to "" = clear)
	Design   string  // new design text (empty = don't change)
	Claim    bool    // if true, atomically claim the issue
}

// --- Package-level tracker state ---

var (
	trackerMu   sync.RWMutex
	trackerInst IssueTracker
)

// defaultTracker returns the package-level IssueTracker, lazily initializing
// from defaultDeps.Tracker if not explicitly set.
func defaultTracker() IssueTracker {
	trackerMu.RLock()
	t := trackerInst
	trackerMu.RUnlock()
	if t != nil {
		return t
	}
	trackerMu.Lock()
	defer trackerMu.Unlock()
	if trackerInst == nil {
		if t := defaultDeps.Tracker; t != nil {
			trackerInst = t
		} else {
			// Fallback: don't cache nil — return an ephemeral backend so
			// callers never get a nil pointer even if defaultDeps.Tracker
			// was not set (e.g. partial test fixtures).
			return newBdBackend(defaultBDRunner{}, GetBeadsDir())
		}
	}
	return trackerInst
}

// setDefaultTracker overrides the package-level tracker (for testing).
func setDefaultTracker(t IssueTracker) {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = t
}

// resetDefaultTracker clears the override so defaultTracker() re-initializes.
func resetDefaultTracker() {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = nil
}
