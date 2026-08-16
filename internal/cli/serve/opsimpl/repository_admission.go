package opsimpl

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/app/repositoryadmission"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter"
	repositoryadmissioninfra "github.com/tysonthomas9/loomcli/internal/infra/repositoryadmission"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// RepositoryAdmission is the CLI-private composition facade for the durable
// application workflow and its FleetDB/local adapters. The serve command sees
// one cohesive runtime instead of importing either implementation package.
type RepositoryAdmission struct {
	journal  repositoryadmission.Journal
	workflow *repositoryadmission.Workflow
}

func NewRepositoryAdmission(root string) (*RepositoryAdmission, error) {
	journal, err := repositoryadmissioninfra.NewRepositoryAdmissionJournal(root)
	if err != nil {
		return nil, err
	}
	return &RepositoryAdmission{journal: journal}, nil
}

func (composition *RepositoryAdmission) LocalResolver() sourcecontrol.RepositoryAdmissionLocalResolver {
	if composition == nil {
		return nil
	}
	return repositoryAdmissionLocalResolver{journal: composition.journal}
}

func (composition *RepositoryAdmission) Configure(
	cfg *webui.ServerConfig,
	storeHandle *bootstrap.StoreHandle,
	agentsCommands repositoryadmission.ManagedAgentsCommands,
) bool {
	if composition == nil {
		return false
	}
	composition.workflow = ConfigureWorkspaceAdmission(
		cfg, storeHandle, composition.journal, agentsCommands,
	)
	return composition.workflow != nil
}

func (composition *RepositoryAdmission) EnsureBuiltinRolePrompts(
	ctx context.Context,
	cfg *webui.ServerConfig,
	agentsCommands repositoryadmission.ManagedAgentsCommands,
) error {
	if cfg == nil {
		return nil
	}
	return repositoryadmissioninfra.EnsureBuiltinRolePrompts(
		ctx, cfg.WorkspaceCatalog, agentsCommands,
	)
}

func (composition *RepositoryAdmission) RuntimeRegistrations() []platformruntime.Registration {
	if composition == nil || composition.workflow == nil {
		return nil
	}
	return composition.workflow.RuntimeRegistrations()
}

func (composition *RepositoryAdmission) Stop(ctx context.Context) error {
	if composition == nil || composition.workflow == nil {
		return nil
	}
	return composition.workflow.Stop(ctx)
}

// ConfigureWorkspaceAdmission wires delivery callbacks around the workflow.
// A nil store keeps workspace management unavailable; nil durable/journal
// inputs intentionally support the synchronous CLI/test path.
func ConfigureWorkspaceAdmission(
	cfg *webui.ServerConfig,
	storeHandle *bootstrap.StoreHandle,
	journal repositoryadmission.Journal,
	agentsCommands repositoryadmission.ManagedAgentsCommands,
) *repositoryadmission.Workflow {
	if cfg == nil || cfg.Store == nil || agentsCommands == nil {
		return nil
	}
	cfg.WorkspaceIDResolverFn = serveadapter.BuildWorkspaceIDResolverFn(cfg.Store)
	cfg.InitialWorkspaceID = serveadapter.ResolveInitialWorkspaceID(cfg.Store)
	cfg.WorkspaceDeleteCleanupFn = serveadapter.BuildWorkspaceDeleteCleanupFn()
	var admissions repositoryadmission.DurableAdmissions
	if storeHandle != nil && storeHandle.FleetDBClient() != nil {
		admissions = repositoryadmissioninfra.NewRepositoryAdmissionDurability(
			storeHandle.FleetDBClient().RepositoryAdmissions(),
		)
	}
	local := repositoryadmissioninfra.NewRepositoryAdmissionLocalWorkspace(
		cfg.WorkspaceCatalog,
		agentsCommands,
		cfg.WorkspaceSourceControl,
		journal,
	)
	workflow := repositoryadmission.New(admissions, journal, local)
	if workflow == nil {
		return nil
	}
	delivery := repositoryAdmissionDelivery{workflow: workflow}
	cfg.WorkspaceCreateFn = delivery.CreateWorkspace
	cfg.WorkspaceAddReposFn = delivery.AddWorkspaceRepos
	if len(workflow.RuntimeRegistrations()) > 0 {
		cfg.WorkspaceAdmissions = delivery
	}
	if cfg.WorkspaceAdmissions == nil {
		return nil
	}
	return workflow
}

// repositoryAdmissionDelivery maps WebUI transport-neutral coordinator models
// to the Repository Admission application module. It owns no workflow state.
type repositoryAdmissionDelivery struct {
	workflow interface {
		repositoryadmission.Admission
		repositoryadmission.Executor
	}
}

