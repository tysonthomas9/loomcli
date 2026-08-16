package repositoryadmissioninfra

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/repositoryadmission"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type plannedCloneRepo struct {
	name         string
	remoteURL    string
	sourceRepoID string
}

type preparedCloneRepo struct {
	config repositoryadmission.RepositoryPlacement
	reused bool
}

type repositoryAdmissionOwnershipCheck = repositoryadmission.OwnershipCheck

// materializeAddReposClones registers token-free repository intent before
// asking Source Control to materialize it. The owner resolves provider
// credentials behind its Connectors broker; Workspace receives only a local
// receipt and rolls back every record/checkout it created on a normal error.
//
//nolint:cyclop,funlen // Clone materialization retains fail-closed Source Control checks and rollback at each repository boundary.
func materializeAddReposClones(
	ctx context.Context,
	key string,
	admission *repositoryadmission.Record,
	cloneURLs []string,
	wsDir string,
	seen map[string]bool,
	created []createdWorktree,
	requestedBranch string,
	materializer RepositoryCheckoutMaterializer,
	checkOwnership repositoryAdmissionOwnershipCheck,
) ([]repositoryadmission.RepositoryPlacement, []repositoryadmission.RepositoryPlacement, error) {
	if len(cloneURLs) == 0 {
		return nil, nil, nil
	}
	if materializer == nil {
		cleanupAttachedWorktrees(created)
		return nil, nil, workspacemodule.NewCreateError(
			workspacemodule.SecurityViolation,
			"repository clone requires the Source Control capability",
			sourcecontrol.ErrUnavailable,
		)
	}
	if admission == nil ||
		admission.AdmissionID == "" ||
		admission.WorkspaceKey != key {
		cleanupAttachedWorktrees(created)
		return nil, nil, repositoryadmission.ErrUnavailable
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
			return nil, nil, workspacemodule.NewCreateError(
				workspacemodule.GitFailed,
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
			return nil, nil, workspacemodule.NewCreateError(
				workspacemodule.SecurityViolation,
				fmt.Sprintf("Source Control returned divergent checkout coordinates for %q", repository.name),
				sourcecontrol.ErrInvalidMaterialization,
			)
		}
		prepared = append(prepared, preparedCloneRepo{
			config: repositoryadmission.RepositoryPlacement{
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
			return nil, nil, workspacemodule.NewCreateError(
				workspacemodule.GitFailed,
				fmt.Sprintf("detect default branch for cloned repo %q", repository.name),
				err,
			)
		}
		if err := checkRepositoryAdmissionOwnership(ctx, checkOwnership); err != nil {
			return nil, nil, err
		}
		prepared[len(prepared)-1].config.DefaultBranch = defaultBranch
	}

	cloned := make([]repositoryadmission.RepositoryPlacement, 0, len(prepared))
	for _, repository := range prepared {
		cloned = append(cloned, repository.config)
	}
	if err := applyRequestedCloneBranch(cloned, requestedBranch); err != nil {
		rollback()
		return nil, nil, workspacemodule.NewCreateError(
			workspacemodule.GitFailed,
			"validate cloned repository default branch",
			err,
		)
	}
	clonesToCleanup := make([]repositoryadmission.RepositoryPlacement, 0, len(prepared))
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
			return nil, workspacemodule.NewCreateError(
				workspacemodule.SecurityViolation,
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

// persistAddReposRecords writes the fleet-db repo records for each new
// repo and saves the local-state file. On any failure it rolls back the
// store records, attached worktrees, and clone directories so the caller
// is left with the pre-call state.
func persistAddReposRecords(
	ctx context.Context,
	catalog workspacemodule.API,
	key, wsDir, branch string,
	reposToCreate []repositoryadmission.RepositoryPlacement,
	allRepos []repositoryadmission.RepositoryPlacement,
	created []createdWorktree,
	clonedRepos []repositoryadmission.RepositoryPlacement,
	clonesToCleanup []repositoryadmission.RepositoryPlacement,
) error {
	var storeRepos []string
	rollback := func() {
		for _, repository := range clonedRepos {
			if _, err := catalog.UnregisterRepository(context.Background(), workspacemodule.UnregisterRepositoryCommand{WorkspaceReference: key, Name: repository.Name}); err != nil &&
				!errors.Is(err, workspacemodule.ErrNotFound) {
				slog.Warn("failed to rollback store repo create", "workspace", key, "repo", repository.Name, "err", err)
			}
			removeLocalRepoState(key, repository.Name)
		}
		for _, name := range storeRepos {
			if _, err := catalog.UnregisterRepository(context.Background(), workspacemodule.UnregisterRepositoryCommand{WorkspaceReference: key, Name: name}); err != nil && !errors.Is(err, workspacemodule.ErrNotFound) {
				slog.Warn("failed to rollback store repo create", "workspace", key, "repo", name, "err", err)
			}
		}
		cleanupAttachedWorktrees(created)
		cleanupClonedRepos(clonesToCleanup)
	}

	for _, r := range reposToCreate {
		if err := createStoreRepo(ctx, catalog, key, branch, r); err != nil {
			rollback()
			return err
		}
		storeRepos = append(storeRepos, r.Name)
	}
	if err := saveLocalWorkspaceState(key, wsDir, allRepos); err != nil {
		rollback()
		return err
	}
	return nil
}

func createStoreRepo(ctx context.Context, catalog workspacemodule.API, key, branch string, r repositoryadmission.RepositoryPlacement) error {
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
	if _, err := catalog.RegisterRepository(ctx, workspacemodule.RegisterRepositoryCommand{
		WorkspaceReference: key,
		Name:               r.Name,
		RemoteURL:          remoteURL,
		Remote:             remoteName,
		DefaultBranch:      defaultBranch,
		SourceRepoID:       r.SourceRepoID,
	}); err != nil {
		if errors.Is(err, workspacemodule.ErrConflict) {
			return workspacemodule.NewCreateError(
				workspacemodule.AlreadyExists,
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
	return "", workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("workspace %q has no local path; open it on this machine before adding repos", key), nil)
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
			return "", workspacemodule.NewCreateError(
				workspacemodule.SecurityViolation,
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
		return "", workspacemodule.NewCreateError(
			workspacemodule.SecurityViolation,
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
		return "", workspacemodule.NewCreateError(
			workspacemodule.SecurityViolation,
			fmt.Sprintf("repository %q source path is unsafe", filepath.Base(repoPath)),
			nil,
		)
	}
	return sourcePath, nil
}
