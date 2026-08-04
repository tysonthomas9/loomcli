package connectors

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	// ActionExecuteGitRead is the exact internal action used by Source Control
	// to ask Connectors to perform one bounded, credential-brokered Git read.
	ActionExecuteGitRead authority.Action = "connectors.execute-git-read"
	// ActionEnsureGrant is the exact internal action used by the
	// AgentProvisioning process manager to recover one binding-scoped
	// Connector grant. It is deliberately system-only: callers cannot mint a
	// grant by carrying an authority value in a request payload.
	ActionEnsureGrant authority.Action = "connectors.ensure-grant"
)

// OperationRules is the complete default-deny Connectors operation registry
// for the Phase 5 seams. Only registered server components may invoke these
// commands; callers cannot submit an authority on a request wire.
func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.Allow(ActionExecuteGitRead, authority.ClassSystem),
		authority.Allow(ActionEnsureGrant, authority.ClassSystem),
	}
}

// GitReadBroker is the minimal Phase 5 Connectors public API. It deliberately
// returns a receipt instead of a credential, helper command, environment, or
// provider response. The implementation resolves any credential only while
// executing the bounded read operation.
type GitReadBroker interface {
	ExecuteGitRead(context.Context, authority.SystemAuthority, GitReadCommand) (GitReadReceipt, error)
}

// GrantCommands is the Connectors-owned binding-scoped grant command API.
// EnsureGrant is replay-safe for one exact immutable grant tuple. Reusing a
// GrantID for a different connector, binding, action, or resource fails
// closed with ErrGrantConflict.
type GrantCommands interface {
	EnsureGrant(context.Context, authority.SystemAuthority, EnsureGrantCommand) (*ConnectorGrant, error)
}

// Management is the operator-facing Connector definition, grant, and audit
// surface. Results are transport-neutral owner projections and Connector
// results never contain inbound secrets or sealed credential bytes.
type Management interface {
	CreateConnector(context.Context, CreateConnectorCommand) (*Connector, error)
	RotateConnector(context.Context, RotateConnectorCommand) (*Connector, error)
	SynchronizeConnectorCredential(context.Context, SynchronizeConnectorCredentialCommand) (*Connector, error)
	GetConnector(context.Context, GetConnectorQuery) (*Connector, error)
	ListConnectors(context.Context, ListConnectorsQuery) ([]*Connector, error)
	CreateGrant(context.Context, CreateGrantCommand) (*ConnectorGrant, error)
	RevokeGrant(context.Context, RevokeGrantCommand) error
	ListGrants(context.Context, ListGrantsQuery) ([]*ConnectorGrant, error)
	ListCalls(context.Context, ListCallsQuery) ([]*ConnectorCallRecord, error)
}
