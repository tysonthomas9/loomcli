package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr/admissionstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

type cloneWorkspaceAdmissionPlan struct {
	key       string
	path      string
	specs     []infrafleetdb.RepositoryAdmissionRepoSpec
	cloneURLs []string
	record    *infrafleetdb.RepositoryAdmissionRecord
}

//nolint:funlen // Clone preparation validates workspace identity, repository specifications, and recovery journal intent before side effects.
func prepareStoreBackedCloneWorkspaceAdmission(
	ctx context.Context,
	req service.WorkspaceCreateRequest,
	process *repositoryAdmissionProcess,
) (cloneWorkspaceAdmissionPlan, error) {
	if len(req.CloneURLs) == 0 {
		return cloneWorkspaceAdmissionPlan{}, workspaceerrors.New(
			workspaceerrors.PathNotFound,
			"no clone URLs specified",
			nil,
		)
	}
	key := service.WorkspaceKeyFromName(req.Name)
	planned, err := planCloneRepos(req.CloneURLs, make(map[string]bool))
	if err != nil {
		return cloneWorkspaceAdmissionPlan{}, err
	}
	specs := cloneAdmissionSpecs(planned, req.Branch)
	cloneURLs := make([]string, 0, len(planned))
	for _, repository := range planned {
		cloneURLs = append(cloneURLs, repository.remoteURL)
	}
	wsPlan, err := process.resolveCreateWorkspacePath(
		ctx,
		req.Path,
		req.Name,
		key,
		specs,
	)
	if err != nil {
		return cloneWorkspaceAdmissionPlan{}, err
	}
	initialBranch := strings.TrimSpace(req.Branch)
	record, err := process.prepareCreate(
		ctx,
		infrafleetdb.RepositoryAdmissionWorkspaceInput{
			Key: key, Name: req.Name, State: string(domain.WorkspaceStateCreating),
			DefaultBranch: initialBranch,
		},
		wsPlan.path,
		specs,
		cloneURLs,
		req.Branch,
	)
	if err != nil {
		if errors.Is(err, infrafleetdb.ErrRepositoryAdmissionConflict) {
			return cloneWorkspaceAdmissionPlan{}, workspaceerrors.New(
				workspaceerrors.AlreadyExists,
				fmt.Sprintf("workspace %q or one of its repositories conflicts with an existing repository admission", req.Name),
				err,
			)
		}
		return cloneWorkspaceAdmissionPlan{}, err
	}
	return cloneWorkspaceAdmissionPlan{
		key: key, path: wsPlan.path, specs: specs,
		cloneURLs: cloneURLs, record: record,
	}, nil
}

//nolint:funlen,cyclop // One durable process coordinates FleetDB, local checkout state, roles, and terminal commit.
func createStoreBackedCloneWorkspaceAdmission(
	ctx context.Context,
	s admissionstore.Store,
	req service.WorkspaceCreateRequest,
	process *repositoryAdmissionProcess,
) (service.WorkspaceCreateResult, error) {
	plan, err := prepareStoreBackedCloneWorkspaceAdmission(ctx, req, process)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if plan.record.State == "committed" {
		process.forgetPreparedRepositoryAdmission(plan.record.AdmissionID)
		return replayCommittedRepositoryAdmission(ctx, s, plan.record, plan.path, true)
	}
	materializationCtx, ownedRecord, release, err := process.beginMaterialization(
		ctx,
		plan.record,
		plan.path,
	)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	defer release()
	plan.record = ownedRecord
	verifyOwnership := func(checkCtx context.Context) error {
		checkErr := process.verifyMaterializationOwnership(checkCtx, plan.record)
		return checkErr
	}
	if err := verifyOwnership(materializationCtx); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := os.MkdirAll(plan.path, 0o755); err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			fmt.Errorf("cannot create workspace directory: %w", err),
		)
	}
	if err := verifyOwnership(materializationCtx); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := saveLocalWorkspaceState(plan.key, plan.path, nil, true); err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			err,
		)
	}
	repositories, _, err := materializeAddReposClones(
		materializationCtx,
		plan.key,
		plan.record,
		plan.cloneURLs,
		plan.path,
		make(map[string]bool),
		nil,
		req.Branch,
		process.materialize,
		verifyOwnership,
	)
	if err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			err,
		)
	}
	if len(repositories) != len(plan.specs) {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			workspaceerrors.New(
				workspaceerrors.GitFailed,
				"one or more repository checkouts were not materialized",
				nil,
			),
		)
	}
	if err := verifyOwnership(materializationCtx); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := saveLocalWorkspaceState(plan.key, plan.path, repositories, true); err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			err,
		)
	}
	if err := verifyOwnership(materializationCtx); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := seedBuiltInRoles(materializationCtx, s, plan.key, plan.path); err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			fmt.Errorf("seed built-in roles: %w", err),
		)
	}
	defaultBranch := repositories[0].DefaultBranch
	if err := verifyOwnership(materializationCtx); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	committed, err := process.commit(
		materializationCtx,
		plan.record,
		repositories,
		&infrafleetdb.RepositoryAdmissionWorkspaceFinalization{
			State: "ready", DefaultBranch: defaultBranch,
		},
	)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if committed.State != "committed" {
		return service.WorkspaceCreateResult{}, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	return service.WorkspaceCreateResult{WorkspaceID: plan.key, WorkspacePath: plan.path}, nil
}

