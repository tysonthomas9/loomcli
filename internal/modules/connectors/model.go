package connectors

import (
	"fmt"
	"strconv"
	"time"
)

const (
	VaultKeyEnvVar                = "LOOM_CONNECTOR_VAULT_KEY"
	DefaultConnectorSecretOverlap = 15 * time.Minute
	MaxConnectorSecretOverlap     = 24 * time.Hour
	RotationAuditBindingID        = "connector-rotation"
	RotationAuditAction           = "connector.rotate"
)

type DispatchPreconditions struct {
	ExpectedHeadSha  string
	ExpectedRevision string
}

// DispatchCommand contains only the authorized operation coordinates and
// sanitized provider arguments. Credentials and source kind are resolved by
// Connectors and never appear on this public command.
type DispatchCommand struct {
	WorkspaceKey  string
	RunID         string
	BindingID     string
	ConnectorID   string
	Action        string
	Resource      string
	Args          map[string]any
	Preconditions DispatchPreconditions
	CallSeq       int
}

func (command DispatchCommand) Validate() error {
	if command.WorkspaceKey == "" {
		return fmt.Errorf("connector dispatch workspace_key required: %w", ErrInvalid)
	}
	if command.RunID == "" {
		return fmt.Errorf("connector dispatch run_id required: %w", ErrInvalid)
	}
	if command.BindingID == "" {
		return fmt.Errorf("connector dispatch binding_id required: %w", ErrInvalid)
	}
	if command.ConnectorID == "" {
		return fmt.Errorf("connector dispatch connector_id required: %w", ErrInvalid)
	}
	if normalized, err := normalizeConnectorAction(command.Action); err != nil || normalized != command.Action {
		if err == nil {
			err = ErrInvalid
		}
		return fmt.Errorf("invalid connector dispatch action %q: %w", command.Action, err)
	}
	if command.Resource == "" {
		return fmt.Errorf("connector dispatch resource required: %w", ErrInvalid)
	}
	if command.CallSeq < 0 {
		return fmt.Errorf("connector dispatch call_seq %d negative: %w", command.CallSeq, ErrInvalid)
	}
	return nil
}

type DispatchResult struct {
	CallID   string
	Decision ConnectorCallDecision
	Status   int
	Body     map[string]any
}

func ConnectorCallID(runID string, action string, sequence int) string {
	return runID + "#" + action + "#" + strconv.Itoa(sequence)
}

// CredentialAAD binds ciphertext to one exact workspace and connector. The
// NUL separators make the encoding injective because canonical identifiers
// cannot contain NUL.
func CredentialAAD(workspaceKey string, connectorID string) []byte {
	aad := make([]byte, 0, len("loom-connector-credential")+len(workspaceKey)+len(connectorID)+2)
	aad = append(aad, "loom-connector-credential"...)
	aad = append(aad, 0)
	aad = append(aad, workspaceKey...)
	aad = append(aad, 0)
	aad = append(aad, connectorID...)
	return aad
}

// ProviderCall is an owner-private egress port shape. Credential exists only
// for the duration of this in-process call and never appears in the public
// Dispatcher command/result API.
type ProviderCall struct {
	Action         string
	Resource       string
	Args           map[string]any
	Preconditions  DispatchPreconditions
	IdempotencyKey string
	Credential     string
}

type ProviderResult struct {
	Status   int
	Body     map[string]any
	Decision ConnectorCallDecision
}

type ConnectorSourceKind string

const (
	ConnectorSourceGitHub   ConnectorSourceKind = "github"
	ConnectorSourceSlack    ConnectorSourceKind = "slack"
	ConnectorSourceDatadog  ConnectorSourceKind = "datadog"
	ConnectorSourceInternal ConnectorSourceKind = "internal"
)

func (kind ConnectorSourceKind) Valid() bool {
	switch kind {
	case ConnectorSourceGitHub, ConnectorSourceSlack, ConnectorSourceDatadog, ConnectorSourceInternal:
		return true
	default:
		return false
	}
}

type ConnectorStatus string

