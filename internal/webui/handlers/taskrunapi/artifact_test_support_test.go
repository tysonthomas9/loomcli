package taskrunapi

import (
	"context"
	"errors"
	"strings"

	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type artifactCommandStore interface {
	ArtifactCommands() artifactsmodule.Store
}

// taskRunArtifactTestAdapter keeps handler tests at the Artifacts-owned port
// while preserving the task-run lease check performed by runtime composition.
type taskRunArtifactTestAdapter struct {
	module *Module
	store  artifactsmodule.Store
}

var _ artifactsmodule.Store = taskRunArtifactTestAdapter{}

func newTaskRunArtifactAPIForTest(module *Module, source any) artifactsmodule.API {
	provider, ok := source.(artifactCommandStore)
	if !ok {
		panic("task-run artifact test store does not expose the Artifacts command port")
	}
	return taskRunArtifactTestAPI{store: taskRunArtifactTestAdapter{
		module: module,
		store:  provider.ArtifactCommands(),
	}}
}

type taskRunArtifactTestAPI struct {
	store artifactsmodule.Store
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
	if strings.TrimSpace(command.TaskID) == "" {
		command.TaskID = run.WorkItemID
	}
	return a.store.Create(ctx, owner, command)
}

func (a taskRunArtifactTestAdapter) Upload(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.UploadCommand) (*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	return a.store.Upload(ctx, owner, command)
}

func (a taskRunArtifactTestAdapter) Finalize(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.FinalizeCommand) (*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	return a.store.Finalize(ctx, owner, command)
}

func (a taskRunArtifactTestAdapter) Reference(ctx context.Context, owner artifactsmodule.ExecutionOwner, command artifactsmodule.ReferenceCommand) (artifactsmodule.ReferenceResult, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return artifactsmodule.ReferenceResult{}, err
	}
	return a.store.Reference(ctx, owner, command)
}

func (a taskRunArtifactTestAdapter) Get(ctx context.Context, owner artifactsmodule.ExecutionOwner, query artifactsmodule.GetQuery) (*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	return a.store.Get(ctx, owner, query)
}

func (a taskRunArtifactTestAdapter) List(ctx context.Context, owner artifactsmodule.ExecutionOwner, filter artifactsmodule.ListFilter) ([]*artifactsmodule.Artifact, error) {
	if _, err := a.authorize(ctx, owner); err != nil {
		return nil, err
	}
	return a.store.List(ctx, owner, filter)
}

func (a taskRunArtifactTestAdapter) authorize(ctx context.Context, owner artifactsmodule.ExecutionOwner) (*execution.TaskRun, error) {
	if a.module == nil || a.module.taskRuns == nil || a.store == nil {
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
