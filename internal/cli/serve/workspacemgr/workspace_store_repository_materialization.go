package workspacemgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr/admissionstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

type plannedCloneRepo struct {
	name         string
	remoteURL    string
	sourceRepoID string
}

type preparedCloneRepo struct {
	config config.RepoConfig
	reused bool
}

type repositoryAdmissionOwnershipCheck func(context.Context) error

func checkRepositoryAdmissionOwnership(
	ctx context.Context,
	check repositoryAdmissionOwnershipCheck,
) error {
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

// materializeAddReposClones registers token-free repository intent before
// asking Source Control to materialize it. The owner resolves provider
// credentials behind its Connectors broker; Workspace receives only a local
// receipt and rolls back every record/checkout it created on a normal error.
//
//nolint:cyclop,funlen // Clone materialization retains fail-closed Source Control checks and rollback at each repository boundary.
func materializeAddReposClones(
	ctx context.Context,
	key string,
	admission *infrafleetdb.RepositoryAdmissionRecord,
	cloneURLs []string,
	wsDir string,
	seen map[string]bool,
	created []createdWorktree,
	requestedBranch string,
	materializer repositoryCheckoutMaterializer,
	checkOwnership repositoryAdmissionOwnershipCheck,
) ([]config.RepoConfig, []config.RepoConfig, error) {
	if len(cloneURLs) == 0 {
		return nil, nil, nil
	}
	if materializer == nil {
		cleanupAttachedWorktrees(created)
		return nil, nil, workspaceerrors.New(
			workspaceerrors.SecurityViolation,
			"repository clone requires the Source Control capability",
			sourcecontrol.ErrUnavailable,
		)
	}
	if admission == nil ||
		admission.AdmissionID == "" ||
		admission.WorkspaceKey != key {
		cleanupAttachedWorktrees(created)
		return nil, nil, repositoryAdmissionUnavailable()
	}
	admissionID := admission.AdmissionID
	planned, err := planCloneRepos(cloneURLs, seen)
	if err != nil {
		cleanupAttachedWorktrees(created)
		return nil, nil, err
	}

	prepared := make([]preparedCloneRepo, 0, len(planned))
	// A successfully verified checkout is durable recovery progress. Never
	// delete it because a later repository, branch check, local-state write, or
	// FleetDB response failed; the owner-fenced retry reuses the same checkout.
	rollback := func() {}

	for _, repository := range planned {
		if err := checkRepositoryAdmissionOwnership(ctx, checkOwnership); err != nil {
			return nil, nil, err
		}
		receipt, err := materializer.PrepareRepositoryAdmissionCheckout(
			ctx,
			sourcecontrol.RepositoryAdmissionCheckoutCommand{
				WorkspaceKey:      key,
				AdmissionID:       admissionID,
				RepositoryRef:     repository.sourceRepoID,
				OwnerID:           admission.OwnerID,
				OwnerGenerationID: admission.OwnerGenerationID,
				SpecFingerprint:   admission.SpecFingerprint,
			},
		)
		if err != nil {
			rollback()
			return nil, nil, workspaceerrors.New(
				workspaceerrors.GitFailed,
				fmt.Sprintf("materialize repository %q through Source Control", repository.name),
				err,
			)
		}
		if err := checkRepositoryAdmissionOwnership(ctx, checkOwnership); err != nil {
			return nil, nil, err
		}
		expectedPath := filepath.Join(wsDir, repository.name)
		if receipt == nil ||
			receipt.WorkspaceKey != key ||
			receipt.AdmissionID != admissionID ||
			receipt.RepositoryRef != repository.sourceRepoID ||
			filepath.Clean(receipt.CheckoutPath) != expectedPath {
			rollback()
			return nil, nil, workspaceerrors.New(
				workspaceerrors.SecurityViolation,
				fmt.Sprintf("Source Control returned divergent checkout coordinates for %q", repository.name),
				sourcecontrol.ErrInvalidMaterialization,
			)
		}
		prepared = append(prepared, preparedCloneRepo{
			config: config.RepoConfig{
				Name:         repository.name,
				Path:         receipt.CheckoutPath,
				Remote:       "origin",
				SourceRepoID: repository.sourceRepoID,
			},
			reused: receipt.Reused,
		})
		defaultBranch, err := detectRepoDefaultBranch(receipt.CheckoutPath)
		if err != nil {
			rollback()
			return nil, nil, workspaceerrors.New(
				workspaceerrors.GitFailed,
				fmt.Sprintf("detect default branch for cloned repo %q", repository.name),
				err,
			)
		}
		if err := checkRepositoryAdmissionOwnership(ctx, checkOwnership); err != nil {
			return nil, nil, err
		}
		prepared[len(prepared)-1].config.DefaultBranch = defaultBranch
	}

	cloned := make([]config.RepoConfig, 0, len(prepared))
	for _, repository := range prepared {
		cloned = append(cloned, repository.config)
	}
	if err := applyRequestedCloneBranch(cloned, requestedBranch); err != nil {
		rollback()
		return nil, nil, workspaceerrors.New(
			workspaceerrors.GitFailed,
			"validate cloned repository default branch",
			err,
		)
	}
	clonesToCleanup := make([]config.RepoConfig, 0, len(prepared))
	for _, repository := range prepared {
		if !repository.reused {
			clonesToCleanup = append(clonesToCleanup, repository.config)
		}
	}
	return cloned, clonesToCleanup, nil
}

func planCloneRepos(cloneURLs []string, seen map[string]bool) ([]plannedCloneRepo, error) {
	if seen == nil {
		seen = make(map[string]bool)
	}
	planned := make([]plannedCloneRepo, 0, len(cloneURLs))
	for _, rawRemote := range cloneURLs {
		remoteURL, err := sourcecontrol.ValidateTokenFreeRemote(rawRemote)
		if err != nil {
			return nil, workspaceerrors.New(
				workspaceerrors.SecurityViolation,
				"repository remote must be canonical and credential-free",
				err,
			)
		}
		name := deduplicateRepoName(repoNameFromURL(remoteURL), seen)
		seen[name] = true
		planned = append(planned, plannedCloneRepo{
			name:         name,
			remoteURL:    remoteURL,
			sourceRepoID: name,
		})
	}
	return planned, nil
}

func workspaceRepositoryOperationID(workspaceKey, repositoryRef, remoteURL string) string {
	hash := sha256.New()
	for _, value := range []string{workspaceKey, repositoryRef, remoteURL} {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return "workspace-repository:" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func classifyStoreRepoCreateError(name string, err error) error {
	if errors.Is(err, domain.ErrAlreadyExists) {
		return workspaceerrors.New(
			workspaceerrors.AlreadyExists,
			fmt.Sprintf(
				"repository name %q is already registered in this workspace",
				name,
			),
			err,
		)
	}
	return fmt.Errorf("create repo %q in store: %w", name, err)
}

// persistAddReposRecords writes the fleet-db repo records for each new
// repo and saves the local-state file. On any failure it rolls back the
// store records, attached worktrees, and clone directories so the caller
// is left with the pre-call state.
func persistAddReposRecords(
	ctx context.Context,
	s admissionstore.Store,
	key, wsDir, branch string,
	reposToCreate []config.RepoConfig,
	allRepos []config.RepoConfig,
	created []createdWorktree,
	clonedRepos []config.RepoConfig,
	clonesToCleanup []config.RepoConfig,
) error {
	var storeRepos []string
	rollback := func() {
		for _, repository := range clonedRepos {
			if err := s.Repos().Delete(context.Background(), key, repository.Name); err != nil &&
				!errors.Is(err, domain.ErrNotFound) {
				slog.Warn("failed to rollback store repo create", "workspace", key, "repo", repository.Name, "err", err)
			}
			removeLocalRepoState(key, repository.Name)
		}
		for _, name := range storeRepos {
			if err := s.Repos().Delete(context.Background(), key, name); err != nil && !errors.Is(err, domain.ErrNotFound) {
				slog.Warn("failed to rollback store repo create", "workspace", key, "repo", name, "err", err)
			}
		}
		cleanupAttachedWorktrees(created)
		cleanupClonedRepos(clonesToCleanup)
	}

	for _, r := range reposToCreate {
		if err := createStoreRepo(ctx, s, key, branch, r); err != nil {
			rollback()
			return err
		}
		storeRepos = append(storeRepos, r.Name)
	}
	if err := saveLocalWorkspaceState(key, wsDir, allRepos, true); err != nil {
		rollback()
		return err
	}
	return nil
}

func createStoreRepo(ctx context.Context, s admissionstore.Store, key, branch string, r config.RepoConfig) error {
	remoteName := r.Remote
	if remoteName == "" {
		remoteName = "origin"
	}
	remoteURL, err := persistentGitRemoteURL(r.Path, remoteName)
	if err != nil {
		return err
	}
	defaultBranch := strings.TrimSpace(r.DefaultBranch)
	if defaultBranch == "" {
		defaultBranch = strings.TrimSpace(branch)
	}
	if _, err := s.Repos().Create(ctx, storepkg.RepoCreate{
		WorkspaceKey:  key,
		Name:          r.Name,
		RemoteURL:     remoteURL,
		Remote:        remoteName,
		DefaultBranch: defaultBranch,
		SourceRepoID:  r.SourceRepoID,
	}); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return workspaceerrors.New(
				workspaceerrors.AlreadyExists,
				fmt.Sprintf("repository name %q is already registered in this workspace", r.Name),
				err,
			)
		}
		return fmt.Errorf("create repo %q in store: %w", r.Name, err)
	}
	return nil
}

func localWorkspacePath(key string) (string, error) {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return "", fmt.Errorf("load local workspace state: %w", err)
	}
	if sc != nil {
		if local, ok := sc.Workspaces[key]; ok && strings.TrimSpace(local.Path) != "" {
			return local.Path, nil
		}
	}
	return "", workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("workspace %q has no local path; open it on this machine before adding repos", key), nil)
}

