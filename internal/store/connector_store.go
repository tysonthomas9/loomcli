package store

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ConnectorCreate carries the fields for a new connector. A workspace may
// hold multiple named connectors per source kind; identity is
// (WorkspaceKey, ConnectorID).
type ConnectorCreate struct {
	WorkspaceKey        string
	ConnectorID         string
	SourceKind          domain.ConnectorSourceKind
	DisplayName         string
	InboundEndpointPath string
	InboundSecret       string
	// OutboundCredentialSealed must already be ciphertext: serve's vault
	// layer seals the credential (AES-256-GCM under
	// LOOM_CONNECTOR_VAULT_KEY) BEFORE the store write. Stores never see
	// plaintext.
	OutboundCredentialSealed []byte
	// Status defaults to active when empty.
	Status    domain.ConnectorStatus
	CreatedBy string
}

// ConnectorFilter narrows connector listings.
type ConnectorFilter struct {
	SourceKind domain.ConnectorSourceKind
	Status     domain.ConnectorStatus
	Limit      int
}

// ConnectorSecretRotation carries a RotateSecrets request. The previous
// inbound secret keeps verifying until PreviousSecretValidUntil; when zero,
// implementations default it to now + domain.DefaultConnectorSecretOverlap
// and cap it at now + domain.MaxConnectorSecretOverlap.
type ConnectorSecretRotation struct {
	NewInboundSecret         string
	PreviousSecretValidUntil time.Time
	// NewOutboundCredentialSealed, when non-nil, replaces the sealed
	// outbound credential (already ciphertext — see ConnectorCreate). Nil
	// leaves the existing credential in place.
	NewOutboundCredentialSealed []byte
}

// ConnectorInboundSecrets is the privileged resolve result for inbound
// signature verification during the dual-secret rotation window. Previous is
// empty outside a rotation window; verifiers matching against Previous must
// emit a stale-secret audit signal.
type ConnectorInboundSecrets struct {
	Current            string
	Previous           string
	PreviousValidUntil time.Time
}

// ConnectorStore persists connectors.
//
// Create has first-writer-wins SetNX semantics: a second Create for the same
// (workspaceKey, connectorID) fails wrapping domain.ErrConnectorExists.
//
// Get and List ALWAYS return redacted connectors (inbound secrets blanked,
// sealed credential dropped — see domain.Connector.Redacted). The Resolve*
// methods are the privileged read paths, mirroring
// TriggerBindingStore.ResolveWebhookSecret.
type ConnectorStore interface {
	Create(ctx context.Context, in ConnectorCreate) (*domain.Connector, error)
	Get(ctx context.Context, workspaceKey, connectorID string) (*domain.Connector, error)
	List(ctx context.Context, workspaceKey string, filter ConnectorFilter) ([]*domain.Connector, error)
	// ResolveInboundSecret fetches the plaintext inbound signing secret(s)
	// for webhook verification — the privileged path the inbound verifier
	// uses; never exposed through Get/List.
	ResolveInboundSecret(ctx context.Context, workspaceKey, connectorID string) (*ConnectorInboundSecrets, error)
	// ResolveOutboundCredentialSealed fetches the sealed outbound credential
	// ciphertext for serve's vault layer to unseal. Stores hold and return
	// ciphertext only.
	ResolveOutboundCredentialSealed(ctx context.Context, workspaceKey, connectorID string) ([]byte, error)
	// RotateSecrets installs a new inbound secret, demoting the current one
	// to PreviousInboundSecret for the rotation window, and optionally
	// re-seals the outbound credential. Returns the redacted connector.
	RotateSecrets(ctx context.Context, workspaceKey, connectorID string, in ConnectorSecretRotation) (*domain.Connector, error)
}

// ConnectorGrantCreate carries the fields for a new grant.
type ConnectorGrantCreate struct {
	WorkspaceKey    string
	GrantID         string
	ConnectorID     string
	BindingID       string
	Action          string
	ResourcePattern string
}

// ConnectorGrantStore persists binding-scoped egress grants.
//
// Grants are deny-by-default: absence of a matching active grant means the
// call is denied (domain.ErrGrantDenied at the enforcement layer). Revoked
// grants are filtered from ListByBinding/ListByConnector so resolve paths
// only ever see active grants; Revoke on an already-revoked grant fails
// wrapping domain.ErrGrantRevoked.
type ConnectorGrantStore interface {
	Create(ctx context.Context, in ConnectorGrantCreate) (*domain.ConnectorGrant, error)
	Revoke(ctx context.Context, workspaceKey, grantID string) error
	ListByBinding(ctx context.Context, workspaceKey, bindingID string) ([]*domain.ConnectorGrant, error)
	ListByConnector(ctx context.Context, workspaceKey, connectorID string) ([]*domain.ConnectorGrant, error)
}

// ConnectorCallFilter narrows audit listings.
type ConnectorCallFilter struct {
	Decision domain.ConnectorCallDecision
	Limit    int
}

// ConnectorAuditStore is the append-only connector-call journal — a
// dedicated audit trail separate from TaskRunEvent. Every granted AND denied
// egress call appends exactly one record; Append on a duplicate CallID fails
// wrapping domain.ErrAlreadyExists (the deterministic runID#action#seq id
// makes retries idempotent).
type ConnectorAuditStore interface {
	Append(ctx context.Context, rec *domain.ConnectorCallRecord) error
	ListByRun(ctx context.Context, workspaceKey, runID string, filter ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error)
	ListByBinding(ctx context.Context, workspaceKey, bindingID string, filter ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error)
}
