package taskrunapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// taskRunArtifactTestAdapter preserves memstore-based handler coverage without
// restoring the legacy Store.Artifacts production fallback. Production tests
// for the real owner-fenced transport live at the app/serve capability seam.
type taskRunArtifactTestAdapter struct {
	module *Module
}

var _ artifactsmodule.Store = taskRunArtifactTestAdapter{}

func newTaskRunArtifactAPIForTest(module *Module) artifactsmodule.API {
	return taskRunArtifactTestAPI{store: taskRunArtifactTestAdapter{module: module}}
}

type taskRunArtifactTestAPI struct {
	store taskRunArtifactTestAdapter
}

var _ artifactsmodule.API = taskRunArtifactTestAPI{}

func (a taskRunArtifactTestAPI) Create(ctx context.Context, _ authority.ExecutionAuthority, owner artifactsmodule.ExecutionOwner, command artifactsmodule.CreateCommand) (*artifactsmodule.Artifact, error) {
	return a.store.Create(ctx, owner, command)
}

func (a taskRunArtifactTestAPI) Upload(ctx context.Context, _ authority.ExecutionAuthority, owner artifactsmodule.ExecutionOwner, command artifactsmodule.UploadCommand) (*artifactsmodule.Artifact, error) {
	return a.store.Upload(ctx, owner, command)
}

func (a taskRunArtifactTestAPI) Finalize(ctx context.Context, _ authority.ExecutionAuthority, owner artifactsmodule.ExecutionOwner, command artifactsmodule.FinalizeCommand) (*artifactsmodule.Artifact, error) {
	return a.store.Finalize(ctx, owner, command)
}

func (a taskRunArtifactTestAPI) Reference(ctx context.Context, _ authority.ExecutionAuthority, owner artifactsmodule.ExecutionOwner, command artifactsmodule.ReferenceCommand) (artifactsmodule.ReferenceResult, error) {
	return a.store.Reference(ctx, owner, command)
}

func (a taskRunArtifactTestAPI) Get(ctx context.Context, _ authority.ExecutionAuthority, owner artifactsmodule.ExecutionOwner, query artifactsmodule.GetQuery) (*artifactsmodule.Artifact, error) {
	return a.store.Get(ctx, owner, query)
}

func (a taskRunArtifactTestAPI) List(ctx context.Context, _ authority.ExecutionAuthority, owner artifactsmodule.ExecutionOwner, filter artifactsmodule.ListFilter) ([]*artifactsmodule.Artifact, error) {
	return a.store.List(ctx, owner, filter)
}

func (a taskRunArtifactTestAPI) CreateContent(
	ctx context.Context,
	_ artifactsmodule.ContentAuthorities,
	owner artifactsmodule.ExecutionOwner,
	command artifactsmodule.CreateCommand,
	content []byte,
	reference artifactsmodule.ReferenceCommand,
) (artifactsmodule.ContentResult, error) {
	artifact, err := a.store.Create(ctx, owner, command)
	if err != nil {
		return artifactsmodule.ContentResult{}, err
	}
	uploaded, err := a.store.Upload(ctx, owner, artifactsmodule.UploadCommand{
		ArtifactID: artifact.ArtifactID, Content: content, MIMEType: command.MIMEType,
	})
	if err != nil {
		return artifactsmodule.ContentResult{}, err
	}
	hash := uploaded.ContentHash
	finalized, err := a.store.Finalize(ctx, owner, artifactsmodule.FinalizeCommand{
		ArtifactID: artifact.ArtifactID, ContentHash: &hash,
	})
	if err != nil {
		return artifactsmodule.ContentResult{}, err
	}
	reference.ArtifactID = finalized.ArtifactID
	referenced, err := a.store.Reference(ctx, owner, reference)
	if err != nil {
		return artifactsmodule.ContentResult{}, err
	}
	return artifactsmodule.ContentResult(referenced), nil
}

func (a taskRunArtifactTestAdapter) Create(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.CreateCommand) (*artifactsmodule.Artifact, error) {
	run, err := a.authorize(ctx, owner)
	if err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(command.TaskID)
	if taskID == "" {
		taskID = run.TaskID
	}
	artifact, err := a.module.store.Artifacts().Create(ctx, store.ArtifactCreate{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID, SessionID: command.SessionID,
		TaskID: taskID, OwnerType: string(artifactsmodule.OwnerTaskRun), OwnerID: owner.TaskRunID,
		Type: command.Type, URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, Checksum: command.Checksum, ContentHash: command.ContentHash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
		DurableStatus: string(artifactsmodule.StatusDeclared), Metadata: command.Metadata,
	})
	return taskRunArtifactFromDomain(artifact), taskRunArtifactStoreError(err)
}

