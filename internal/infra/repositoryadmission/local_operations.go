package repositoryadmissioninfra

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/repositoryadmission"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type repositoryAdmissionLocalWorkspace struct {
	catalog      workspacemodule.API
	agents       repositoryadmission.ManagedAgentsCommands
	materializer RepositoryCheckoutMaterializer
	journal      repositoryadmission.Journal
}

func NewRepositoryAdmissionLocalWorkspace(
	catalog workspacemodule.API,
	agents repositoryadmission.ManagedAgentsCommands,
	materializer RepositoryCheckoutMaterializer,
	journal repositoryadmission.Journal,
) repositoryadmission.LocalWorkspace {
	if catalog == nil || agents == nil {
		return nil
	}
	return &repositoryAdmissionLocalWorkspace{
		catalog: catalog, agents: agents, materializer: materializer, journal: journal,
	}
}

func (local *repositoryAdmissionLocalWorkspace) CreateEmpty(ctx context.Context, command repositoryadmission.CreateCommand) (repositoryadmission.Result, error) {
	return createStoreBackedEmptyWorkspace(ctx, local.catalog, local.agents, command)
}

func (local *repositoryAdmissionLocalWorkspace) AddWithoutAdmission(ctx context.Context, command repositoryadmission.AddRepositoriesCommand) (repositoryadmission.Result, error) {
	return addReposToStoreBackedWorkspace(ctx, local.catalog, command, local.materializer)
}

func (local *repositoryAdmissionLocalWorkspace) PlanCreate(ctx context.Context, command repositoryadmission.CreateCommand) (repositoryadmission.CreatePlan, error) {
	if len(command.CloneURLs) == 0 {
		return repositoryadmission.CreatePlan{}, workspacemodule.NewCreateError(workspacemodule.PathNotFound, "no clone URLs specified", nil)
	}
	key := repositoryadmission.WorkspaceKey(command.Name)
	planned, err := planCloneRepos(command.CloneURLs, make(map[string]bool))
	if err != nil {
		return repositoryadmission.CreatePlan{}, err
	}
	specs := cloneAdmissionSpecs(planned, command.Branch)
	cloneURLs := make([]string, 0, len(planned))
	for _, repository := range planned {
		cloneURLs = append(cloneURLs, repository.remoteURL)
	}
	workspacePath, err := canonicalWorkspacePath(command.Path, command.Name)
	if err != nil {
		return repositoryadmission.CreatePlan{}, err
	}
	operationID, err := repositoryadmission.OperationID("create_workspace", key, workspacePath, specs)
	if err != nil {
		return repositoryadmission.CreatePlan{}, err
	}
	recovering, err := local.matchesRecoveryIntent(ctx, repositoryadmission.LocalIntent{
		OperationID: operationID, WorkspaceKey: key,
		WorkspaceName: strings.TrimSpace(command.Name), WorkspacePath: workspacePath,
		Kind:   repositoryadmission.KindCreateWorkspace,
		Branch: strings.TrimSpace(command.Branch), CloneURLs: cloneURLs,
	})
	if err != nil {
		return repositoryadmission.CreatePlan{}, err
	}
	if !recovering {
		workspacePlan, strictErr := resolveWorkspaceDirForCreate(command.Path, command.Name)
		if strictErr != nil {
			return repositoryadmission.CreatePlan{}, strictErr
		}
		workspacePath = workspacePlan.path
	}
	if err := context.Cause(ctx); err != nil {
		return repositoryadmission.CreatePlan{}, err
	}
	return repositoryadmission.CreatePlan{
		WorkspaceKey: key, WorkspacePath: workspacePath,
		Repositories: specs, CloneURLs: cloneURLs,
	}, nil
}

