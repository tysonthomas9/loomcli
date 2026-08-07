// Package placement brokers lead sandbox placement records against a narrow
// provider interface.
package placement

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/leadtoken"
)

const (
	// PlacementLabelKey is applied in Provider.Create so reconciliation can
	// match a provider sandbox back to the placement record after a crash.
	PlacementLabelKey = "loom-placement"

	// EnvironmentLabelKey scopes provider-side reconciliation to one Loom
	// deployment sharing a provider account.
	EnvironmentLabelKey = "loom-env"

	// LeadPTYSessionID is the provider PTY session id used for every lead.
	// Serve names every lead PTY by this convention and stores nothing, so
	// the terminal layer must attach using the same id the broker created --
	// two literals that silently drift would make every attach fail as
	// "session not found".
	LeadPTYSessionID = "lead"

	// OccupantTokenEnv is the sandbox environment variable carrying the lead
	// API occupant bearer token.
	OccupantTokenEnv = "LOOM_LEAD_OCCUPANT_TOKEN" //nolint:gosec // env var name, not a credential

	// CapLeadSession is re-exported for placement-created lead nodes and
	// occupant tokens.
	CapLeadSession = leadtoken.CapLeadSession
)

// ErrSandboxNotFound lets release confirmation and resume paths distinguish a
// confirmed-missing sandbox from a provider-side failure.
var ErrSandboxNotFound = errors.New("placement: sandbox not found")

// ErrPtySessionAlreadyExists is a successful CreatePty outcome for the broker:
// the requested idempotent session is already present.
var ErrPtySessionAlreadyExists = errors.New("placement: pty session already exists")

// Provider is the narrow sandbox provider surface required by the broker.
type Provider interface {
	Create(context.Context, CreateRequest) (CreateResult, error)
	Get(context.Context, string) (ProviderSandbox, error)
	ListManaged(context.Context, map[string]string) ([]ProviderSandbox, error)
	Delete(context.Context, string) error
	UpdateLastActivity(context.Context, string) error
	SetAutostopInterval(context.Context, string, time.Duration) error
	// CreatePty creates the requested PTY session if absent. Providers should
	// return ErrPtySessionAlreadyExists when the session already exists.
	CreatePty(context.Context, string, ProcessSpec) error
	ListPtySessions(context.Context, string) ([]PtySession, error)
	KillPtySession(context.Context, string, string) error
}

// ResourceSize is the reserved provider pool capacity for one placement.
type ResourceSize struct {
	VCPU   int
	MemGiB int
}

// CreateRequest is the provider-neutral sandbox creation request.
type CreateRequest struct {
	WorkspaceKey           string
	AgentName              string
	SnapshotRef            string
	Labels                 map[string]string
	Env                    map[string]string
	Resource               ResourceSize
	NetworkDomainAllowlist []string
}

// CreateResult returns the provider's sandbox identity.
type CreateResult struct {
	SandboxID string
}

// ProcessSpec describes the PTY process the provider starts in a sandbox.
type ProcessSpec struct {
	SessionID  string
	Command    []string
	Env        map[string]string
	WorkingDir string
	TTY        bool
}

// PtySession is the provider-visible PTY identity used for idempotent lead boot
// checks.
type PtySession struct {
	SessionID string
}

// ProviderSandboxState is the provider-visible sandbox lifecycle state.
type ProviderSandboxState string

const (
	ProviderSandboxRunning ProviderSandboxState = "running"
	ProviderSandboxStopped ProviderSandboxState = "stopped"
	ProviderSandboxAbsent  ProviderSandboxState = "absent"
)

// ProviderSandbox is a reconciliation view of provider-side sandbox state.
type ProviderSandbox struct {
	ID     string
	Labels map[string]string
	State  ProviderSandboxState
}
