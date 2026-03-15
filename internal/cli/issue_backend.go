package cli

import (
	"context"
	"sync"
)

// IssueBackend is the low-level interface for running issue-tracker commands.
// It exists for Phase 1 backward compatibility with BDRunner's exec-based approach.
type IssueBackend interface {
	RunCommand(dir string, args ...string) (string, error)
}

// IssueTracker extends IssueBackend with typed methods for issue operations.
// Implementations include bdBackend (wrapping BDRunner) and fleetDBBackend.
type IssueTracker interface {
	IssueBackend
	Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error)
	List(ctx context.Context, opts ListOpts) ([]BdIssue, error)
	Blocked(ctx context.Context) ([]BdIssue, error)
	Stats(ctx context.Context) (BdStats, error)
	GetIssue(ctx context.Context, id string) (*BdIssue, error)
	GetIssueText(ctx context.Context, id string) (string, error)
	UpdateStatus(ctx context.Context, id, status, assignee string) error
	UpdateExternalRef(ctx context.Context, id, ref string) error
	CloseIssue(ctx context.Context, id, reason string) error
	SyncStatus(ctx context.Context) (string, error)
	BackendName() string
}

// ReadyOpts configures the Ready query.
type ReadyOpts struct {
	Limit    int
	ParentID string
}

// ListOpts configures the List query.
type ListOpts struct {
	Status    string
	IssueType string
	Assignee  string
	Limit     int
}

var (
	trackerMu       sync.RWMutex
	trackerInstance IssueTracker
)

// defaultTracker returns the package-level IssueTracker.
// It panics if setDefaultTracker has not been called.
func defaultTracker() IssueTracker {
	trackerMu.RLock()
	t := trackerInstance
	trackerMu.RUnlock()
	if t != nil {
		return t
	}
	trackerMu.Lock()
	defer trackerMu.Unlock()
	if trackerInstance == nil {
		panic("issue_backend: no tracker configured; call setDefaultTracker first")
	}
	return trackerInstance
}

// setDefaultTracker replaces the package-level IssueTracker.
// Passing nil resets to unconfigured state (useful for test teardown).
func setDefaultTracker(t IssueTracker) {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	trackerInstance = t
}
