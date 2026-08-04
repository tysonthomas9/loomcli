// Package connectorsrotation adapts Connector secret rotation and audit
// persistence to the owner's narrow secret-lifecycle port.
package connectorsrotation

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Adapter struct {
	connectors store.ConnectorStore
	audit      store.ConnectorAuditStore
}

var _ connectorsmodule.SecretLifecycleStore = (*Adapter)(nil)

func New(connectors store.ConnectorStore, audit store.ConnectorAuditStore) (*Adapter, error) {
	if connectors == nil {
		return nil, connectorsmodule.ErrUnavailable
	}
	return &Adapter{connectors: connectors, audit: audit}, nil
}

func (adapter *Adapter) GetConnectorRecord(
	ctx context.Context,
	workspace,
	connectorID string,
) (*connectorsmodule.Connector, error) {
	value, err := adapter.connectors.Get(ctx, workspace, connectorID)
	return connectorProjection(value), translateError(err)
}

func (adapter *Adapter) ResolveCurrentInboundSecretRecord(
	ctx context.Context,
	workspace,
	connectorID string,
) (string, error) {
	value, err := adapter.connectors.ResolveInboundSecret(ctx, workspace, connectorID)
	if err != nil {
		return "", translateError(err)
	}
	if value == nil {
		return "", connectorsmodule.ErrInvalidPersistedState
	}
	return value.Current, nil
}

func (adapter *Adapter) ResolveOutboundCredentialSealedRecord(
	ctx context.Context,
	workspace,
	connectorID string,
) ([]byte, error) {
	value, err := adapter.connectors.ResolveOutboundCredentialSealed(ctx, workspace, connectorID)
	return append([]byte(nil), value...), translateError(err)
}

func (adapter *Adapter) RotateConnectorSecretsRecord(
	ctx context.Context,
	workspace,
	connectorID string,
	mutation connectorsmodule.RotateConnectorSecretsMutation,
) (*connectorsmodule.Connector, error) {
	value, err := adapter.connectors.RotateSecrets(ctx, workspace, connectorID, store.ConnectorSecretRotation{
		NewInboundSecret:            mutation.NewInboundSecret,
		PreviousSecretValidUntil:    mutation.PreviousSecretValidUntil,
		ExpectedUpdatedAt:           mutation.ExpectedUpdatedAt,
		NewOutboundCredentialSealed: append([]byte(nil), mutation.NewOutboundCredentialSealed...),
	})
	return connectorProjection(value), translateError(err)
}

func (adapter *Adapter) AppendConnectorCallRecord(
	ctx context.Context,
	record *connectorsmodule.ConnectorCallRecord,
) error {
	if record == nil {
		return connectorsmodule.ErrInvalid
	}
	if adapter.audit == nil {
		return nil
	}
	return translateError(adapter.audit.Append(ctx, &domain.ConnectorCallRecord{
		WorkspaceKey: record.WorkspaceKey, CallID: record.CallID, Seq: record.Seq,
		RunID: record.RunID, BindingID: record.BindingID, ConnectorID: record.ConnectorID,
		SourceKind: domain.ConnectorSourceKind(record.SourceKind), Action: record.Action,
		Resource: record.Resource, Decision: domain.ConnectorCallDecision(record.Decision),
		UpstreamStatus: record.UpstreamStatus, ErrorClass: record.ErrorClass,
		SanitizedSummary: record.SanitizedSummary, OccurredAt: record.OccurredAt,
	}))
}

func connectorProjection(value *domain.Connector) *connectorsmodule.Connector {
	if value == nil {
		return nil
	}
	return &connectorsmodule.Connector{
		WorkspaceKey: value.WorkspaceKey, ConnectorID: value.ConnectorID,
		SourceKind: connectorsmodule.ConnectorSourceKind(value.SourceKind), DisplayName: value.DisplayName,
		InboundEndpointPath:      value.InboundEndpointPath,
		PreviousSecretValidUntil: cloneTime(value.PreviousSecretValidUntil),
		Status:                   connectorsmodule.ConnectorStatus(value.Status),
		CreatedBy:                value.CreatedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		RotatedAt: cloneTime(value.RotatedAt),
	}
}

func translateError(err error) error {
	if err == nil {
		return nil
	}
	var owner error
	switch {
	case errors.Is(err, domain.ErrConnectorNotFound), errors.Is(err, domain.ErrNotFound):
		owner = connectorsmodule.ErrNotFound
	case errors.Is(err, domain.ErrAlreadyExists):
		owner = connectorsmodule.ErrAlreadyExists
	case errors.Is(err, domain.ErrConflict):
		owner = connectorsmodule.ErrConflict
	case errors.Is(err, domain.ErrInvalid):
		owner = connectorsmodule.ErrInvalid
	default:
		return err
	}
	return errors.Join(owner, err)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