func persistentGitRemoteURL(repoPath, remote string) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	out, err := cli.RunGitCommand(repoPath, "remote", "get-url", remote)
	if err != nil {
		// A local repository without an origin is still a real, token-free
		// source. Persist its clean absolute source path rather than inventing
		// a network remote or publishing an empty FleetDB repository identity.
		sourcePath := filepath.Clean(strings.TrimSpace(repoPath))
		if !filepath.IsAbs(sourcePath) ||
			sourcePath == "." ||
			sourcePath == string(filepath.Separator) {
			return "", workspaceerrors.New(
				workspaceerrors.SecurityViolation,
				fmt.Sprintf("repository %q source path is unsafe", filepath.Base(repoPath)),
				err,
			)
		}
		return sourcePath, nil
	}
	remoteURL := strings.TrimSpace(out)
	if remoteURL == "" {
		return persistentGitRemoteURLWithoutConfiguredRemote(repoPath)
	}
	validated, err := sourcecontrol.ValidateTokenFreeRemote(remoteURL)
	if err != nil {
		return "", workspaceerrors.New(
			workspaceerrors.SecurityViolation,
			fmt.Sprintf("repository %q remote must be credential-free", filepath.Base(repoPath)),
			err,
		)
	}
	return validated, nil
}

func persistentGitRemoteURLWithoutConfiguredRemote(repoPath string) (string, error) {
	sourcePath := filepath.Clean(strings.TrimSpace(repoPath))
	if !filepath.IsAbs(sourcePath) ||
		sourcePath == "." ||
		sourcePath == string(filepath.Separator) {
		return "", workspaceerrors.New(
			workspaceerrors.SecurityViolation,
			fmt.Sprintf("repository %q source path is unsafe", filepath.Base(repoPath)),
			nil,
		)
	}
	return sourcePath, nil
}

