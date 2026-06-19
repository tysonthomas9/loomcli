// Package stackpublish drives the stack-aware PR publisher: a repo-scoped GitHub
// Forge plus a cursor-less, idempotent Reconciler that implements the four-phase
// reorder (reparent affected PRs to a safe base, push branches, set final bases,
// create/close) so it avoids both the spr ghost-merge and the git-town 422.
//
// Posture is fully control-plane authoritative (decision 1): publish corrects all
// drift to match desired lineage, a merged PR is terminal, and its descendants
// auto-slide to RootBase. See docs/design/2026-06-18-stack-aware-pr-publisher.md.
package stackpublish

import "context"

// PR is the repo-scoped view of a GitHub pull request the reconciler needs.
type PR struct {
	Number int
	Head   string // head ref (branch) name
	Base   string // base ref (branch) name
	State  string // "open" | "closed"
	Merged bool
	Title  string
	Body   string
	URL    string
}

// Forge is the repo-scoped Git/GitHub surface the reconciler depends on. Every
// query is scoped to a single (owner, repo) — never account-wide — which is the
// fix for spr's viewer-wide discovery.
type Forge interface {
	// ListStackPRs returns all PRs (any state) whose head ref starts with
	// headPrefix, e.g. "loom/stack/epic-E1/". Used for Phase-0 discovery,
	// adoption by head, merged detection, and orphan detection.
	ListStackPRs(ctx context.Context, owner, repo, headPrefix string) ([]PR, error)
	// CreatePR opens a PR (ready for review — no draft, decision 4).
	CreatePR(ctx context.Context, owner, repo, head, base, title, body string) (PR, error)
	// UpdatePRBase retargets an open PR's base branch.
	UpdatePRBase(ctx context.Context, owner, repo string, number int, base string) error
	// ClosePR closes a PR (optionally posting a comment first). Branches are kept (decision 2).
	ClosePR(ctx context.Context, owner, repo string, number int, comment string) error
	// PushBranches atomically pushes the given local branches from repoPath to
	// origin. Each push carries an explicit expected remote SHA so the lease is
	// asserted against the actual remote (not stale remote-tracking state); an
	// empty ExpectedSHA means a new branch (create, no lease).
	PushBranches(ctx context.Context, repoPath string, pushes []BranchPush) error
	// QueuedPRNumbers returns the set of open PR numbers currently in the repo's
	// GitHub merge queue (whose base branch is therefore immutable). Used by the
	// reconciler's pre-flight to abort a reorder before any mutation.
	QueuedPRNumbers(ctx context.Context, owner, repo string) (map[int]bool, error)
	// PRStatuses returns per-PR health (checks/review/mergeable) for open PRs whose
	// head starts with headPrefix, keyed by head ref. Read-only; for status display.
	PRStatuses(ctx context.Context, owner, repo, headPrefix string) (map[string]PRStatus, error)
	// UpdatePRBody sets a PR's description (used to write the stack listing).
	UpdatePRBody(ctx context.Context, owner, repo string, number int, body string) error
}

// BranchPush is one branch to push, with the SHA the remote ref is expected to
// be at (for `--force-with-lease=<ref>:<sha>`). Empty ExpectedSHA = new branch.
type BranchPush struct {
	Branch      string
	ExpectedSHA string
}

// PRStatus is the read-only health of a PR, for the tasks/PR-page display.
type PRStatus struct {
	Number    int    `json:"number"`
	Checks    string `json:"checks"`    // passing | failing | pending | none
	Review    string `json:"review"`    // approved | changes_requested | review_required | none
	Mergeable string `json:"mergeable"` // mergeable | conflicting | unknown
}
