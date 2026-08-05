package serve

import (
	"context"
	"errors"
	"time"

	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	artifactfleetdb "github.com/tysonthomas9/loomcli/internal/modules/artifacts/fleetdb"
)

// artifactsFleetDBTransport is the composition-owned DTO/error bridge from
// the shared low-level FleetDB client to the Artifacts-owned transport. It
// owns no lifecycle policy and never constructs another client.
type artifactsFleetDBTransport struct {
	transport infrafleetdb.ArtifactTransport
}

var _ artifactfleetdb.Transport = (*artifactsFleetDBTransport)(nil)

func newArtifactsFleetDBTransport(transport infrafleetdb.ArtifactTransport) artifactfleetdb.Transport {
	if transport == nil {
		return nil
	}
	return &artifactsFleetDBTransport{transport: transport}
}

func (transport *artifactsFleetDBTransport) Create(
	ctx context.Context,
	owner artifacts.ExecutionOwner,
	command artifacts.CreateCommand,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.Create(ctx, artifactInfraOwner(owner), infrafleetdb.ArtifactCreateCommand{
		ArtifactID: command.ArtifactID, SessionID: command.SessionID, TaskID: command.TaskID,
		Type: command.Type, URI: command.URI, Summary: command.Summary, MIMEType: command.MIMEType,
		SizeBytes: command.SizeBytes, Checksum: command.Checksum, ContentHash: command.ContentHash,
		Visibility: command.Visibility, RedactionStatus: command.RedactionStatus,
		Metadata: cloneArtifactStringMap(command.Metadata),
	})
	return artifactFromInfra(value), translateArtifactsFleetDBError(err)
}

func (transport *artifactsFleetDBTransport) Upload(
	ctx context.Context,
	owner artifacts.ExecutionOwner,
	command artifacts.UploadCommand,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.Upload(ctx, artifactInfraOwner(owner), infrafleetdb.ArtifactUploadCommand{
		ArtifactID: command.ArtifactID, Content: append([]byte(nil), command.Content...), MIMEType: command.MIMEType,
	})
	return artifactFromInfra(value), translateArtifactsFleetDBError(err)
}

func (transport *artifactsFleetDBTransport) Finalize(
	ctx context.Context,
	owner artifacts.ExecutionOwner,
	command artifacts.FinalizeCommand,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.Finalize(ctx, artifactInfraOwner(owner), infrafleetdb.ArtifactFinalizeCommand{
		ArtifactID: command.ArtifactID, URI: cloneArtifactStringPointer(command.URI),
		Summary: cloneArtifactStringPointer(command.Summary), MIMEType: cloneArtifactStringPointer(command.MIMEType),
		SizeBytes: cloneArtifactInt64Pointer(command.SizeBytes), Checksum: cloneArtifactStringPointer(command.Checksum),
		ContentHash: cloneArtifactStringPointer(command.ContentHash), Visibility: cloneArtifactStringPointer(command.Visibility),
		RedactionStatus: cloneArtifactStringPointer(command.RedactionStatus), Metadata: cloneArtifactMapPointer(command.Metadata),
	})
	return artifactFromInfra(value), translateArtifactsFleetDBError(err)
}

func (transport *artifactsFleetDBTransport) Reference(
	ctx context.Context,
	owner artifacts.ExecutionOwner,
	command artifacts.ReferenceCommand,
) (artifacts.ReferenceResult, error) {
	value, err := transport.transport.Reference(ctx, artifactInfraOwner(owner), infrafleetdb.ArtifactReferenceCommand{
		ArtifactID: command.ArtifactID, Kind: command.Kind, TargetRef: command.TargetRef,
	})
	if err != nil {
		return artifacts.ReferenceResult{}, translateArtifactsFleetDBError(err)
	}
	return artifacts.ReferenceResult{Artifact: artifactFromInfra(value.Artifact), Reference: artifactReferenceFromInfra(value.Reference)}, nil
}

