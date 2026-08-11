package connectors

import (
	"context"
	"reflect"

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
	BindingLifecycle
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

// BindingLifecycle owns Connector concerns attached to one Automation binding.
// Secret material and grant cleanup stay behind this interface; callers never
// receive the persistence ports or enumerate grant records themselves.
type BindingLifecycle interface {
	ConfigureBindingSecret(context.Context, ConfigureBindingSecretCommand) error
	RevokeBindingGrants(context.Context, BindingGrantCleanupCommand) (int, error)
}

// Dispatcher is the credential-free public egress API. Implementations own
// grant evaluation, just-in-time credential use, provider dispatch, and audit.
type Dispatcher interface {
	Dispatch(context.Context, DispatchCommand) (DispatchResult, error)
}

// DispatcherAvailable rejects both a nil interface and an interface holding a
// typed nil implementation. Inbound adapters use it to preserve fail-closed
// behavior while the legacy pointer implementation sits behind this port.
func DispatcherAvailable(dispatcher Dispatcher) bool {
	if dispatcher == nil {
		return false
	}
	value := reflect.ValueOf(dispatcher)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}
