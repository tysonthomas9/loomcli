package loomgit

import (
	"context"
	"errors"
	"time"
)

var ErrLeaseHeld = errors.New("lease held by another owner")

// Lease fences writes to one ownership scope across serve and daemon.
type Lease struct {
	Scope     string
	Owner     string
	Fence     int64
	ExpiresAt time.Time
}

// JournalEntry is the durable state of one requested mutation. A caller must
// persist Started before performing an external effect, then advance phases
// using the returned version. Done entries retain their result for retries.
type JournalEntry struct {
	ID        string
	RequestID string
	Operation string
	Phase     string
	Version   int64
	Fence     int64
	Result    []byte
}

// OutboxEvent is committed in the same transaction as a phase change.
type OutboxEvent struct {
	ID           int64
	EntryID      string
	Kind         string
	Payload      []byte
	Delivered    bool
	JSONLEmitted bool
}

// Revision is one immutable source or derived version of a change. Ready is
// false only while a reserved revision is being installed or recovered.
// NoChanges marks a complete source revision whose tree equals its base tree:
// the attempt changed nothing, so it is never reviewed, applied or published.
// Derived revisions never set it.
type Revision struct {
	Workspace, Change, RequestID string
	Number                       int
	Kind, Operation, Outcome     string
	BaseSHA, HeadSHA, TreeHash   string
	SourceHeadSHA                string
	DerivedFromChange            string
	DerivedFromNumber            int
	Ready                        bool
	Incomplete                   bool
	NoChanges                    bool
}

// AppliedLayer attributes commits installed in a lead's working area.
// P2.13 extends this log with the lead's own change.
type AppliedLayer struct {
	RequestID, Workspace, Lead, Change string
	Revision                           int
	OldTip, NewTip, Phase              string
	Commits, DroppedCommits            []string
	CommitDetails                      []AppliedCommit
}

type AppliedCommit struct {
	SHA, Change, Revision, Task, Attempt string
}

// Verdict is an immutable decision about one exact revision head.
type Verdict struct {
	ID                                        int64
	Workspace, Change                         string
	Number                                    int
	HeadSHA, Kind, ActorKind, ActorID, Reason string
	TargetLead                                string
	SourceVerdictID                           int64
}

// WorkspaceRepo records the trunk independently of the lead's working branch.
type WorkspaceRepo struct {
	Workspace, Repo, Trunk, WorkspaceBranch string
	BaseSHA                                 string
}

// WorkspaceStore commits all repo records with the workspace creation journal
// entry. Git checkouts are performed after Begin and before CommitWorkspace.
type WorkspaceStore interface {
	Begin(context.Context, string, string) (JournalEntry, bool, error)
	CommitWorkspace(context.Context, JournalEntry, []WorkspaceRepo) error
	AbortWorkspace(context.Context, JournalEntry) error
	WorkspaceRepos(context.Context, string) ([]WorkspaceRepo, error)
}

// RevisionStore reserves monotonically numbered revisions and finishes them
// after their immutable refs have been installed.
type RevisionStore interface {
	ReserveRevision(context.Context, Revision) (Revision, error)
	FinishRevision(context.Context, Revision) error
	GetRevision(context.Context, string, string, int) (Revision, error)
}

// Store is the persistence port for journaled Git operations. Implementations
// must use row compare-and-swap for Advance and Takeover; callers cannot rely
// on a process mutex, file lock, or SQLite's single-writer behavior.
type Store interface {
	ClaimLease(context.Context, string, string, time.Duration) (Lease, error)
	RenewLease(context.Context, Lease, time.Duration) (Lease, error)
	ReleaseLease(context.Context, Lease) error
	Begin(context.Context, string, string) (JournalEntry, bool, error)
	Get(context.Context, string) (JournalEntry, error)
	OpenEntries(context.Context) ([]JournalEntry, error)
	Advance(context.Context, JournalEntry, string, []byte, []OutboxEvent) (JournalEntry, error)
	Takeover(context.Context, JournalEntry) (JournalEntry, error)
	PendingEvents(context.Context) ([]OutboxEvent, error)
	MarkDelivered(context.Context, int64) error
	Close() error
}

// RepoStore is the Git object and ref boundary for one source repository.
// A provider-backed implementation can replace the local implementation.
type RepoStore interface {
	Path() string
	Run(context.Context, ...string) ([]byte, error)
	RunWithEnv(context.Context, map[string]string, ...string) ([]byte, error)
	UpdateRef(context.Context, string, string, string) error
}