const (
	ConnectorStatusActive   ConnectorStatus = "active"
	ConnectorStatusDisabled ConnectorStatus = "disabled"
)

func (status ConnectorStatus) Valid() bool {
	return status == ConnectorStatusActive || status == ConnectorStatusDisabled
}

// Connector is a redacted Connector definition projection. Credential and
// signing-secret fields do not exist on the public model, so an adapter cannot
// accidentally expose them through JSON, logs, or UI responses.
type Connector struct {
	WorkspaceKey             string              `json:"workspace_key"`
	ConnectorID              string              `json:"connector_id"`
	SourceKind               ConnectorSourceKind `json:"source_kind"`
	DisplayName              string              `json:"display_name,omitempty"`
	InboundEndpointPath      string              `json:"inbound_endpoint_path,omitempty"`
	PreviousSecretValidUntil *time.Time          `json:"previous_secret_valid_until,omitempty"`
	Status                   ConnectorStatus     `json:"status"`
	CreatedBy                string              `json:"created_by,omitempty"`
	CreatedAt                time.Time           `json:"created_at"`
	UpdatedAt                time.Time           `json:"updated_at"`
	RotatedAt                *time.Time          `json:"rotated_at,omitempty"`
}

type CreateConnectorCommand struct {
	WorkspaceKey        string
	ConnectorID         string
	SourceKind          ConnectorSourceKind
	DisplayName         string
	InboundEndpointPath string
	InboundSecret       string
	// OutboundCredentialSealed is opaque ciphertext produced by the
	// Connectors vault seam. Persistence never receives plaintext.
	OutboundCredentialSealed []byte
	Status                   ConnectorStatus
	CreatedBy                string
}

type GetConnectorQuery struct {
	WorkspaceKey string
	ConnectorID  string
}

type ConnectorFilter struct {
	SourceKind ConnectorSourceKind
	Status     ConnectorStatus
	Limit      int
}

type ListConnectorsQuery struct {
	WorkspaceKey string
	Filter       ConnectorFilter
}

type RotateConnectorCommand struct {
	WorkspaceKey     string
	ConnectorID      string
	NewInboundSecret string
	// NewCredential is plaintext accepted only at the owner boundary. The
	// service wipes this buffer after sealing and persistence sees ciphertext.
	NewCredential     []byte
	InboundWindow     time.Duration
	ExpectedUpdatedAt time.Time
}

// SynchronizeConnectorCredentialCommand asks Connectors to compare a desired
// credential with the currently sealed value and atomically rotate only when
// they differ. DesiredCredential is wiped before the command returns.
type SynchronizeConnectorCredentialCommand struct {
	WorkspaceKey      string
	ConnectorID       string
	DesiredCredential []byte
}

type CreateGrantCommand = EnsureGrantCommand

type RevokeGrantCommand struct {
	WorkspaceKey string
	GrantID      string
}

type ListGrantsQuery struct {
	WorkspaceKey string
	BindingID    string
	ConnectorID  string
}

type ConnectorCallDecision string

const (
	ConnectorCallGranted              ConnectorCallDecision = "granted"
	ConnectorCallDenied               ConnectorCallDecision = "denied"
	ConnectorCallStaleSubject         ConnectorCallDecision = "stale_subject"
	ConnectorCallPreconditionRequired ConnectorCallDecision = "precondition_required"
	ConnectorCallUpstreamError        ConnectorCallDecision = "upstream_error"
)

func (decision ConnectorCallDecision) Valid() bool {
	switch decision {
	case ConnectorCallGranted, ConnectorCallDenied, ConnectorCallStaleSubject,
		ConnectorCallPreconditionRequired, ConnectorCallUpstreamError:
		return true
	default:
		return false
	}
}

