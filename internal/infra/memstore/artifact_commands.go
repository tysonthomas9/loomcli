package memstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
)

var _ artifacts.Store = (*artifactStore)(nil)

// ArtifactCommands exposes the Artifacts-owned durable port from this
// explicitly test-only adapter. Runtime composition uses FleetDB instead.
func (s *Store) ArtifactCommands() artifacts.Store {
	if s == nil {
		return nil
	}
	return s.artifacts
}

func (s *artifactStore) Create(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.CreateCommand) (*artifacts.Artifact, error) {
	if _, err := s.GetArtifactRecord(ctx, owner.WorkspaceKey, command.ArtifactID); err == nil {
		return nil, artifacts.ErrAlreadyExists
	} else if !errors.Is(err, artifacts.ErrNotFound) {
		return nil, err
	}
	return s.seed(ctx, artifacts.Artifact{
		WorkspaceKey: owner.WorkspaceKey, ArtifactID: command.ArtifactID,
		AgentID: command.AgentID, SessionID: command.SessionID, TaskID: command.TaskID,
		OwnerType: artifacts.OwnerTaskRun, OwnerID: owner.TaskRunID,
		Type: command.Type, URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, Checksum: command.Checksum, ContentHash: command.ContentHash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
		DurableStatus: artifacts.StatusDeclared, Metadata: command.Metadata,
	}, nil)
}

func (s *artifactStore) Upload(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.UploadCommand) (*artifacts.Artifact, error) {
	value, err := s.ownedExecutionArtifact(ctx, owner, command.ArtifactID)
	if err != nil {
		return nil, err
	}
	value.MIMEType = command.MIMEType
	value.DurableStatus = artifacts.StatusUploading
	value.FinalizedAt = nil
	return s.seed(ctx, *value, command.Content)
}

func (s *artifactStore) Finalize(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.FinalizeCommand) (*artifacts.Artifact, error) {
	value, err := s.ownedExecutionArtifact(ctx, owner, command.ArtifactID)
	if err != nil {
		return nil, err
	}
	applyArtifactFinalizeCommand(value, command)
	value.DurableStatus = artifacts.StatusFinalized
	value.FinalizedAt = nil
	return s.seed(ctx, *value, nil)
}

func applyArtifactFinalizeCommand(value *artifacts.Artifact, command artifacts.FinalizeCommand) {
	assignArtifactCommand(command.URI, &value.URI)
	assignArtifactCommand(command.Summary, &value.Summary)
	assignArtifactCommand(command.MIMEType, &value.MIMEType)
	assignArtifactCommand(command.SizeBytes, &value.SizeBytes)
	assignArtifactCommand(command.Checksum, &value.Checksum)
	assignArtifactCommand(command.ContentHash, &value.ContentHash)
	assignArtifactCommand(command.Visibility, &value.Visibility)
	assignArtifactCommand(command.RedactionStatus, &value.RedactionStatus)
	if command.Metadata != nil {
		value.Metadata = cloneMap(*command.Metadata)
	}
}

func assignArtifactCommand[T any](source *T, target *T) {
	if source != nil {
		*target = *source
	}
}

func (s *artifactStore) Reference(ctx context.Context, owner artifacts.ExecutionOwner, command artifacts.ReferenceCommand) (artifacts.ReferenceResult, error) {
	value, err := s.ownedExecutionArtifact(ctx, owner, command.ArtifactID)
	if err != nil {
		return artifacts.ReferenceResult{}, err
	}
	if value.DurableStatus != artifacts.StatusFinalized || value.FinalizedAt == nil {
		return artifacts.ReferenceResult{}, artifacts.ErrInvalidTransition
	}
	createdAt := value.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return artifacts.ReferenceResult{
		Artifact: value,
		Reference: &artifacts.ArtifactReference{
			WorkspaceKey: owner.WorkspaceKey,
			ReferenceID:  fmt.Sprintf("mem-reference:%s:%s:%s", command.ArtifactID, command.Kind, command.TargetRef),
			ArtifactID:   command.ArtifactID, OwnerType: artifacts.OwnerTaskRun, OwnerID: owner.TaskRunID,
			Kind: command.Kind, TargetRef: command.TargetRef, CreatedAt: createdAt,
		},
	}, nil
}

func (s *artifactStore) Get(ctx context.Context, owner artifacts.ExecutionOwner, query artifacts.GetQuery) (*artifacts.Artifact, error) {
	return s.ownedExecutionArtifact(ctx, owner, query.ArtifactID)
}

func (s *artifactStore) List(ctx context.Context, owner artifacts.ExecutionOwner, filter artifacts.ListFilter) ([]*artifacts.Artifact, error) {
	return s.ListArtifactRecords(ctx, owner.WorkspaceKey, artifacts.SearchFilter{
		OwnerType: artifacts.OwnerTaskRun, OwnerID: owner.TaskRunID,
		Type: filter.Type, DurableStatus: filter.DurableStatus, Limit: filter.Limit,
	})
}

func (s *artifactStore) ownedExecutionArtifact(ctx context.Context, owner artifacts.ExecutionOwner, artifactID string) (*artifacts.Artifact, error) {
	value, err := s.GetArtifactRecord(ctx, owner.WorkspaceKey, artifactID)
	if err != nil {
		return nil, err
	}
	if value.OwnerType != artifacts.OwnerTaskRun || value.OwnerID != owner.TaskRunID {
		return nil, errors.Join(artifacts.ErrNotFound, artifacts.ErrNotOwner)
	}
	return value, nil
}
