// Package connector retains the legacy rotation entry point while callers
// migrate to the Connectors owner API. Rotation policy and direct persistence
// belong to internal/modules/connectors and internal/infra/connectorsrotation;
// this file is deliberately a behavior-free compatibility facade.
package connector

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsrotation"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	RotationAuditBindingID = connectorsmodule.RotationAuditBindingID
	RotationAuditAction    = connectorsmodule.RotationAuditAction
)

var (
	ErrRotationConflict = errors.Join(
		connectorsmodule.ErrRotationConflict,
		domain.ErrConflict,
	)
	ErrRotationSealerMissing = errors.Join(
		connectorsmodule.ErrRotationSealerMissing,
		domain.ErrInvalid,
	)
)

// RotateRequest is the legacy transport shape for one credential-rotation
// ceremony. NewCredential is plaintext and is wiped before Rotate returns.
type RotateRequest struct {
	WorkspaceKey      string
	ConnectorID       string
	NewInboundSecret  string
	NewCredential     []byte
	InboundWindow     time.Duration
	ExpectedUpdatedAt time.Time
	Now               func() time.Time
}

// Rotate delegates the complete ceremony to the Connectors owner. The
// returned domain projection remains redacted for compatibility with callers
// that have not yet switched to connectors.Management.
func Rotate(
	ctx context.Context,
	connectors store.ConnectorStore,
	audit store.ConnectorAuditStore,
	sealer Sealer,
	req RotateRequest,
) (*domain.Connector, error) {
	defer zeroBytes(req.NewCredential)

	adapter, err := connectorsrotation.New(connectors, audit)
	if err != nil {
		return nil, translateRotationError(err)
	}
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	service, err := connectorsmodule.NewSecretLifecycle(adapter, sealer, now)
	if err != nil {
		return nil, translateRotationError(err)
	}
	rotated, err := service.RotateConnector(ctx, connectorsmodule.RotateConnectorCommand{
		WorkspaceKey:      req.WorkspaceKey,
		ConnectorID:       req.ConnectorID,
		NewInboundSecret:  req.NewInboundSecret,
		NewCredential:     req.NewCredential,
		InboundWindow:     req.InboundWindow,
		ExpectedUpdatedAt: req.ExpectedUpdatedAt,
	})
	return legacyConnectorProjection(rotated), translateRotationError(err)
}

func translateRotationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, connectorsmodule.ErrRotationConflict):
		return errors.Join(ErrRotationConflict, err)
	case errors.Is(err, connectorsmodule.ErrRotationSealerMissing):
		return errors.Join(ErrRotationSealerMissing, err)
	case errors.Is(err, connectorsmodule.ErrInvalid):
		return errors.Join(domain.ErrInvalid, err)
	default:
		return err
	}
}

func legacyConnectorProjection(value *connectorsmodule.Connector) *domain.Connector {
	if value == nil {
		return nil
	}
	return &domain.Connector{
		WorkspaceKey:             value.WorkspaceKey,
		ConnectorID:              value.ConnectorID,
		SourceKind:               domain.ConnectorSourceKind(value.SourceKind),
		DisplayName:              value.DisplayName,
		InboundEndpointPath:      value.InboundEndpointPath,
		PreviousSecretValidUntil: cloneRotationTime(value.PreviousSecretValidUntil),
		Status:                   domain.ConnectorStatus(value.Status),
		CreatedBy:                value.CreatedBy,
		CreatedAt:                value.CreatedAt,
		UpdatedAt:                value.UpdatedAt,
		RotatedAt:                cloneRotationTime(value.RotatedAt),
	}
}

func cloneRotationTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
