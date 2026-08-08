package connectors

import (
	"context"
	"time"
)

// ConnectorGrantStore is the Connectors-owned persistence port for the two
// Fleet operations required by EnsureGrant. FleetDB has no Get-by-grant-ID
// route, so implementations must use the binding-filtered active listing and
// must not invent a hidden read path.
type ConnectorGrantStore interface {
	CreateGrant(context.Context, CreateGrantMutation) (*ConnectorGrant, error)
	ListGrantsByBinding(context.Context, string, string) ([]*ConnectorGrant, error)
}

type CreateGrantMutation struct {
	WorkspaceKey    string
	GrantID         string
	ConnectorID     string
	BindingID       string
	Action          string
	ResourcePattern string
}

// ManagementStore is the Connector owner's durable definition, grant, and
// audit port. Secret reads, vault operations, and provider dispatch are
// deliberately absent from this query/lifecycle slice.
type ManagementStore interface {
	SecretLifecycleStore
	CreateConnectorRecord(context.Context, CreateConnectorMutation) (*Connector, error)
	ListConnectorRecords(context.Context, string, ConnectorFilter) ([]*Connector, error)
	CreateManagementGrant(context.Context, CreateGrantMutation) (*ConnectorGrant, error)
	RevokeGrantRecord(context.Context, string, string) error
	ListGrantRecordsByBinding(context.Context, string, string) ([]*ConnectorGrant, error)
	ListGrantRecordsByConnector(context.Context, string, string) ([]*ConnectorGrant, error)
	ListCallRecordsByRun(context.Context, string, string, ConnectorCallFilter) ([]*ConnectorCallRecord, error)
	ListCallRecordsByBinding(context.Context, string, string, ConnectorCallFilter) ([]*ConnectorCallRecord, error)
}

type SecretLifecycleStore interface {
	GetConnectorRecord(context.Context, string, string) (*Connector, error)
	ResolveCurrentInboundSecretRecord(context.Context, string, string) (string, error)
	ResolveOutboundCredentialSealedRecord(context.Context, string, string) ([]byte, error)
	RotateConnectorSecretsRecord(context.Context, string, string, RotateConnectorSecretsMutation) (*Connector, error)
	AppendConnectorCallRecord(context.Context, *ConnectorCallRecord) error
}

type CreateConnectorMutation struct {
	WorkspaceKey             string
	ConnectorID              string
	SourceKind               ConnectorSourceKind
	DisplayName              string
	InboundEndpointPath      string
	InboundSecret            string
	OutboundCredentialSealed []byte
	Status                   ConnectorStatus
	CreatedBy                string
}

type RotateConnectorSecretsMutation struct {
	NewInboundSecret            string
	PreviousSecretValidUntil    time.Time
	ExpectedUpdatedAt           time.Time
	NewOutboundCredentialSealed []byte
}

// CredentialSealer is the Connectors-owned write-only vault port used during
// rotation. Plaintext enters this one method and is wiped by the service;
// neither persistence nor the public result can receive it.
type CredentialSealer interface {
	Seal([]byte, []byte) ([]byte, error)
}

// CredentialVault is the owner-private comparison seam. Implementations
// unseal only inside the adapter and return equality, never plaintext.
type CredentialVault interface {
	CredentialSealer
	Matches(sealed, plaintext, aad []byte) (bool, error)
}

// CredentialOpener is injected only into the Connectors dispatch service. It
// is intentionally absent from the public Dispatcher API.
type CredentialOpener interface {
	Unseal(sealed, aad []byte) ([]byte, error)
}

type Provider interface {
	Call(context.Context, ProviderCall) (ProviderResult, error)
}

type ProviderRegistry interface {
	Get(ConnectorSourceKind) (Provider, error)
}

// DispatchStore is the exact durable surface required by one provider call.
// ManagementStore satisfies it; no composite store reaches the owner service.
type DispatchStore interface {
	GetConnectorRecord(context.Context, string, string) (*Connector, error)
	ResolveOutboundCredentialSealedRecord(context.Context, string, string) ([]byte, error)
	ListGrantRecordsByBinding(context.Context, string, string) ([]*ConnectorGrant, error)
	AppendConnectorCallRecord(context.Context, *ConnectorCallRecord) error
}

// GitReadExecutor is the Connectors-owned provider-dispatch port. Concrete
// implementations may resolve a secret internally, but neither this port nor
// its command/result types contain plaintext credential fields.
type GitReadExecutor interface {
	// ValidateGitRead resolves and validates the real filesystem containment
	// immediately before execution. It must reject symlinked workspace, parent,
	// and target paths and return a stable resolved target identity for locking.
	ValidateGitRead(context.Context, GitReadCommand) (string, error)
	ExecuteGitRead(context.Context, GitReadCommand) error
}
