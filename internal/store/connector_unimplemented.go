package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Fail-closed placeholders for Store implementations that have not wired
// connector persistence yet (memstore lands in CV2, fleet-db in CV3). Every
// method returns an error wrapping errors.ErrUnsupported so callers can
// detect the gap via errors.Is without panicking on a nil sub-store.

func errConnectorUnsupported(backend, op string) error {
	return fmt.Errorf("%s: connector store %s: %w", backend, op, errors.ErrUnsupported)
}

// UnimplementedConnectorStore is a fail-closed ConnectorStore placeholder.
type UnimplementedConnectorStore struct {
	// Backend names the implementation for error messages.
	Backend string
}

var _ ConnectorStore = UnimplementedConnectorStore{}

func (u UnimplementedConnectorStore) Create(context.Context, ConnectorCreate) (*domain.Connector, error) {
	return nil, errConnectorUnsupported(u.Backend, "create")
}

func (u UnimplementedConnectorStore) Get(context.Context, string, string) (*domain.Connector, error) {
	return nil, errConnectorUnsupported(u.Backend, "get")
}

func (u UnimplementedConnectorStore) List(context.Context, string, ConnectorFilter) ([]*domain.Connector, error) {
	return nil, errConnectorUnsupported(u.Backend, "list")
}

func (u UnimplementedConnectorStore) ResolveInboundSecret(context.Context, string, string) (*ConnectorInboundSecrets, error) {
	return nil, errConnectorUnsupported(u.Backend, "resolve inbound secret")
}

func (u UnimplementedConnectorStore) ResolveOutboundCredentialSealed(context.Context, string, string) ([]byte, error) {
	return nil, errConnectorUnsupported(u.Backend, "resolve outbound credential")
}

func (u UnimplementedConnectorStore) RotateSecrets(context.Context, string, string, ConnectorSecretRotation) (*domain.Connector, error) {
	return nil, errConnectorUnsupported(u.Backend, "rotate secrets")
}

// UnimplementedConnectorGrantStore is a fail-closed ConnectorGrantStore
// placeholder.
type UnimplementedConnectorGrantStore struct {
	Backend string
}

var _ ConnectorGrantStore = UnimplementedConnectorGrantStore{}

func (u UnimplementedConnectorGrantStore) Create(context.Context, ConnectorGrantCreate) (*domain.ConnectorGrant, error) {
	return nil, errConnectorUnsupported(u.Backend, "grant create")
}

func (u UnimplementedConnectorGrantStore) Revoke(context.Context, string, string) error {
	return errConnectorUnsupported(u.Backend, "grant revoke")
}

func (u UnimplementedConnectorGrantStore) ListByBinding(context.Context, string, string) ([]*domain.ConnectorGrant, error) {
	return nil, errConnectorUnsupported(u.Backend, "grant list by binding")
}

func (u UnimplementedConnectorGrantStore) ListByConnector(context.Context, string, string) ([]*domain.ConnectorGrant, error) {
	return nil, errConnectorUnsupported(u.Backend, "grant list by connector")
}

// UnimplementedConnectorAuditStore is a fail-closed ConnectorAuditStore
// placeholder.
type UnimplementedConnectorAuditStore struct {
	Backend string
}

var _ ConnectorAuditStore = UnimplementedConnectorAuditStore{}

func (u UnimplementedConnectorAuditStore) Append(context.Context, *domain.ConnectorCallRecord) error {
	return errConnectorUnsupported(u.Backend, "audit append")
}

func (u UnimplementedConnectorAuditStore) ListByRun(context.Context, string, string, ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	return nil, errConnectorUnsupported(u.Backend, "audit list by run")
}

func (u UnimplementedConnectorAuditStore) ListByBinding(context.Context, string, string, ConnectorCallFilter) ([]*domain.ConnectorCallRecord, error) {
	return nil, errConnectorUnsupported(u.Backend, "audit list by binding")
}