type addRepositoriesAdmissionPlan struct {
	key             string
	workspacePath   string
	branch          string
	localRepos      []resolvedRepo
	localConfigs    []config.RepoConfig
	materializeSeen map[string]bool
	specs           []infrafleetdb.RepositoryAdmissionRepoSpec
	cloneURLs       []string
	record          *infrafleetdb.RepositoryAdmissionRecord
}

//nolint:funlen,cyclop // Preparation resolves the exact complete batch before one durable Begin.
func prepareAddReposToStoreBackedWorkspaceAdmission(
	ctx context.Context,
	s admissionstore.Store,
	catalog workspacemodule.API,
	req service.WorkspaceAddReposRequest,
	process *repositoryAdmissionProcess,
) (addRepositoriesAdmissionPlan, error) {
	key, workspace, err := resolveWorkspaceForAddRepos(ctx, catalog, req.WorkspaceID)
	if err != nil {
		return addRepositoriesAdmissionPlan{}, err
	}
	workspacePath, err := prepareWorkspaceDir(key)
	if err != nil {
		return addRepositoriesAdmissionPlan{}, err
	}
	localRepos, err := resolveRequestRepos(req.Repos)
	if err != nil {
		return addRepositoriesAdmissionPlan{}, err
	}
	seen, err := dedupAddReposAgainstExisting(ctx, catalog, key, localRepos)
	if err != nil {
		return addRepositoriesAdmissionPlan{}, err
	}
	materializeSeen := make(map[string]bool, len(seen))
	for name, present := range seen {
		materializeSeen[name] = present
	}
	plannedClones, err := planCloneRepos(req.CloneURLs, seen)
	if err != nil {
		return addRepositoriesAdmissionPlan{}, err
	}
	branch := pickAddReposBranch(req.Branch, workspace, key)
	localConfigs, localSpecs, err := planLocalAdmissionRepos(
		localRepos,
		workspacePath,
		branch,
		req.Branch,
	)
	if err != nil {
		if errors.Is(err, infrafleetdb.ErrRepositoryAdmissionConflict) {
			return addRepositoriesAdmissionPlan{}, workspaceerrors.New(
				workspaceerrors.AlreadyExists,
				fmt.Sprintf("one or more repositories conflict with an existing repository admission in workspace %q", key),
				err,
			)
		}
		return addRepositoriesAdmissionPlan{}, err
	}
	specs := append(
		append([]infrafleetdb.RepositoryAdmissionRepoSpec(nil), localSpecs...),
		cloneAdmissionSpecs(plannedClones, req.Branch)...,
	)
	cloneURLs := make([]string, 0, len(plannedClones))
	for _, repository := range plannedClones {
		cloneURLs = append(cloneURLs, repository.remoteURL)
	}
	localRepoPaths := make([]string, 0, len(localRepos))
	for _, repository := range localRepos {
		localRepoPaths = append(localRepoPaths, repository.path)
	}
	record, err := process.prepareExisting(
		ctx,
		key,
		workspacePath,
		specs,
		cloneURLs,
		localRepoPaths,
		req.Branch,
	)
	if err != nil {
		return addRepositoriesAdmissionPlan{}, err
	}
	return addRepositoriesAdmissionPlan{
		key: key, workspacePath: workspacePath, branch: branch,
		localRepos: localRepos, localConfigs: localConfigs,
		materializeSeen: materializeSeen, specs: specs,
		cloneURLs: cloneURLs, record: record,
	}, nil
}