func (local *repositoryAdmissionLocalWorkspace) MaterializeCreate(
	ctx context.Context,
	command repositoryadmission.CreateCommand,
	plan repositoryadmission.CreatePlan,
	record *repositoryadmission.Record,
	check repositoryadmission.OwnershipCheck,
) (repositoryadmission.MaterializationResult, error) {
	if err := checkRepositoryAdmissionOwnership(ctx, check); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if err := os.MkdirAll(plan.WorkspacePath, 0o755); err != nil {
		return repositoryadmission.MaterializationResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}
	if err := checkRepositoryAdmissionOwnership(ctx, check); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if err := saveLocalWorkspaceState(plan.WorkspaceKey, plan.WorkspacePath, nil); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	repositories, _, err := materializeAddReposClones(
		ctx, plan.WorkspaceKey, record, plan.CloneURLs, plan.WorkspacePath,
		make(map[string]bool), nil, command.Branch, local.materializer, check,
	)
	if err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if len(repositories) == 0 || len(repositories) != len(plan.Repositories) {
		return repositoryadmission.MaterializationResult{}, repositoryadmission.ErrInvalid
	}
	if err := checkRepositoryAdmissionOwnership(ctx, check); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if err := saveLocalWorkspaceState(plan.WorkspaceKey, plan.WorkspacePath, repositories); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if err := seedBuiltInRoles(ctx, local.agents, plan.WorkspaceKey, plan.WorkspacePath); err != nil {
		return repositoryadmission.MaterializationResult{}, fmt.Errorf("seed built-in roles: %w", err)
	}
	return repositoryadmission.MaterializationResult{
		Repositories: repositories, DefaultBranch: repositories[0].DefaultBranch,
	}, nil
}

func (local *repositoryAdmissionLocalWorkspace) PlanAdd(ctx context.Context, command repositoryadmission.AddRepositoriesCommand) (repositoryadmission.AddPlan, error) {
	key, workspace, err := resolveWorkspaceForAddRepos(ctx, local.catalog, command.WorkspaceID)
	if err != nil {
		return repositoryadmission.AddPlan{}, err
	}
	workspacePath, err := prepareWorkspaceDir(key)
	if err != nil {
		return repositoryadmission.AddPlan{}, err
	}
	localRepositories, err := resolveRequestRepos(command.Repos)
	if err != nil {
		return repositoryadmission.AddPlan{}, err
	}
	existing, err := local.catalog.ListRepositories(ctx, workspacemodule.ListRepositoriesQuery{WorkspaceReference: key})
	if err != nil {
		return repositoryadmission.AddPlan{}, fmt.Errorf("list workspace repos: %w", err)
	}
	branch := pickAddReposBranch(command.Branch, workspace, key)
	_, localSpecs, err := planLocalAdmissionRepos(localRepositories, workspacePath, branch, command.Branch)
	if err != nil {
		return repositoryadmission.AddPlan{}, err
	}
	localPaths := make([]string, 0, len(localRepositories))
	for _, repository := range localRepositories {
		localPaths = append(localPaths, repository.path)
	}
	inputs := addRepositoryPlanInputs{
		command: command, key: key, workspacePath: workspacePath, branch: branch,
		localRepositories: localRepositories, localSpecs: localSpecs,
		localPaths: localPaths, existing: existing,
	}
	if plan, recovering, err := local.planRecoveryAdd(ctx, inputs); err != nil || recovering {
		return plan, err
	}
	return planFreshAdd(inputs)
}

type addRepositoryPlanInputs struct {
	command           repositoryadmission.AddRepositoriesCommand
	key               string
	workspacePath     string
	branch            string
	localRepositories []resolvedRepo
	localSpecs        []repositoryadmission.RepositorySpec
	localPaths        []string
	existing          []workspacemodule.Repository
}

func (local *repositoryAdmissionLocalWorkspace) planRecoveryAdd(
	ctx context.Context,
	inputs addRepositoryPlanInputs,
) (repositoryadmission.AddPlan, bool, error) {
	// Build the exact original candidate before considering catalog names. A
	// committed admission has already published those names, so applying normal
	// fresh-request deduplication first would manufacture different names and
	// make its durable operation impossible to replay.
	requestedNames := make(map[string]bool, len(inputs.localRepositories)+len(inputs.command.CloneURLs))
	for _, repository := range inputs.localRepositories {
		requestedNames[repository.name] = true
	}
	recoveryClones, err := planCloneRepos(inputs.command.CloneURLs, requestedNames)
	if err != nil {
		return repositoryadmission.AddPlan{}, false, err
	}
	recoverySpecs := append(append([]repositoryadmission.RepositorySpec(nil), inputs.localSpecs...), cloneAdmissionSpecs(recoveryClones, inputs.command.Branch)...)
	recoveryCloneURLs := make([]string, 0, len(recoveryClones))
	for _, repository := range recoveryClones {
		recoveryCloneURLs = append(recoveryCloneURLs, repository.remoteURL)
	}
	operationID, err := repositoryadmission.OperationID("add_repositories", inputs.key, inputs.workspacePath, recoverySpecs)
	if err != nil {
		return repositoryadmission.AddPlan{}, false, err
	}
	recovering, err := local.matchesRecoveryIntent(ctx, repositoryadmission.LocalIntent{
		OperationID: operationID, WorkspaceKey: inputs.key, WorkspacePath: inputs.workspacePath,
		Kind:   repositoryadmission.KindAddRepositories,
		Branch: strings.TrimSpace(inputs.command.Branch), CloneURLs: recoveryCloneURLs,
		LocalRepoPaths: inputs.localPaths,
	})
	if err != nil {
		return repositoryadmission.AddPlan{}, false, err
	}
	if recovering {
		return repositoryadmission.AddPlan{
			WorkspaceKey: inputs.key, WorkspacePath: inputs.workspacePath, Branch: inputs.branch,
			Repositories: recoverySpecs, CloneURLs: recoveryCloneURLs, LocalRepoPaths: inputs.localPaths,
		}, true, nil
	}
	return repositoryadmission.AddPlan{}, false, nil
}