//nolint:cyclop,funlen // Orchestrates clone lifecycle state, filesystem cleanup, and store writes.
func createStoreBackedCloneWorkspace(
	ctx context.Context,
	s admissionstore.Store,
	req service.WorkspaceCreateRequest,
	materializer repositoryCheckoutMaterializer,
) (service.WorkspaceCreateResult, error) {
	cloneURLs := req.CloneURLs
	if len(cloneURLs) == 0 {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.PathNotFound, "no clone URLs specified", nil)
	}
	if materializer == nil {
		return service.WorkspaceCreateResult{}, workspaceerrors.New(
			workspaceerrors.SecurityViolation,
			"repository clone requires the Source Control capability",
			sourcecontrol.ErrUnavailable,
		)
	}
	if err := ensureCloneWorkspaceNameAvailable(ctx, s, req.Name); err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	wsPlan, err := resolveWorkspaceDirForCreate(req.Path, req.Name)
	if err != nil {
		return service.WorkspaceCreateResult{}, err
	}
	wsDir := wsPlan.path
	branch := req.Branch
	if branch == "" {
		branch = "main"
	}
	key := service.WorkspaceKeyFromName(req.Name)
	if err := ensureCloneWorkspaceKeyAvailable(ctx, s, req.Name, key); err != nil {
		return service.WorkspaceCreateResult{}, err
	}

	if _, err := s.Workspaces().Create(ctx, storepkg.WorkspaceCreate{
		Key:           key,
		Name:          req.Name,
		DefaultBranch: branch,
	}); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), err)
		}
		return service.WorkspaceCreateResult{}, fmt.Errorf("create workspace in store: %w", err)
	}
	rollbackStore := func() {
		deleteLocalWorkspaceState(key)
		if err := s.Workspaces().Delete(context.Background(), key); err != nil && !errors.Is(err, domain.ErrNotFound) {
			slog.Warn("failed to rollback store clone workspace create", "workspace", key, "err", err)
		}
	}
	if err := seedBuiltInRoles(ctx, s, key, wsDir); err != nil {
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("seed built-in roles: %w", err)
	}
	_ = updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCreating)

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}
	if err := saveLocalWorkspaceState(key, wsDir, nil, true); err != nil {
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, err
	}

	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateCloning); err != nil {
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace cloning: %w", err)
	}
	repos, clonesToCleanup, err := materializeAddReposClones(
		ctx,
		key,
		nil,
		cloneURLs,
		wsDir,
		make(map[string]bool),
		nil,
		req.Branch,
		materializer,
		nil,
	)
	if err != nil {
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, err
	}
	detectedBranch := ""
	if strings.TrimSpace(req.Branch) == "" && len(repos) > 0 {
		branch = strings.TrimSpace(repos[0].DefaultBranch)
		if branch == "" {
			cleanupClonedRepos(clonesToCleanup)
			cleanupWorkspaceRoot(wsPlan)
			rollbackStore()
			return service.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.GitFailed, "cloned repository default branch is empty", nil)
		}
		detectedBranch = branch
	}

	if err := updateStoreWorkspaceStateAndDefaultBranch(ctx, s, key, domain.WorkspaceStateInitializing, detectedBranch); err != nil {
		cleanupClonedRepos(clonesToCleanup)
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace initializing: %w", err)
	}
	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		cleanupClonedRepos(clonesToCleanup)
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, err
	}
	if err := updateStoreWorkspaceState(ctx, s, key, domain.WorkspaceStateReady); err != nil {
		cleanupClonedRepos(clonesToCleanup)
		cleanupWorkspaceRoot(wsPlan)
		rollbackStore()
		return service.WorkspaceCreateResult{}, fmt.Errorf("mark workspace ready: %w", err)
	}

	return service.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