type ConnectorCallRecord struct {
	WorkspaceKey     string                `json:"workspace_key"`
	CallID           string                `json:"call_id"`
	Seq              int                   `json:"seq"`
	RunID            string                `json:"run_id"`
	BindingID        string                `json:"binding_id"`
	ConnectorID      string                `json:"connector_id"`
	SourceKind       ConnectorSourceKind   `json:"source_kind"`
	Action           string                `json:"action"`
	Resource         string                `json:"resource,omitempty"`
	Decision         ConnectorCallDecision `json:"decision"`
	UpstreamStatus   int                   `json:"upstream_status,omitempty"`
	ErrorClass       string                `json:"error_class,omitempty"`
	SanitizedSummary string                `json:"sanitized_summary,omitempty"`
	OccurredAt       time.Time             `json:"occurred_at"`
}

type ConnectorCallFilter struct {
	Decision ConnectorCallDecision
	Limit    int
}

type ListCallsQuery struct {
	WorkspaceKey string
	RunID        string
	BindingID    string
	Filter       ConnectorCallFilter
}

// ConnectorGrant is the Connectors-owned, transport-neutral projection of
// one active or historical binding-scoped egress grant. The immutable tuple
// (GrantID, ConnectorID, BindingID, Action, ResourcePattern) is the durable
// idempotency identity used by EnsureGrant.
type ConnectorGrant struct {
	WorkspaceKey    string     `json:"workspace_key"`
	GrantID         string     `json:"grant_id"`
	ConnectorID     string     `json:"connector_id"`
	BindingID       string     `json:"binding_id"`
	Action          string     `json:"action"`
	ResourcePattern string     `json:"resource_pattern"`
	CreatedAt       time.Time  `json:"created_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

// EnsureGrantCommand requests one exact binding-scoped grant. GrantID is the
// durable replay key; no caller-chosen authority or audit actor is accepted.
type EnsureGrantCommand struct {
	WorkspaceKey    string `json:"workspace_key"`
	GrantID         string `json:"grant_id"`
	ConnectorID     string `json:"connector_id"`
	BindingID       string `json:"binding_id"`
	Action          string `json:"action"`
	ResourcePattern string `json:"resource_pattern"`
}

func cloneConnectorGrant(in *ConnectorGrant) *ConnectorGrant {
	if in == nil {
		return nil
	}
	out := *in
	if in.RevokedAt != nil {
		value := *in.RevokedAt
		out.RevokedAt = &value
	}
	return &out
}

// GitReadOperation is the closed set of credential-brokered Git reads.
// Clone materializes an admitted repository. Fetch-ref updates one exact
// Source-Control-owned local ref from one exact remote ref; it cannot fetch an
// arbitrary refspec or write outside refs/loom.
type GitReadOperation string

const (
	GitReadClone    GitReadOperation = "clone"
	GitReadFetchRef GitReadOperation = "fetch-ref"
)

// GitReadCommand authorizes no standing credential. It identifies one exact
// repository, operation, workspace root, and checkout target. RemoteURL must
// be token-free; URL userinfo, query strings, and fragments are rejected for
// every scheme. Canonical SCP-style remotes may carry a username, not a
// password or other embedded authority.
type GitReadCommand struct {
	WorkspaceKey   string           `json:"workspace_key"`
	OperationID    string           `json:"operation_id"`
	RepositoryRef  string           `json:"repository_ref"`
	Operation      GitReadOperation `json:"operation"`
	RemoteURL      string           `json:"remote_url"`
	WorkspacePath  string           `json:"workspace_path"`
	TargetPath     string           `json:"target_path"`
	RemoteName     string           `json:"remote_name,omitempty"`
	SourceRef      string           `json:"source_ref,omitempty"`
	DestinationRef string           `json:"destination_ref,omitempty"`
}

// GitReadReceipt is deliberately credential-free. It lets Source Control
// validate that the broker executed the same bounded request without learning
// how the provider authenticated it.
type GitReadReceipt struct {
	WorkspaceKey   string           `json:"workspace_key"`
	OperationID    string           `json:"operation_id"`
	RepositoryRef  string           `json:"repository_ref"`
	Operation      GitReadOperation `json:"operation"`
	TargetPath     string           `json:"target_path"`
	RemoteName     string           `json:"remote_name,omitempty"`
	SourceRef      string           `json:"source_ref,omitempty"`
	DestinationRef string           `json:"destination_ref,omitempty"`
}