func planFreshAdd(inputs addRepositoryPlanInputs) (repositoryadmission.AddPlan, error) {
	// No exact protected recovery fact exists, so preserve the fresh-request
	// behavior: local-name collisions fail and clone-name collisions receive a
	// deterministic suffix.
	seen := make(map[string]bool, len(inputs.existing)+len(inputs.localRepositories)+len(inputs.command.CloneURLs))
	for _, repository := range inputs.existing {
		seen[repository.Name] = true
	}
	for _, repository := range inputs.localRepositories {
		if seen[repository.name] {
			return repositoryadmission.AddPlan{}, workspacemodule.NewCreateError(
				workspacemodule.AlreadyExists,
				fmt.Sprintf("repo %q already exists in workspace %q", repository.name, inputs.key),
				nil,
			)
		}
		seen[repository.name] = true
	}
	plannedClones, err := planCloneRepos(inputs.command.CloneURLs, seen)
	if err != nil {
		return repositoryadmission.AddPlan{}, err
	}
	specs := append(append([]repositoryadmission.RepositorySpec(nil), inputs.localSpecs...), cloneAdmissionSpecs(plannedClones, inputs.command.Branch)...)
	cloneURLs := make([]string, 0, len(plannedClones))
	for _, repository := range plannedClones {
		cloneURLs = append(cloneURLs, repository.remoteURL)
	}
	return repositoryadmission.AddPlan{
		WorkspaceKey: inputs.key, WorkspacePath: inputs.workspacePath, Branch: inputs.branch,
		Repositories: specs, CloneURLs: cloneURLs, LocalRepoPaths: inputs.localPaths,
	}, nil
}