func (delivery repositoryAdmissionDelivery) CreateWorkspace(
	ctx context.Context,
	request workspacecoord.WorkspaceCreateRequest,
) (workspacecoord.WorkspaceCreateResult, error) {
	workflowCtx := repositoryadmission.WithWarnings(ctx)
	result, err := delivery.workflow.Create(workflowCtx, mapCreateCommand(request))
	for _, warning := range repositoryadmission.Warnings(workflowCtx) {
		workspacecoord.AddCreateWarning(ctx, warning)
	}
	return workspacecoord.WorkspaceCreateResult{
		WorkspaceID:   result.WorkspaceID,
		WorkspacePath: result.WorkspacePath,
	}, err
}

func (delivery repositoryAdmissionDelivery) AddWorkspaceRepos(
	ctx context.Context,
	request workspacecoord.WorkspaceAddReposRequest,
) (workspacecoord.WorkspaceCreateResult, error) {
	result, err := delivery.workflow.AddRepositories(ctx, mapAddRepositoriesCommand(request))
	return workspacecoord.WorkspaceCreateResult{
		WorkspaceID:   result.WorkspaceID,
		WorkspacePath: result.WorkspacePath,
	}, err
}

func (delivery repositoryAdmissionDelivery) StartCreate(
	ctx context.Context,
	request workspacecoord.WorkspaceCreateRequest,
) (string, error) {
	return delivery.workflow.StartCreate(ctx, mapCreateCommand(request))
}

func (delivery repositoryAdmissionDelivery) StartAddRepos(
	ctx context.Context,
	request workspacecoord.WorkspaceAddReposRequest,
) (string, error) {
	return delivery.workflow.StartAddRepositories(ctx, mapAddRepositoriesCommand(request))
}

func (delivery repositoryAdmissionDelivery) LookupJob(
	ctx context.Context,
	admissionID string,
) (*workspacecoord.WorkspaceJob, bool, error) {
	status, found, err := delivery.workflow.Get(ctx, admissionID)
	if err != nil || !found || status == nil {
		return nil, found, err
	}
	return &workspacecoord.WorkspaceJob{
		ID:          status.ID,
		Status:      workspacecoord.WorkspaceJobStatus(status.State),
		Progress:    status.Progress,
		WorkspaceID: status.WorkspaceID,
		Error:       status.Error,
		CompletedAt: status.CompletedAt,
	}, true, nil
}

func mapCreateCommand(request workspacecoord.WorkspaceCreateRequest) repositoryadmission.CreateCommand {
	return repositoryadmission.CreateCommand{
		Name: request.Name, Type: request.Type,
		Repos:     append([]string(nil), request.Repos...),
		CloneURLs: append([]string(nil), request.CloneURLs...),
		Branch:    request.Branch, Path: request.Path,
	}
}

func mapAddRepositoriesCommand(
	request workspacecoord.WorkspaceAddReposRequest,
) repositoryadmission.AddRepositoriesCommand {
	return repositoryadmission.AddRepositoriesCommand{
		WorkspaceID: request.WorkspaceID,
		Repos:       append([]string(nil), request.Repos...),
		CloneURLs:   append([]string(nil), request.CloneURLs...),
		Branch:      request.Branch,
	}
}

var _ workspacecoord.WorkspaceAdmissionCoordinator = repositoryAdmissionDelivery{}

type repositoryAdmissionLocalResolver struct {
	journal repositoryadmission.Journal
}

func (resolver repositoryAdmissionLocalResolver) ResolveLocalRepositoryAdmission(
	ctx context.Context,
	admissionID string,
) (sourcecontrol.RepositoryAdmissionLocalProjection, error) {
	if resolver.journal == nil {
		return sourcecontrol.RepositoryAdmissionLocalProjection{}, sourcecontrol.ErrRepositoryAdmissionNotFound
	}
	projection, err := resolver.journal.ResolveLocal(ctx, admissionID)
	if err != nil {
		switch {
		case errors.Is(err, repositoryadmission.ErrLocalNotFound):
			return sourcecontrol.RepositoryAdmissionLocalProjection{}, errors.Join(sourcecontrol.ErrRepositoryAdmissionNotFound, err)
		case errors.Is(err, repositoryadmission.ErrInvalid):
			return sourcecontrol.RepositoryAdmissionLocalProjection{}, errors.Join(sourcecontrol.ErrInvalidMaterialization, err)
		default:
			return sourcecontrol.RepositoryAdmissionLocalProjection{}, fmt.Errorf("resolve local repository admission: %w", err)
		}
	}
	return sourcecontrol.RepositoryAdmissionLocalProjection{
		WorkspaceKey: projection.WorkspaceKey, AdmissionID: projection.AdmissionID,
		OperationID: projection.OperationID, OwnerID: projection.OwnerID,
		OwnerGenerationID: projection.OwnerGenerationID,
		SpecFingerprint:   projection.SpecFingerprint,
		WorkspacePath:     projection.WorkspacePath,
	}, nil
}

var _ sourcecontrol.RepositoryAdmissionLocalResolver = repositoryAdmissionLocalResolver{}
