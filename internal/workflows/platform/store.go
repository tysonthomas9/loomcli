package platform

import "context"

// Store groups the platform-entity sub-stores the workflow runner
// needs. Implemented by platformdb.Client (HTTP against fleet-db) and
// MemStore (in-memory, tests).
type Store interface {
	Drivers() DriverStore
	DriverRuns() DriverRunStore
	TaskRuns() TaskRunStore
	ActionLedger() ActionLedgerStore
	Events() EventStore
}

// DriverStore manages Driver and DriverVersion records. Phase 1 only
// needs ensure-style registration for dev-mode version stamping.
type DriverStore interface {
	// Get returns the driver or domain.ErrNotFound.
	Get(ctx context.Context, ws, driverID string) (*Driver, error)
	// Create registers a driver. Returns domain.ErrAlreadyExists when
	// the driver_id is taken.
	Create(ctx context.Context, ws string, d Driver) (*Driver, error)
	// CreateVersion registers an immutable version under the driver.
	CreateVersion(ctx context.Context, ws, driverID string, v DriverVersion) (*DriverVersion, error)
	// Activate flips the driver's active_version_id.
	Activate(ctx context.Context, ws, driverID, versionID string) (*Driver, error)
}

// DriverRunStore manages the DriverRun lifecycle.
type DriverRunStore interface {
	// Create admits a run. An idempotency-key hit or an active run on
	// the same epic returns the EXISTING run (compare RunID to detect).
	Create(ctx context.Context, ws string, in DriverRunCreate) (*DriverRun, error)
	Get(ctx context.Context, ws, runID string) (*DriverRun, error)
	List(ctx context.Context, ws string, f DriverRunFilter) ([]*DriverRun, error)
	// Claim transitions queued→running and returns the fencing token on
	// the run. domain.ErrConflict when already claimed or not queued.
	Claim(ctx context.Context, ws, runID, nodeID, leaseID string) (*DriverRun, error)
	// Heartbeat renews ownership; the owner triple must match.
	Heartbeat(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64) (*DriverRun, error)
	// Finish transitions running→terminal; the owner triple must match.
	Finish(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64, in DriverRunFinish) (*DriverRun, error)
	// RecoverStale fails running runs whose heartbeat predates maxAge
	// (seconds; 0 = fleet-db's default) and returns the recovered run
	// IDs.
	RecoverStale(ctx context.Context, ws string, maxAge int64, errorClass, summary string) ([]string, error)
	// Events returns the run's lifecycle events after the cursor
	// ("0" = beginning) plus the next cursor.
	Events(ctx context.Context, ws, runID, after string, limit int) ([]RunEvent, string, error)
}

// TaskRunStore manages child TaskRun records.
type TaskRunStore interface {
	// Create returns domain.ErrAlreadyExists for a duplicate TaskRunID —
	// the idempotency mechanism for option-(b) task starts.
	Create(ctx context.Context, ws string, in TaskRunCreate) (*TaskRun, error)
	List(ctx context.Context, ws string, f TaskRunFilter) ([]*TaskRun, error)
}

// ActionLedgerStore records effectively-once side effects.
type ActionLedgerStore interface {
	// Create is idempotent on IdempotencyKey: a repeat create returns
	// the existing entry (inspect Status to decide whether to apply).
	Create(ctx context.Context, ws string, in LedgerCreate) (*LedgerEntry, error)
	// Complete transitions the entry to a terminal status.
	Complete(ctx context.Context, ws, actionID string, status LedgerStatus) (*LedgerEntry, error)
}

// EventStore exposes the workspace mutation feed used to wake
// reconcilers.
type EventStore interface {
	// Poll long-polls the mutation feed. It returns an empty page (with
	// an advanced cursor) on timeout — that is not an error.
	Poll(ctx context.Context, ws string, req MutationPoll) (*MutationPage, error)
}