func (local *repositoryAdmissionLocalWorkspace) matchesRecoveryIntent(ctx context.Context, expected repositoryadmission.LocalIntent) (bool, error) {
	if local.journal == nil {
		return false, nil
	}
	record, err := local.journal.GetByOperation(ctx, expected.OperationID)
	if errors.Is(err, repositoryadmission.ErrLocalNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	actual := record.Intent
	if actual.OperationID != expected.OperationID ||
		actual.WorkspaceKey != expected.WorkspaceKey ||
		actual.WorkspaceName != expected.WorkspaceName ||
		filepath.Clean(actual.WorkspacePath) != filepath.Clean(expected.WorkspacePath) ||
		actual.Kind != expected.Kind ||
		actual.Branch != expected.Branch ||
		!slices.Equal(actual.CloneURLs, expected.CloneURLs) ||
		!slices.Equal(actual.LocalRepoPaths, expected.LocalRepoPaths) {
		return false, repositoryadmission.ErrLocalConflict
	}
	return true, nil
}

func (local *repositoryAdmissionLocalWorkspace) MaterializeAdd(
	ctx context.Context,
	command repositoryadmission.AddRepositoriesCommand,
	plan repositoryadmission.AddPlan,
	record *repositoryadmission.Record,
	check repositoryadmission.OwnershipCheck,
) (repositoryadmission.MaterializationResult, error) {
	localRepositories, err := resolveRequestRepos(command.Repos)
	if err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	seen, err := dedupAddReposAgainstExisting(ctx, local.catalog, plan.WorkspaceKey, localRepositories)
	if err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	localConfigs, _, err := planLocalAdmissionRepos(localRepositories, plan.WorkspacePath, plan.Branch, command.Branch)
	if err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	materializedLocal, err := materializeLocalAdmissionRepos(ctx, plan.WorkspacePath, plan.Branch, localRepositories, localConfigs, check)
	if err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	cloned, _, err := materializeAddReposClones(
		ctx, plan.WorkspaceKey, record, plan.CloneURLs, plan.WorkspacePath,
		seen, nil, command.Branch, local.materializer, check,
	)
	if err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	repositories := append(append([]repositoryadmission.RepositoryPlacement(nil), materializedLocal...), cloned...)
	if len(repositories) != len(plan.Repositories) {
		return repositoryadmission.MaterializationResult{}, repositoryadmission.ErrInvalid
	}
	if err := checkRepositoryAdmissionOwnership(ctx, check); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	if err := saveLocalWorkspaceState(plan.WorkspaceKey, plan.WorkspacePath, repositories); err != nil {
		return repositoryadmission.MaterializationResult{}, err
	}
	return repositoryadmission.MaterializationResult{Repositories: repositories}, nil
}

func planLocalAdmissionRepos(
	repositories []resolvedRepo,
	workspacePath, workspaceBranch, defaultBranchOverride string,
) ([]repositoryadmission.RepositoryPlacement, []repositoryadmission.RepositorySpec, error) {
	placements := make([]repositoryadmission.RepositoryPlacement, 0, len(repositories))
	specs := make([]repositoryadmission.RepositorySpec, 0, len(repositories))
	for _, repository := range repositories {
		placement, err := worktreeRepoConfig(repository, filepath.Join(workspacePath, repository.name), defaultBranchOverride)
		if err != nil {
			return nil, nil, err
		}
		if placement.DefaultBranch == "" {
			placement.DefaultBranch = workspaceBranch
		}
		remoteURL, err := persistentGitRemoteURL(repository.path, "origin")
		if err != nil {
			return nil, nil, err
		}
		placements = append(placements, placement)
		specs = append(specs, repositoryadmission.RepositorySpec{
			Name: repository.name, RemoteURL: remoteURL, Remote: "origin",
			DefaultBranch: placement.DefaultBranch, SourceRepoID: repository.name,
		})
	}
	return placements, specs, nil
}

func materializeLocalAdmissionRepos(
	ctx context.Context,
	workspacePath, workspaceBranch string,
	repositories []resolvedRepo,
	placements []repositoryadmission.RepositoryPlacement,
	check repositoryadmission.OwnershipCheck,
) ([]repositoryadmission.RepositoryPlacement, error) {
	if len(repositories) != len(placements) {
		return nil, repositoryadmission.ErrInvalid
	}
	result := make([]repositoryadmission.RepositoryPlacement, 0, len(placements))
	for index, repository := range repositories {
		if err := checkRepositoryAdmissionOwnership(ctx, check); err != nil {
			return nil, err
		}
		target := filepath.Join(workspacePath, repository.name)
		var created *createdWorktree
		if _, err := os.Stat(target); err == nil {
			matched, matchErr := sameGitCommonDirectoryContext(ctx, repository.path, target)
			if matchErr != nil {
				return nil, matchErr
			}
			if !matched {
				return nil, sourcecontrol.ErrCheckoutConflict
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		} else {
			worktree, createErr := createWorkspaceWorktreeContext(ctx, repository, target, workspaceBranch)
			if createErr != nil {
				return nil, createErr
			}
			created = &worktree
		}
		if err := checkRepositoryAdmissionOwnership(ctx, check); err != nil {
			if created != nil {
				cleanupAttachedWorktrees([]createdWorktree{*created})
			}
			return nil, err
		}
		result = append(result, placements[index])
	}
	return result, nil
}

func sameGitCommonDirectoryContext(ctx context.Context, sourcePath, checkoutPath string) (bool, error) {
	resolve := func(repositoryPath string) (string, error) {
		output, err := runWorkspaceGitContext(ctx, repositoryPath, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return "", err
		}
		common := filepath.Clean(strings.TrimSpace(output))
		if !filepath.IsAbs(common) {
			common = filepath.Join(repositoryPath, common)
		}
		return filepath.EvalSymlinks(common)
	}
	sourceCommon, err := resolve(sourcePath)
	if err != nil {
		return false, err
	}
	checkoutCommon, err := resolve(checkoutPath)
	if err != nil {
		return false, err
	}
	return sourceCommon == checkoutCommon, nil
}

func (local *repositoryAdmissionLocalWorkspace) Replay(ctx context.Context, record *repositoryadmission.Record, workspacePath string, createsWorkspace bool) (repositoryadmission.Result, error) {
	if !validCommittedRepositoryAdmissionReplay(record, createsWorkspace) {
		return repositoryadmission.Result{}, repositoryadmission.ErrInvalid
	}
	repositories := make([]repositoryadmission.RepositoryPlacement, 0, len(record.Receipt.Repositories))
	for _, receipt := range record.Receipt.Repositories {
		repository := receipt.Repository
		repositories = append(repositories, repositoryadmission.RepositoryPlacement{
			Name: repository.Name, Path: filepath.Join(workspacePath, repository.Name),
			Remote: repository.Remote, DefaultBranch: repository.DefaultBranch,
			SourceRepoID: repository.SourceRepoID,
		})
	}
	if err := saveLocalWorkspaceState(record.WorkspaceKey, workspacePath, repositories); err != nil {
		return repositoryadmission.Result{}, err
	}
	if createsWorkspace {
		if err := seedBuiltInRoles(ctx, local.agents, record.WorkspaceKey, workspacePath); err != nil {
			return repositoryadmission.Result{}, err
		}
	}
	return repositoryadmission.Result{WorkspaceID: record.WorkspaceKey, WorkspacePath: workspacePath}, nil
}

func validCommittedRepositoryAdmissionReplay(record *repositoryadmission.Record, createsWorkspace bool) bool {
	if record == nil || record.State != "committed" || record.Receipt == nil || len(record.Receipt.Repositories) == 0 {
		return false
	}
	finalization := record.Receipt.WorkspaceFinalization
	if !createsWorkspace {
		return finalization == nil
	}
	return finalization != nil && finalization.State == "ready" && finalization.DefaultBranch == record.Receipt.Repositories[0].Repository.DefaultBranch
}

func (local *repositoryAdmissionLocalWorkspace) VerifyRecoveryIntent(ctx context.Context, intent repositoryadmission.LocalIntent) error {
	switch intent.Kind {
	case repositoryadmission.KindCreateWorkspace:
		plan, err := local.PlanCreate(ctx, repositoryadmission.CreateCommand{
			Name: intent.WorkspaceName, Type: "clone", Path: intent.WorkspacePath,
			Branch: intent.Branch, CloneURLs: append([]string(nil), intent.CloneURLs...),
		})
		if err != nil {
			return err
		}
		return verifyLocalIntentOperation(intent, plan.WorkspaceKey, plan.WorkspacePath, plan.Repositories, "create_workspace")
	case repositoryadmission.KindAddRepositories:
		plan, err := local.PlanAdd(ctx, repositoryadmission.AddRepositoriesCommand{
			WorkspaceID: intent.WorkspaceKey, Repos: append([]string(nil), intent.LocalRepoPaths...),
			CloneURLs: append([]string(nil), intent.CloneURLs...), Branch: intent.Branch,
		})
		if err != nil {
			return err
		}
		return verifyLocalIntentOperation(intent, plan.WorkspaceKey, plan.WorkspacePath, plan.Repositories, "add_repositories")
	default:
		return repositoryadmission.ErrInvalid
	}
}

func verifyLocalIntentOperation(intent repositoryadmission.LocalIntent, workspaceKey, workspacePath string, specs []repositoryadmission.RepositorySpec, kind string) error {
	if workspaceKey != intent.WorkspaceKey || filepath.Clean(workspacePath) != filepath.Clean(intent.WorkspacePath) {
		return repositoryadmission.ErrInvalid
	}
	operationID, err := repositoryadmission.OperationID(kind, workspaceKey, workspacePath, specs)
	if err != nil {
		return err
	}
	if operationID != intent.OperationID {
		return repositoryadmission.ErrConflict
	}
	return nil
}

func checkRepositoryAdmissionOwnership(ctx context.Context, check repositoryadmission.OwnershipCheck) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if check == nil {
		return nil
	}
	if err := check(ctx); err != nil {
		return err
	}
	return context.Cause(ctx)
}

func cloneAdmissionSpecs(planned []plannedCloneRepo, requestedBranch string) []repositoryadmission.RepositorySpec {
	specs := make([]repositoryadmission.RepositorySpec, 0, len(planned))
	for _, repository := range planned {
		specs = append(specs, repositoryadmission.RepositorySpec{
			Name: repository.name, RemoteURL: repository.remoteURL,
			Remote: "origin", DefaultBranch: strings.TrimSpace(requestedBranch),
			SourceRepoID: repository.sourceRepoID,
		})
	}
	return specs
}

var _ repositoryadmission.LocalWorkspace = (*repositoryAdmissionLocalWorkspace)(nil)

// Keep errors imported for callers that classify local planning conflicts.
var _ = errors.Is
