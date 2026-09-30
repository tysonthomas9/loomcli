package loomgit

import (
	"context"
	"time"
)

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
	ID        int64
	EntryID   string
	Kind      string
	Payload   []byte
	Delivered bool
}

// Revision is one immutable source or derived version of a change. Ready is
// false only while a reserved revision is being installed or recovered.
type Revision struct {
	Workspace, Change, RequestID string
	Number                       int
	Kind, Operation, Outcome     string
	BaseSHA, HeadSHA, TreeHash   string
	SourceHeadSHA                string
	DerivedFromChange            string
	DerivedFromNumber            int
	Ready                        bool
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
