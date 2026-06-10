// Package platform defines the control-plane view of FleetDB's
// platform entities (drivers, driver runs, task runs, action ledger)
// and the narrow store interfaces the workflow runner needs.
//
// It deliberately does NOT extend store.Store: platform entities carry
// lifecycle semantics (fencing tokens, one_active_per_epic admission,
// idempotency keys) that don't fit the CRUD contract, and forcing
// memstore parity would cost hundreds of lines before any feature
// works. Implementations: internal/infra/platformdb (HTTP against
// fleet-db) and MemStore in this package (tests).
package platform

import (
	"encoding/json"
	"time"
)

// DriverRunStatus mirrors fleet-db's models.DriverRunStatus.
type DriverRunStatus string

const (
	DriverRunQueued      DriverRunStatus = "queued"
	DriverRunRunning     DriverRunStatus = "running"
	DriverRunCompleted   DriverRunStatus = "completed"
	DriverRunFailed      DriverRunStatus = "failed"
	DriverRunNeedsReview DriverRunStatus = "needs_review"
	DriverRunCancelled   DriverRunStatus = "cancelled"
)

// Terminal reports whether the status is a final state.
func (s DriverRunStatus) Terminal() bool {
	switch s {
	case DriverRunCompleted, DriverRunFailed, DriverRunNeedsReview, DriverRunCancelled:
		return true
	default:
		return false
	}
}

// Driver is an installed workflow program (fleet-db Driver record).
type Driver struct {
	DriverID        string            `json:"driver_id"`
	Name            string            `json:"name"`
	OwnerType       string            `json:"owner_type,omitempty"`
	Description     string            `json:"description,omitempty"`
	ActiveVersionID string            `json:"active_version_id,omitempty"`
	Status          string            `json:"status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// DriverVersion is an immutable build of a driver. fleet-db requires
// source_digest and bundle_digest; dev-mode stamps placeholder digests.
type DriverVersion struct {
	VersionID        string            `json:"version_id"`
	DriverID         string            `json:"driver_id"`
	Version          int               `json:"version"`
	SourceRef        string            `json:"source_ref,omitempty"`
	SourceDigest     string            `json:"source_digest"`
	BundleRef        string            `json:"bundle_ref,omitempty"`
	BundleDigest     string            `json:"bundle_digest"`
	Runtime          string            `json:"runtime,omitempty"`
	Manifest         map[string]string `json:"manifest,omitempty"`
	ValidationStatus string            `json:"validation_status,omitempty"`
	CreatedBy        string            `json:"created_by,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
}