func (transport *artifactsFleetDBTransport) Get(
	ctx context.Context,
	owner artifacts.ExecutionOwner,
	query artifacts.GetQuery,
) (*artifacts.Artifact, error) {
	value, err := transport.transport.Get(ctx, artifactInfraOwner(owner), query.ArtifactID)
	return artifactFromInfra(value), translateArtifactsFleetDBError(err)
}

func (transport *artifactsFleetDBTransport) List(
	ctx context.Context,
	owner artifacts.ExecutionOwner,
	filter artifacts.ListFilter,
) ([]*artifacts.Artifact, error) {
	values, err := transport.transport.List(ctx, artifactInfraOwner(owner), infrafleetdb.ArtifactFilter{
		Type: filter.Type, DurableStatus: string(filter.DurableStatus), Limit: filter.Limit,
	})
	if err != nil {
		return nil, translateArtifactsFleetDBError(err)
	}
	result := make([]*artifacts.Artifact, 0, len(values))
	for _, value := range values {
		result = append(result, artifactFromInfra(value))
	}
	return result, nil
}

func artifactReferenceFromInfra(value *infrafleetdb.ArtifactReference) *artifacts.ArtifactReference {
	if value == nil {
		return nil
	}
	return &artifacts.ArtifactReference{
		WorkspaceKey: value.WorkspaceKey, ReferenceID: value.ReferenceID, ArtifactID: value.ArtifactID,
		OwnerType: artifacts.OwnerType(value.OwnerType), OwnerID: value.OwnerID,
		Kind: value.Kind, TargetRef: value.TargetRef, CreatedAt: value.CreatedAt,
	}
}

func artifactInfraOwner(owner artifacts.ExecutionOwner) infrafleetdb.ArtifactOwner {
	return infrafleetdb.ArtifactOwner{
		WorkspaceKey: owner.WorkspaceKey, TaskRunID: owner.TaskRunID, NodeID: owner.NodeID,
		LeaseID: owner.LeaseID, LeaseToken: owner.LeaseToken, FencingToken: owner.FencingToken,
	}
}

func artifactFromInfra(value *infrafleetdb.Artifact) *artifacts.Artifact {
	if value == nil {
		return nil
	}
	return &artifacts.Artifact{
		WorkspaceKey: value.WorkspaceKey, ArtifactID: value.ArtifactID,
		SessionID: value.SessionID, TaskID: value.TaskID, OwnerType: artifacts.OwnerType(value.OwnerType), OwnerID: value.OwnerID,
		Type: value.Type, URI: value.URI, Summary: value.Summary, MIMEType: value.MIMEType, SizeBytes: value.SizeBytes,
		Checksum: value.Checksum, ContentHash: value.ContentHash, Visibility: value.Visibility,
		RedactionStatus: value.RedactionStatus, DurableStatus: artifacts.DurableStatus(value.DurableStatus),
		Metadata: cloneArtifactStringMap(value.Metadata), FinalizedAt: cloneArtifactTimePointer(value.FinalizedAt),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func translateArtifactsFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	var translated error
	switch {
	case errors.Is(err, infrafleetdb.ErrArtifactsNotFound):
		translated = artifactfleetdb.ErrTransportNotFound
	case errors.Is(err, infrafleetdb.ErrArtifactsInvalid):
		translated = artifactfleetdb.ErrTransportInvalid
	case errors.Is(err, infrafleetdb.ErrArtifactsConflict):
		translated = artifactfleetdb.ErrTransportConflict
	case errors.Is(err, infrafleetdb.ErrArtifactsNotOwner):
		translated = artifactfleetdb.ErrTransportNotOwner
	case errors.Is(err, infrafleetdb.ErrArtifactsInvalidTransition):
		translated = artifactfleetdb.ErrTransportInvalidTransition
	case errors.Is(err, infrafleetdb.ErrArtifactsUnavailable):
		translated = artifactfleetdb.ErrTransportUnavailable
	default:
		return err
	}
	return errors.Join(translated, err)
}

func cloneArtifactStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneArtifactStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneArtifactInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneArtifactMapPointer(value *map[string]string) *map[string]string {
	if value == nil {
		return nil
	}
	result := cloneArtifactStringMap(*value)
	return &result
}

func cloneArtifactTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
