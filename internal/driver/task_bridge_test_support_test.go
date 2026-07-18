//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const testTaskRunAPIURL = "http://127.0.0.1:8080"

var bridgeArtifactTestIssuer = authority.NewIssuer()

// testArtifactsAPI keeps memstore-based driver tests focused on bridge
// behavior without restoring a Store.Artifacts fallback to production code.
func testArtifactsAPI(st store.Store) artifactsmodule.API {
	if st == nil {
		return nil
	}
	admission, err := bridgeArtifactTestIssuer.NewAdmission(artifactsmodule.OperationRules()...)
	if err != nil {
		panic(err)
	}
	service, err := artifactsmodule.New(bridgeArtifactTestAdapter{store: st.Artifacts()}, admission)
	if err != nil {
		panic(err)
	}
	return service
}

type bridgeArtifactTestAdapter struct {
	store store.ArtifactStore
}

var _ artifactsmodule.Store = bridgeArtifactTestAdapter{}

func (a bridgeArtifactTestAdapter) Create(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.CreateCommand) (*artifactsmodule.Artifact, error) {
	if a.store == nil {
		return nil, artifactsmodule.ErrUnavailable
	}
	artifact, err := a.store.Create(ctx, store.ArtifactCreate{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, SessionID: command.SessionID,
		TaskID: command.TaskID, OwnerType: string(artifactsmodule.OwnerTaskRun), OwnerID: owner.TaskRunID,
		Type: command.Type, URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, Checksum: command.Checksum, ContentHash: command.ContentHash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
		DurableStatus: string(artifactsmodule.StatusDeclared), Metadata: command.Metadata,
	})
	return bridgeArtifactModuleFromDomain(artifact), bridgeArtifactStoreError(err)
}