// DriverRun is one admission-checked execution of a driver. For the
// epic runner this is one reconcile wake.
type DriverRun struct {
	RunID           string            `json:"run_id"`
	DriverID        string            `json:"driver_id"`
	DriverVersionID string            `json:"driver_version_id"`
	Entrypoint      string            `json:"entrypoint,omitempty"`
	SourceKind      string            `json:"source_kind,omitempty"`
	SourceRef       string            `json:"source_ref,omitempty"`
	EpicID          string            `json:"epic_id,omitempty"`
	Status          DriverRunStatus   `json:"status"`
	NodeID          string            `json:"node_id,omitempty"`
	LeaseID         string            `json:"lease_id,omitempty"`
	FencingToken    int64             `json:"fencing_token,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	Payload         json.RawMessage   `json:"payload,omitempty"`
	Output          map[string]string `json:"output,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	ErrorClass      string            `json:"error_class,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	LastHeartbeat   time.Time         `json:"last_heartbeat"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// DriverRunCreate is the admission request for a new DriverRun.
//
// fleet-db's create is atomic: an idempotency-key hit or an active run
// for the same epic (one_active_per_epic) returns the EXISTING run
// rather than an error. Callers detect "admission deduped" by
// comparing the returned RunID against the requested one.
type DriverRunCreate struct {
	RunID           string          `json:"run_id"`
	DriverID        string          `json:"driver_id"`
	DriverVersionID string          `json:"driver_version_id"`
	Entrypoint      string          `json:"entrypoint,omitempty"`
	SourceKind      string          `json:"source_kind,omitempty"`
	SourceRef       string          `json:"source_ref,omitempty"`
	EpicID          string          `json:"epic_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

// DriverRunFinish carries the terminal transition for a claimed run.
type DriverRunFinish struct {
	Status     DriverRunStatus   `json:"status"`
	Summary    string            `json:"summary,omitempty"`
	ErrorClass string            `json:"error_class,omitempty"`
	Output     map[string]string `json:"output,omitempty"`
}

// DriverRunFilter narrows DriverRun list queries.
type DriverRunFilter struct {
	DriverID string
	EpicID   string
	Status   DriverRunStatus
	Limit    int
}

// RunEvent is a lifecycle event recorded by fleet-db for a DriverRun
// (driver_run.create / claim / heartbeat / finish / recover).
type RunEvent struct {
	ID         string            `json:"id"`
	Timestamp  time.Time         `json:"timestamp"`
	Actor      string            `json:"actor"`
	Action     string            `json:"action"`
	EntityType string            `json:"entity_type"`
	EntityID   string            `json:"entity_id"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// TaskRunStatus mirrors fleet-db's models.TaskRunStatus.
type TaskRunStatus string

const (
	TaskRunQueued    TaskRunStatus = "queued"
	TaskRunRunning   TaskRunStatus = "running"
	TaskRunCompleted TaskRunStatus = "completed"
	TaskRunFailed    TaskRunStatus = "failed"
	TaskRunCancelled TaskRunStatus = "cancelled"
)

// TaskRun is a control-plane record of one spawned child task. With
// the option-(b) seam, the TaskRun references an existing Issue
// (TaskID) and the issue-claiming agent supervisor executes it
// untouched; the TaskRun itself is the audit/control record.
type TaskRun struct {
	TaskRunID    string        `json:"task_run_id"`
	DriverRunID  string        `json:"driver_run_id,omitempty"`
	DriverStepID string        `json:"driver_step_id,omitempty"`
	TaskID       string        `json:"task_id"`
	Status       TaskRunStatus `json:"status,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// TaskRunCreate creates a TaskRun. fleet-db has no idempotency_key on
// this endpoint; callers get effectively-once creation by using a
// deterministic TaskRunID (duplicate IDs return ErrAlreadyExists).
type TaskRunCreate struct {
	TaskRunID   string `json:"task_run_id"`
	DriverRunID string `json:"driver_run_id,omitempty"`
	TaskID      string `json:"task_id"`
}

// TaskRunFilter narrows TaskRun list queries.
type TaskRunFilter struct {
	DriverRunID string
	TaskID      string
	Status      TaskRunStatus
	Limit       int
}

// LedgerStatus mirrors fleet-db's ActionLedger status enum.
type LedgerStatus string

const (
	LedgerPending LedgerStatus = "pending"
	LedgerApplied LedgerStatus = "applied"
	LedgerFailed  LedgerStatus = "failed"
	LedgerSkipped LedgerStatus = "skipped"
)

// LedgerEntry records one effectively-once side effect.
type LedgerEntry struct {
	ActionID       string       `json:"action_id"`
	IdempotencyKey string       `json:"idempotency_key"`
	ActionType     string       `json:"action_type"`
	TargetRef      string       `json:"target_ref"`
	RequestedBy    string       `json:"requested_by,omitempty"`
	Status         LedgerStatus `json:"status"`
	AppliedAt      *time.Time   `json:"applied_at,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
}

// LedgerCreate opens a ledger entry. Creation is idempotent on
// IdempotencyKey: a repeat create returns the existing entry.
type LedgerCreate struct {
	IdempotencyKey string `json:"idempotency_key"`
	ActionType     string `json:"action_type"`
	TargetRef      string `json:"target_ref"`
}

// MutationEvent is one entry from fleet-db's workspace mutation feed.
type MutationEvent struct {
	ID         string            `json:"id"`
	Timestamp  time.Time         `json:"timestamp"`
	Actor      string            `json:"actor"`
	Action     string            `json:"action"`
	EntityType string            `json:"entity_type"`
	EntityID   string            `json:"entity_id"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// MutationPoll is a long-poll request against the mutation feed.
// Since is "0" for the beginning or an opaque stream cursor from a
// prior response. Timeout is the server-side block in milliseconds
// (0 = return immediately; otherwise fleet-db accepts 1000–10000).
type MutationPoll struct {
	Since   string
	Timeout time.Duration
	Limit   int
}

// MutationPage is one long-poll response.
type MutationPage struct {
	Events  []MutationEvent `json:"events"`
	Cursor  string          `json:"cursor"`
	HasMore bool            `json:"has_more"`
}
