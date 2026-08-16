package repositoryadmission

import (
	"context"
	"errors"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

var (
	ErrUnavailable = errors.New("repository admission: durable operations unavailable")
	ErrNotFound    = errors.New("repository admission: not found")
	ErrInvalid     = errors.New("repository admission: invalid")
	ErrConflict    = errors.New("repository admission: conflict")
	ErrFenceLost   = errors.New("repository admission: owner fence lost")
	ErrState       = errors.New("repository admission: state conflict")
)

// DurableAdmissions is the Repository Admission-owned durability port.
// Implementations persist exact intent, owner generations, replay identity,
// and terminal receipts; they do not perform local checkout materialization.
type DurableAdmissions interface {
	CreateWorkspace(context.Context, WorkspaceBegin) (*WorkspaceBeginResult, error)
	Begin(context.Context, string, Begin) (*Record, error)
	Get(context.Context, string, string) (*Record, error)
	GetByOperation(context.Context, string, string) (*Record, error)
	ListRecoverable(context.Context, string, int) ([]*Record, error)
	Renew(context.Context, Renew) (*Record, error)
	ClaimRecovery(context.Context, RecoveryClaim) (*Record, error)
	Commit(context.Context, Commit) (*Record, error)
	Fail(context.Context, Fail) (*Record, error)
	Abort(context.Context, Abort) (*Record, error)
}

type RepositorySpec struct {
	Name          string
	RemoteURL     string
	Remote        string
	DefaultBranch string
	Groups        []string
	SourceRepoID  string
}

type Spec struct {
	WorkspaceKey string
	OperationID  string
	Repositories []RepositorySpec
}

type Begin struct {
	OperationID  string
	OwnerID      string
	OwnerLease   time.Duration
	Repositories []RepositorySpec
}

type WorkspaceInput struct {
	Key           string
	Name          string
	Description   string
	State         string
	ErrorMessage  string
	DefaultBranch string
	DesignFormat  string
}

type WorkspaceBegin struct {
	Workspace    WorkspaceInput
	OperationID  string
	OwnerID      string
	OwnerLease   time.Duration
	Repositories []RepositorySpec
}

type WorkspaceBeginResult struct {
	Workspace        *workspacemodule.Workspace
	Admission        *Record
	WorkspaceEventID string
}

type RepositoryReceipt struct {
	Repository workspacemodule.Repository
	EventID    string
}

type Receipt struct {
	AdmissionID           string
	SpecFingerprint       string
	Repositories          []RepositoryReceipt
	WorkspaceFinalization *WorkspaceFinalization
	CommittedAt           time.Time
}

type WorkspaceFinalization struct {
	State         string
	DefaultBranch string
}

type Record struct {
	AdmissionID         string
	WorkspaceKey        string
	OperationID         string
	OwnerID             string
	OwnerGenerationID   string
	OwnerLeaseExpiresAt time.Time
	SpecFingerprint     string
	Spec                Spec
	State               string
	LastErrorClass      string
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	TerminalAt          *time.Time
	Receipt             *Receipt
}

type Guard struct {
	WorkspaceKey      string
	AdmissionID       string
	OwnerID           string
	OwnerGenerationID string
	SpecFingerprint   string
	ExpectedVersion   int64
}

type ResolvedBranch struct {
	Name          string
	DefaultBranch string
}

type Renew struct {
	Guard
	Lease time.Duration
}

type RecoveryClaim struct {
	WorkspaceKey            string
	AdmissionID             string
	ExpectedSpecFingerprint string
	ExpectedVersion         int64
	NewOwnerID              string
	Lease                   time.Duration
}

type Commit struct {
	Guard
	ResolvedDefaultBranches []ResolvedBranch
	WorkspaceFinalization   *WorkspaceFinalization
}

type Fail struct {
	Guard
	ErrorClass string
	Retryable  bool
}

type Abort struct {
	Guard
	ReasonClass string
}
