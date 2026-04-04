// Issue tracking — package-level IssueBackend singleton management.
//
// The IssueBackend interface is defined in internal/backend/issuebackend.go.
// This file provides lazy initialization and test overrides for the
// package-level IssueBackend instance used by CLI commands.

package cli

import (
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

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