func (a bridgeArtifactTestAdapter) Upload(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.UploadCommand) (*artifactsmodule.Artifact, error) {
	if _, err := a.ownedArtifact(ctx, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	artifact, err := a.store.UploadContent(ctx, owner.WorkspaceKey, command.ArtifactID, store.ArtifactContentUpload{
		Body: bytes.NewReader(command.Content), MIMEType: command.MIMEType,
	})
	return bridgeArtifactModuleFromDomain(artifact), bridgeArtifactStoreError(err)
}

func (a bridgeArtifactTestAdapter) Finalize(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.FinalizeCommand) (*artifactsmodule.Artifact, error) {
	if _, err := a.ownedArtifact(ctx, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	artifact, err := a.store.Finalize(ctx, owner.WorkspaceKey, command.ArtifactID, store.ArtifactFinalize{
		URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType, SizeBytes: command.SizeBytes,
		Checksum: command.Checksum, ContentHash: command.ContentHash, Visibility: command.Visibility,
		RedactionStatus: command.RedactionStatus, Metadata: command.Metadata,
	})
	return bridgeArtifactModuleFromDomain(artifact), bridgeArtifactStoreError(err)
}

func (a bridgeArtifactTestAdapter) Reference(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.ReferenceCommand) (artifactsmodule.ReferenceResult, error) {
	artifact, err := a.ownedArtifact(ctx, owner, command.ArtifactID)
	if err != nil {
		return artifactsmodule.ReferenceResult{}, err
	}
	if artifact.DurableStatus != string(artifactsmodule.StatusFinalized) || artifact.FinalizedAt == nil {
		return artifactsmodule.ReferenceResult{}, artifactsmodule.ErrInvalidTransition
	}
	createdAt := artifact.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return artifactsmodule.ReferenceResult{
		Artifact: bridgeArtifactModuleFromDomain(artifact),
		Reference: &artifactsmodule.ArtifactReference{
			WorkspaceKey: owner.WorkspaceKey,
			ReferenceID:  fmt.Sprintf("test-reference:%s:%s:%s", command.ArtifactID, command.Kind, command.TargetRef),
			ArtifactID:   command.ArtifactID,
			OwnerType:    artifactsmodule.OwnerTaskRun,
			OwnerID:      owner.TaskRunID,
			Kind:         command.Kind,
			TargetRef:    command.TargetRef,
			CreatedAt:    createdAt,
		},
	}, nil
}

func (a bridgeArtifactTestAdapter) Get(ctx context.Context, owner artifactsmodule.ExecutionOwner, query artifactsmodule.GetQuery) (*artifactsmodule.Artifact, error) {
	artifact, err := a.ownedArtifact(ctx, owner, query.ArtifactID)
	return bridgeArtifactModuleFromDomain(artifact), err
}

func (a bridgeArtifactTestAdapter) List(ctx context.Context, owner artifactsmodule.ExecutionOwner, filter artifactsmodule.ListFilter) ([]*artifactsmodule.Artifact, error) {
	if a.store == nil {
		return nil, artifactsmodule.ErrUnavailable
	}
	values, err := a.store.List(ctx, owner.WorkspaceKey, store.ArtifactFilter{
		OwnerType: string(artifactsmodule.OwnerTaskRun), OwnerID: owner.TaskRunID,
		Type: filter.Type, Status: string(filter.DurableStatus), Limit: filter.Limit,
	})
	if err != nil {
		return nil, bridgeArtifactStoreError(err)
	}
	out := make([]*artifactsmodule.Artifact, 0, len(values))
	for _, artifact := range values {
		out = append(out, bridgeArtifactModuleFromDomain(artifact))
	}
	return out, nil
}

func (a bridgeArtifactTestAdapter) ownedArtifact(ctx context.Context, owner artifactsmodule.ExecutionOwner, artifactID string) (*domain.Artifact, error) {
	if a.store == nil {
		return nil, artifactsmodule.ErrUnavailable
	}
	artifact, err := a.store.Get(ctx, owner.WorkspaceKey, artifactID)
	if err != nil {
		return nil, bridgeArtifactStoreError(err)
	}
	if artifact.OwnerType != string(artifactsmodule.OwnerTaskRun) || artifact.OwnerID != owner.TaskRunID {
		return nil, errors.Join(artifactsmodule.ErrNotFound, domain.ErrNotFound)
	}
	return artifact, nil
}

func bridgeArtifactModuleFromDomain(artifact *domain.Artifact) *artifactsmodule.Artifact {
	if artifact == nil {
		return nil
	}
	return &artifactsmodule.Artifact{
		WorkspaceKey: artifact.WorkspaceKey, ArtifactID: artifact.ArtifactID, SessionID: artifact.SessionID,
		TaskID: artifact.TaskID, OwnerType: artifactsmodule.OwnerType(artifact.OwnerType), OwnerID: artifact.OwnerID,
		Type: artifact.Type, URI: artifact.URI, Summary: artifact.Summary, MIMEType: artifact.MIMEType,
		SizeBytes: artifact.SizeBytes, Checksum: artifact.Checksum, ContentHash: artifact.ContentHash,
		Visibility: artifact.Visibility, RedactionStatus: artifact.RedactionStatus,
		DurableStatus: artifactsmodule.DurableStatus(artifact.DurableStatus), Metadata: artifact.Metadata,
		FinalizedAt: artifact.FinalizedAt, CreatedAt: artifact.CreatedAt, UpdatedAt: artifact.UpdatedAt,
	}
}

func bridgeArtifactStoreError(err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, domain.ErrNotFound):
		mapped = artifactsmodule.ErrNotFound
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		mapped = artifactsmodule.ErrAlreadyExists
	case errors.Is(err, domain.ErrNotOwner):
		mapped = artifactsmodule.ErrNotOwner
	case errors.Is(err, domain.ErrInvalidTransition):
		mapped = artifactsmodule.ErrInvalidTransition
	case errors.Is(err, domain.ErrInvalid):
		mapped = artifactsmodule.ErrInvalid
	default:
		mapped = artifactsmodule.ErrUnavailable
	}
	return errors.Join(mapped, err)
}