func ensureCloneWorkspaceNameAvailable(ctx context.Context, s admissionstore.Store, name string) error {
	existing, err := s.Workspaces().GetByName(ctx, name)
	if err == nil && existing != nil {
		return workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", name), nil)
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("check workspace name: %w", err)
	}
	return nil
}

func ensureCloneWorkspaceKeyAvailable(ctx context.Context, s admissionstore.Store, name, key string) error {
	_, err := s.Workspaces().Get(ctx, key)
	if err == nil {
		return workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", name), nil)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("check workspace key: %w", err)
	}
	return nil
}

func updateStoreWorkspaceState(ctx context.Context, s admissionstore.Store, key string, state domain.WorkspaceState) error {
	return updateStoreWorkspaceStateAndDefaultBranch(ctx, s, key, state, "")
}

// updateStoreWorkspaceStateAndDefaultBranch keeps workspace lifecycle and
// clone-derived branch persistence on the existing workspace mutation seam.
// An empty branch leaves the stored default unchanged.
func updateStoreWorkspaceStateAndDefaultBranch(ctx context.Context, s admissionstore.Store, key string, state domain.WorkspaceState, defaultBranch string) error {
	msg := ""
	update := storepkg.WorkspaceUpdate{
		State:        &state,
		ErrorMessage: &msg,
	}
	if defaultBranch = strings.TrimSpace(defaultBranch); defaultBranch != "" {
		update.DefaultBranch = &defaultBranch
	}
	_, err := s.Workspaces().Update(ctx, key, update)
	return err
}
