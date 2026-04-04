// Issue tracking — package-level IssueBackend state and legacy type definitions.
//
// The IssueBackend interface is defined in internal/backend/issuebackend.go.
// This file provides lazy initialization and test overrides for the
// package-level IssueBackend instance used by CLI commands.
//
// Legacy types (IssueTracker, ReadyOpts, ListOpts, UpdateOpts) are kept
// temporarily so that dead-code files (bdBackend, fleetDBBackend,
// MockIssueTracker) still compile. They will be removed in task .9.

package cli

import (
	"context"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// IssueTracker is the legacy issue data access interface.
// Kept so that bdBackend, fleetDBBackend, and MockIssueTracker still compile.
// Will be removed in task .9.
//
// Deprecated: All production callers now use backend.IssueBackend.
type IssueTracker interface {
	Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error)
	List(ctx context.Context, opts ListOpts) ([]BdIssue, error)
	Blocked(ctx context.Context) ([]BdIssue, error)
	Stats(ctx context.Context) (*BdStats, error)
	GetIssue(ctx context.Context, id string) (*BdIssue, error)
	GetIssueText(ctx context.Context, id string) (string, error)
	UpdateIssue(ctx context.Context, id string, opts UpdateOpts) error
	UpdateExternalRef(ctx context.Context, id, ref string) error
	CloseIssue(ctx context.Context, id, reason string) error
	BackendName() string
}

// ReadyOpts configures the Ready query.
// Will be removed in task .9.
//
// Deprecated: Production code uses backend.ReadyOpts directly.
type ReadyOpts struct {
	ParentID    string
	Limit       int
	Labels      []string
	SourceRepos []string
}

// ListOpts configures the List query.
// Will be removed in task .9.
//
// Deprecated: Production code uses backend.ListOpts directly.
type ListOpts struct {
	Status   string
	Assignee string
	Type     string
	ParentID string
	Limit    int
}

// UpdateOpts configures issue field updates.
// Will be removed in task .9.
//
// Deprecated: Production code uses backend.UpdateParams directly.
type UpdateOpts struct {
	Status   string
	Assignee *string
	Design   string
	Claim    bool
}

// --- Package-level IssueBackend state ---

var (
	trackerMu   sync.RWMutex
	trackerInst backend.IssueBackend
)

// defaultIssueBackend returns the package-level IssueBackend, lazily initializing
// from defaultDeps.IssueBackend if not explicitly set.
func defaultIssueBackend() backend.IssueBackend {
	trackerMu.RLock()
	t := trackerInst
	trackerMu.RUnlock()
	if t != nil {
		return t
	}
	trackerMu.Lock()
	defer trackerMu.Unlock()
	if trackerInst == nil {
		if t := defaultDeps.IssueBackend; t != nil {
			trackerInst = t
		} else {
			return newCliBeadsAdapter(defaultBDRunnerImpl{}, GetBeadsDir())
		}
	}
	return trackerInst
}

// setDefaultIssueBackend overrides the package-level IssueBackend (for testing).
func setDefaultIssueBackend(ib backend.IssueBackend) {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = ib
}

// resetDefaultIssueBackend clears the override so defaultIssueBackend() re-initializes.
func resetDefaultIssueBackend() {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInst = nil
}
