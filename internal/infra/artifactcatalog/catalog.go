// Package artifactcatalog adapts legacy Artifact metadata/content stores to
// the Artifacts-owned general query port. It is the only compatibility layer
// used by WebUI projections; callers never receive a legacy domain Artifact.
package artifactcatalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type Catalog struct {
	metadata store.ArtifactStore
	content  store.ArtifactContentReader
}

// StoreProvider is the narrow compatibility surface needed to locate the
// legacy artifact sub-store. It deliberately does not accept store.Store, so
// adding this adapter cannot introduce another composite-store dependency.
type StoreProvider interface {
	Artifacts() store.ArtifactStore
}

var _ artifacts.QueryStore = (*Catalog)(nil)

func New(metadata store.ArtifactStore) (*Catalog, error) {
	if metadata == nil {
		return nil, artifacts.ErrUnavailable
	}
	content, _ := metadata.(store.ArtifactContentReader)
	return &Catalog{metadata: metadata, content: content}, nil
}

// FromProvider is the bounded compatibility composition seam for callers that
// can expose the legacy artifact sub-store. Artifact query consumers use the
// owner API and never select that sub-store themselves.
func FromProvider(provider StoreProvider) (*Catalog, error) {
	if provider == nil {
		return nil, artifacts.ErrUnavailable
	}
	return New(provider.Artifacts())
}

func (catalog *Catalog) GetArtifactRecord(
	ctx context.Context,
	workspace, artifactID string,
) (*artifacts.Artifact, error) {
	if catalog == nil || catalog.metadata == nil {
		return nil, artifacts.ErrUnavailable
	}
	value, err := catalog.metadata.Get(ctx, workspace, artifactID)
	return artifactProjection(value), translateError(err)
}

func (catalog *Catalog) ListArtifactRecords(
	ctx context.Context,
	workspace string,
	filter artifacts.SearchFilter,
) ([]*artifacts.Artifact, error) {
	if catalog == nil || catalog.metadata == nil {
		return nil, artifacts.ErrUnavailable
	}
	values, err := catalog.metadata.List(ctx, workspace, store.ArtifactFilter{
		AgentID: filter.AgentID, SessionID: filter.SessionID, TaskID: filter.TaskID,
		OwnerType: string(filter.OwnerType), OwnerID: filter.OwnerID, Type: filter.Type,
		Status: string(filter.DurableStatus), Limit: filter.Limit,
	})
	if err != nil {
		return nil, translateError(err)
	}
	result := make([]*artifacts.Artifact, len(values))
	for index, value := range values {
		result[index] = artifactProjection(value)
	}
	return result, nil
}

func (catalog *Catalog) ReadArtifactContent(
	ctx context.Context,
	workspace, artifactID string,
) ([]byte, error) {
	if catalog == nil || catalog.metadata == nil {
		return nil, artifacts.ErrUnavailable
	}
	if catalog.content == nil {
		return nil, artifacts.ErrContentUnavailable
	}
	content, err := catalog.content.ReadContent(ctx, workspace, artifactID)
	if err != nil {
		return nil, translateContentError(err)
	}
	return append([]byte(nil), content...), nil
}

func artifactProjection(value *domain.Artifact) *artifacts.Artifact {
	if value == nil {
		return nil
	}
	return &artifacts.Artifact{
		WorkspaceKey: value.WorkspaceKey, ArtifactID: value.ArtifactID,
		AgentID: value.AgentID, SessionID: value.SessionID, TaskID: value.TaskID,
		OwnerType: artifacts.OwnerType(value.OwnerType), OwnerID: value.OwnerID,
		Type: value.Type, URI: value.URI, Summary: value.Summary, MIMEType: value.MIMEType,
		SizeBytes: value.SizeBytes, Checksum: value.Checksum, ContentHash: value.ContentHash,
		Visibility: value.Visibility, RedactionStatus: value.RedactionStatus,
		DurableStatus: artifacts.DurableStatus(value.DurableStatus),
		Metadata:      cloneMetadata(value.Metadata), FinalizedAt: cloneTime(value.FinalizedAt),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func translateError(err error) error {
	if err == nil {
		return nil
	}
	var owner error
	switch {
	case errors.Is(err, domain.ErrNotFound):
		owner = artifacts.ErrNotFound
	case errors.Is(err, domain.ErrInvalid):
		owner = artifacts.ErrInvalid
	case errors.Is(err, domain.ErrUnavailable), errors.Is(err, domain.ErrRateLimited):
		owner = artifacts.ErrUnavailable
	default:
		owner = artifacts.ErrUnavailable
	}
	return fmt.Errorf("artifact catalog: %w", errors.Join(owner, err))
}

func translateContentError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrArtifactContentUnavailable) {
		return fmt.Errorf("artifact content: %w", errors.Join(artifacts.ErrContentUnavailable, err))
	}
	return translateError(err)
}

func cloneMetadata(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