func (a taskRunArtifactTestAdapter) Upload(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.UploadCommand) (*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	if _, err := a.ownedArtifact(ctx, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	artifact, err := a.module.store.Artifacts().UploadContent(ctx, owner.WorkspaceKey, command.ArtifactID, store.ArtifactContentUpload{
		Body: bytes.NewReader(command.Content), MIMEType: command.MIMEType,
	})
	return taskRunArtifactFromDomain(artifact), taskRunArtifactStoreError(err)
}

func (a taskRunArtifactTestAdapter) Finalize(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.FinalizeCommand) (*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	if _, err := a.ownedArtifact(ctx, owner, command.ArtifactID); err != nil {
		return nil, err
	}
	artifact, err := a.module.store.Artifacts().Finalize(ctx, owner.WorkspaceKey, command.ArtifactID, store.ArtifactFinalize{
		URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType, SizeBytes: command.SizeBytes,
		Checksum: command.Checksum, ContentHash: command.ContentHash, Visibility: command.Visibility,
		RedactionStatus: command.RedactionStatus, Metadata: command.Metadata,
	})
	return taskRunArtifactFromDomain(artifact), taskRunArtifactStoreError(err)
}

func (a taskRunArtifactTestAdapter) Reference(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.ReferenceCommand) (artifactsmodule.ReferenceResult, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return artifactsmodule.ReferenceResult{}, err
	}
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
		Artifact: taskRunArtifactFromDomain(artifact),
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

func (a taskRunArtifactTestAdapter) Get(ctx context.Context, owner artifactsmodule.ExecutionOwner, query artifactsmodule.GetQuery) (*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	artifact, err := a.ownedArtifact(ctx, owner, query.ArtifactID)
	return taskRunArtifactFromDomain(artifact), err
}

func (a taskRunArtifactTestAdapter) List(ctx context.Context, owner artifactsmodule.ExecutionOwner, filter artifactsmodule.ListFilter) ([]*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	values, err := a.module.store.Artifacts().List(ctx, owner.WorkspaceKey, store.ArtifactFilter{
		OwnerType: string(artifactsmodule.OwnerTaskRun), OwnerID: owner.TaskRunID,
		Type: filter.Type, Status: string(filter.DurableStatus), Limit: filter.Limit,
	})
	if err != nil {
		return nil, taskRunArtifactStoreError(err)
	}
	out := make([]*artifactsmodule.Artifact, 0, len(values))
	for _, artifact := range values {
		out = append(out, taskRunArtifactFromDomain(artifact))
	}
	return out, nil
}

func (a taskRunArtifactTestAdapter) authorize(ctx context.Context, owner artifactsmodule.ExecutionOwner) (*domain.TaskRun, error) {
	if a.module == nil || a.module.store == nil {
		return nil, artifactsmodule.ErrUnavailable
	}
	run, err := a.module.verifyLease(ctx, owner.WorkspaceKey, leaseIdentity{
		TaskRunID: owner.TaskRunID, NodeID: owner.NodeID, LeaseID: owner.LeaseID,
		LeaseToken: owner.LeaseToken, FencingToken: owner.FencingToken,
	})
	if err != nil {
		return nil, errors.Join(artifactsmodule.ErrNotOwner, err)
	}
	return run, nil
}

func (a taskRunArtifactTestAdapter) ownedArtifact(ctx context.Context, owner artifactsmodule.ExecutionOwner, artifactID string) (*domain.Artifact, error) {
	artifact, err := a.module.store.Artifacts().Get(ctx, owner.WorkspaceKey, artifactID)
	if err != nil {
		return nil, taskRunArtifactStoreError(err)
	}
	if artifact.OwnerType != string(artifactsmodule.OwnerTaskRun) || artifact.OwnerID != owner.TaskRunID {
		return nil, errors.Join(artifactsmodule.ErrNotFound, domain.ErrNotFound)
	}
	return artifact, nil
}

func taskRunArtifactFromDomain(artifact *domain.Artifact) *artifactsmodule.Artifact {
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

func taskRunArtifactStoreError(err error) error {
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