//nolint:funlen,cyclop // Existing-workspace admission spans local worktrees, Source Control, and one FleetDB commit.
func addReposToStoreBackedWorkspaceAdmission(
	ctx context.Context,
	s admissionstore.Store,
	catalog workspacemodule.API,
	req service.WorkspaceAddReposRequest,
	process *repositoryAdmissionProcess,
) (service.WorkspaceCreateResult, error) {
	plan, err := prepareAddReposToStoreBackedWorkspaceAdmission(
		ctx,
		s,
		catalog,
		req,
		process,
	)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if plan.record.State == "committed" {
		process.forgetPreparedRepositoryAdmission(plan.record.AdmissionID)
		return replayCommittedRepositoryAdmission(
			ctx,
			s,
			plan.record,
			plan.workspacePath,
			false,
		)
	}
	materializationCtx, ownedRecord, release, err := process.beginMaterialization(
		ctx,
		plan.record,
		plan.workspacePath,
	)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	defer release()
	plan.record = ownedRecord
	verifyOwnership := func(checkCtx context.Context) error {
		checkErr := process.verifyMaterializationOwnership(checkCtx, plan.record)
		return checkErr
	}
	materializedLocal, err := materializeLocalAdmissionRepos(
		materializationCtx,
		plan.workspacePath,
		plan.branch,
		plan.localRepos,
		plan.localConfigs,
		verifyOwnership,
	)
	if err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			err,
		)
	}
	cloned, _, err := materializeAddReposClones(
		materializationCtx,
		plan.key,
		plan.record,
		plan.cloneURLs,
		plan.workspacePath,
		plan.materializeSeen,
		nil,
		req.Branch,
		process.materialize,
		verifyOwnership,
	)
	if err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			err,
		)
	}
	repositories := make([]config.RepoConfig, 0, len(materializedLocal)+len(cloned))
	repositories = append(repositories, materializedLocal...)
	repositories = append(repositories, cloned...)
	if len(repositories) != len(plan.specs) {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			workspaceerrors.New(
				workspaceerrors.GitFailed,
				"one or more repository checkouts were not materialized",
				nil,
			),
		)
	}
	if err := verifyOwnership(materializationCtx); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if err := saveLocalWorkspaceState(
		plan.key,
		plan.workspacePath,
		repositories,
		true,
	); err != nil {
		return service.WorkspaceCreateResult{}, process.failMaterialization(
			materializationCtx,
			plan.record,
			err,
		)
	}
	if err := verifyOwnership(materializationCtx); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if _, err := process.commit(
		materializationCtx,
		plan.record,
		repositories,
		nil,
	); err != nil {
		if errors.Is(err, infrafleetdb.ErrRepositoryAdmissionConflict) {
			return service.WorkspaceCreateResult{}, workspaceerrors.New(
				workspaceerrors.AlreadyExists,
				"one or more repository names are already registered in this workspace",
				err,
			)
		}
		return service.WorkspaceCreateResult{}, err
	}
	return service.WorkspaceCreateResult{
		WorkspaceID: plan.key, WorkspacePath: plan.workspacePath,
	}, nil
}

func planLocalAdmissionRepos(
	repositories []resolvedRepo,
	workspacePath,
	workspaceBranch,
	defaultBranchOverride string,
) ([]config.RepoConfig, []infrafleetdb.RepositoryAdmissionRepoSpec, error) {
	configs := make([]config.RepoConfig, 0, len(repositories))
	specs := make([]infrafleetdb.RepositoryAdmissionRepoSpec, 0, len(repositories))
	for _, repository := range repositories {
		repositoryConfig, err := worktreeRepoConfig(
			repository,
			filepath.Join(workspacePath, repository.name),
			defaultBranchOverride,
		)
		if err != nil {
			return nil, nil, err
		}
		if repositoryConfig.DefaultBranch == "" {
			repositoryConfig.DefaultBranch = workspaceBranch
		}
		remoteURL, err := persistentGitRemoteURL(repository.path, "origin")
		if err != nil {
			return nil, nil, err
		}
		configs = append(configs, repositoryConfig)
		specs = append(specs, infrafleetdb.RepositoryAdmissionRepoSpec{
			Name: repository.name, RemoteURL: remoteURL, Remote: "origin",
			DefaultBranch: repositoryConfig.DefaultBranch,
			SourceRepoID:  repository.name,
		})
	}
	return configs, specs, nil
}

