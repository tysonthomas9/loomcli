package cmdstore

// Traced wrappers for the connector control-plane stores (connectors,
// grants, call-audit journal), mirroring internal/store/connector_store.go.
// Shared span helpers live in store_tracing.go.
//
// Redaction contract: span attributes carry IDs and low-cardinality tags
// only. In particular the ResolveInboundSecret /
// ResolveOutboundCredentialSealed results (plaintext inbound secrets,
// sealed credential ciphertext) are NEVER recorded on spans — only the
// workspace + connector identifiers of the lookup.

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func (t *tracedStore) Connectors() store.ConnectorStore {
	return t.connectors
}

func (t *tracedStore) ConnectorGrants() store.ConnectorGrantStore {
	return t.connectorGrants
}

func (t *tracedStore) ConnectorCalls() store.ConnectorAuditStore {
	return t.connectorCalls
}

// --- ConnectorStore ---

type tracedConnectorStore struct{ inner store.ConnectorStore }

func (t *tracedConnectorStore) Create(ctx context.Context, in store.ConnectorCreate) (*domain.Connector, error) {
	return traced(ctx, "Connectors", "Create", func(ctx context.Context) (*domain.Connector, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.connector", in.ConnectorID),
		attribute.String("loom.connector_source", string(in.SourceKind)),
	)
}

func (t *tracedConnectorStore) Get(ctx context.Context, ws, connectorID string) (*domain.Connector, error) {
	return traced(ctx, "Connectors", "Get", func(ctx context.Context) (*domain.Connector, error) {
		return t.inner.Get(ctx, ws, connectorID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.connector", connectorID),
	)
}

func (t *tracedConnectorStore) List(ctx context.Context, ws string, filter store.ConnectorFilter) ([]*domain.Connector, error) {
	return tracedList(ctx, "Connectors", "List", func(ctx context.Context) ([]*domain.Connector, error) {
		return t.inner.List(ctx, ws, filter)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedConnectorStore) ResolveInboundSecret(ctx context.Context, ws, connectorID string) (*store.ConnectorInboundSecrets, error) {
	return traced(ctx, "Connectors", "ResolveInboundSecret", func(ctx context.Context) (*store.ConnectorInboundSecrets, error) {
		return t.inner.ResolveInboundSecret(ctx, ws, connectorID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.connector", connectorID),
	)
}

func (t *tracedConnectorStore) ResolveOutboundCredentialSealed(ctx context.Context, ws, connectorID string) ([]byte, error) {
	return traced(ctx, "Connectors", "ResolveOutboundCredentialSealed", func(ctx context.Context) ([]byte, error) {
		return t.inner.ResolveOutboundCredentialSealed(ctx, ws, connectorID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.connector", connectorID),
	)
}

func (t *tracedConnectorStore) RotateSecrets(ctx context.Context, ws, connectorID string, in store.ConnectorSecretRotation) (*domain.Connector, error) {
	return traced(ctx, "Connectors", "RotateSecrets", func(ctx context.Context) (*domain.Connector, error) {
		return t.inner.RotateSecrets(ctx, ws, connectorID, in)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.connector", connectorID),
	)
}

// --- ConnectorGrantStore ---

type tracedConnectorGrantStore struct{ inner store.ConnectorGrantStore }

func (t *tracedConnectorGrantStore) Create(ctx context.Context, in store.ConnectorGrantCreate) (*domain.ConnectorGrant, error) {
	return traced(ctx, "ConnectorGrants", "Create", func(ctx context.Context) (*domain.ConnectorGrant, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.connector", in.ConnectorID),
		attribute.String("loom.binding", in.BindingID),
		attribute.String("loom.connector_action", in.Action),
	)
}

func (t *tracedConnectorGrantStore) Revoke(ctx context.Context, ws, grantID string) error {
	_, err := traced(ctx, "ConnectorGrants", "Revoke", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, t.inner.Revoke(ctx, ws, grantID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.grant", grantID),
	)
	return err
}

func (t *tracedConnectorGrantStore) ListByBinding(ctx context.Context, ws, bindingID string) ([]*domain.ConnectorGrant, error) {
	return tracedList(ctx, "ConnectorGrants", "ListByBinding", func(ctx context.Context) ([]*domain.ConnectorGrant, error) {
		return t.inner.ListByBinding(ctx, ws, bindingID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
}

func (t *tracedConnectorGrantStore) ListByConnector(ctx context.Context, ws, connectorID string) ([]*domain.ConnectorGrant, error) {
	return tracedList(ctx, "ConnectorGrants", "ListByConnector", func(ctx context.Context) ([]*domain.ConnectorGrant, error) {
		return t.inner.ListByConnector(ctx, ws, connectorID)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.connector", connectorID),
	)
}

// --- ConnectorAuditStore ---

type tracedConnectorAuditStore struct{ inner store.ConnectorAuditStore }

func (t *tracedConnectorAuditStore) Append(ctx context.Context, rec *domain.ConnectorCallRecord) error {
	_, err := traced(ctx, "ConnectorCalls", "Append", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, t.inner.Append(ctx, rec)
	},
		attribute.String("loom.workspace", rec.WorkspaceKey),
		attribute.String("loom.connector", rec.ConnectorID),
		attribute.String("loom.connector_action", rec.Action),
		attribute.String("loom.connector_decision", string(rec.Decision)),
	)
	return err
}

func (t *tracedConnectorAuditStore) ListByRun(ctx context.Context, ws, runID string, filter store.ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	return tracedList(ctx, "ConnectorCalls", "ListByRun", func(ctx context.Context) ([]*domain.ConnectorCallRecord, error) {
		return t.inner.ListByRun(ctx, ws, runID, filter)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.run", runID),
	)
}

func (t *tracedConnectorAuditStore) ListByBinding(ctx context.Context, ws, bindingID string, filter store.ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	return tracedList(ctx, "ConnectorCalls", "ListByBinding", func(ctx context.Context) ([]*domain.ConnectorCallRecord, error) {
		return t.inner.ListByBinding(ctx, ws, bindingID, filter)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.binding", bindingID),
	)
}