//nolint:funlen // Local worktree materialization rechecks ownership before and after every filesystem publication and cleanup point.
func materializeLocalAdmissionRepos(
	ctx context.Context,
	workspacePath,
	workspaceBranch string,
	repositories []resolvedRepo,
	configs []config.RepoConfig,
	checkOwnership repositoryAdmissionOwnershipCheck,
) ([]config.RepoConfig, error) {
	if len(repositories) != len(configs) {
		return nil, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	result := make([]config.RepoConfig, 0, len(configs))
	for index, repository := range repositories {
		if err := checkRepositoryAdmissionOwnership(ctx, checkOwnership); err != nil {
			return nil, err
		}
		target := filepath.Join(workspacePath, repository.name)
		var created *createdWorktree
		if _, err := os.Stat(target); err == nil {
			matched, matchErr := sameGitCommonDirectoryContext(
				ctx,
				repository.path,
				target,
			)
			if matchErr != nil {
				return nil, matchErr
			}
			if !matched {
				return nil, sourcecontrol.ErrCheckoutConflict
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		} else if worktree, err := createWorkspaceWorktreeContext(
			ctx,
			repository,
			target,
			workspaceBranch,
		); err != nil {
			return nil, err
		} else {
			created = &worktree
		}
		if err := checkRepositoryAdmissionOwnership(ctx, checkOwnership); err != nil {
			if created != nil {
				cleanupAttachedWorktrees([]createdWorktree{*created})
			}
			return nil, err
		}
		result = append(result, configs[index])
		// A local worktree is durable retry progress, not published topology.
		// The outer admission writes the complete local+remote set only after
		// every member has materialized and exact ownership is rechecked.
		if err := checkRepositoryAdmissionOwnership(ctx, checkOwnership); err != nil {
			if created != nil {
				cleanupAttachedWorktrees([]createdWorktree{*created})
			}
			return nil, err
		}
	}
	return result, nil
}

func sameGitCommonDirectory(sourcePath, checkoutPath string) (bool, error) {
	return sameGitCommonDirectoryContext(
		context.Background(),
		sourcePath,
		checkoutPath,
	)
}

func sameGitCommonDirectoryContext(
	ctx context.Context,
	sourcePath,
	checkoutPath string,
) (bool, error) {
	resolve := func(repositoryPath string) (string, error) {
		output, err := runWorkspaceGitContext(
			ctx,
			repositoryPath,
			"rev-parse",
			"--path-format=absolute",
			"--git-common-dir",
		)
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

func replayCommittedRepositoryAdmission(
	ctx context.Context,
	s admissionstore.Store,
	record *infrafleetdb.RepositoryAdmissionRecord,
	workspacePath string,
	seedRoles bool,
) (service.WorkspaceCreateResult, error) {
	if !validCommittedRepositoryAdmissionReplay(record, seedRoles) {
		return service.WorkspaceCreateResult{}, infrafleetdb.ErrRepositoryAdmissionInvalid
	}
	repositories := make([]config.RepoConfig, 0, len(record.Receipt.Repositories))
	for _, receipt := range record.Receipt.Repositories {
		repository := receipt.Repository
		repositories = append(repositories, config.RepoConfig{
			Name: repository.Name, Path: filepath.Join(workspacePath, repository.Name),
			Remote: repository.Remote, DefaultBranch: repository.DefaultBranch,
			SourceRepoID: repository.SourceRepoID,
		})
	}
	if err := saveLocalWorkspaceState(
		record.WorkspaceKey,
		workspacePath,
		repositories,
		true,
	); err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	if seedRoles {
		if err := seedBuiltInRoles(
			ctx,
			s,
			record.WorkspaceKey,
			workspacePath,
		); err != nil {
			return service.WorkspaceCreateResult{}, err
		}
	}
	return service.WorkspaceCreateResult{
		WorkspaceID: record.WorkspaceKey, WorkspacePath: workspacePath,
	}, nil
}

func validCommittedRepositoryAdmissionReplay(
	record *infrafleetdb.RepositoryAdmissionRecord,
	createsWorkspace bool,
) bool {
	if record == nil ||
		record.State != "committed" ||
		record.Receipt == nil ||
		len(record.Receipt.Repositories) == 0 {
		return false
	}
	finalization := record.Receipt.WorkspaceFinalization
	if !createsWorkspace {
		return finalization == nil
	}
	return finalization != nil &&
		finalization.State == "ready" &&
		finalization.DefaultBranch ==
			record.Receipt.Repositories[0].Repository.DefaultBranch
}
