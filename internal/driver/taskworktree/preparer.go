// Package taskworktree owns the machine-local preparation of a task-run
// worktree. Repo selection and lineage policy remain with the driver package;
// this boundary contains only local state, checkout, and worktree filesystem
// mechanics.
package taskworktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/gitauth"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// Preparer holds the workspace-local state used throughout one task worktree
// preparation.
type Preparer struct {
	workspaceKey string
	local        bootstrap.WorkspaceLocalState
	credentials  gitauth.Source
}

// Open loads and validates the machine-local state for workspaceKey.
func Open(workspaceKey string) (*Preparer, error) {
	return OpenWithCredentials(workspaceKey, nil)
}

// OpenWithLocalSettings loads machine-local state and resolves private HTTPS
// credentials from localSettingsDir just in time for each git operation. An
// empty directory preserves anonymous/SSH/local git behavior.
func OpenWithLocalSettings(workspaceKey, localSettingsDir string) (*Preparer, error) {
	return OpenWithCredentials(workspaceKey, gitauth.NewLocalSettingsSource(localSettingsDir))
}

// OpenWithCredentials loads machine-local state and retains a credential
// source for private clone/fetch operations. The source resolves plaintext
// just in time; Preparer never caches credentials.
func OpenWithCredentials(workspaceKey string, credentials gitauth.Source) (*Preparer, error) {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return nil, fmt.Errorf("load local workspace state: %w", err)
	}
	if sc == nil || sc.Workspaces == nil {
		return nil, fmt.Errorf("workspace %q has no local state", workspaceKey)
	}
	local, ok := sc.Workspaces[workspaceKey]
	if !ok {
		return nil, fmt.Errorf("workspace %q has no local state", workspaceKey)
	}
	if strings.TrimSpace(local.Path) == "" {
		return nil, fmt.Errorf("workspace %q has no local path in loom state", workspaceKey)
	}
	return &Preparer{workspaceKey: workspaceKey, local: local, credentials: credentials}, nil
}

// Prepare ensures repo has a local checkout and creates taskRunID's detached
// worktree. resolveBase is evaluated after checkout preparation, preserving the
// driver's existing ordering for lineage lookup and filesystem side effects.
func (p *Preparer) Prepare(ctx context.Context, repo *domain.Repo, taskRunID string, resolveBase func() string) (string, error) {
	repoPath, err := p.ensureRepoCheckout(ctx, repo)
	if err != nil {
		return "", err
	}
	target, err := localworkspace.TaskRunWorktreePath(p.local.Path, repo.Name, taskRunID)
	if err != nil {
		return "", err
	}
	baseBranch := ""
	if resolveBase != nil {
		baseBranch = resolveBase()
	}
	if err := localworkspace.EnsureDetachedGitWorktreeFromBranchWithCredentials(
		ctx, repoPath, target, repoRemote(repo), baseBranch, p.credentials,
	); err != nil {
		return "", fmt.Errorf("ensure task run worktree for repo %q: %w", repo.Name, err)
	}
	return target, nil
}

func (p *Preparer) ensureRepoCheckout(ctx context.Context, repo *domain.Repo) (string, error) {
	if repo == nil {
		return "", fmt.Errorf("repo required: %w", domain.ErrInvalid)
	}
	repoPath := strings.TrimSpace(localworkspace.RepoPath(p.local, repo.Name))
	if repoPath == "" {
		var err error
		repoPath, err = localworkspace.RepoCheckoutPath(p.local.Path, repo.Name)
		if err != nil {
			return "", err
		}
	}
	if isGitCheckout(repoPath) {
		return repoPath, nil
	}
	if _, err := os.Stat(repoPath); err == nil {
		return "", fmt.Errorf("repo %q path %s is not a git checkout", repo.Name, repoPath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat repo %q path %s: %w", repo.Name, repoPath, err)
	}
	if strings.TrimSpace(repo.RemoteURL) == "" {
		return "", fmt.Errorf("repo %q has no local checkout at %s and no remote URL to clone", repo.Name, repoPath)
	}
	if err := localworkspace.CloneRepoToWithCredentials(ctx, repo.RemoteURL, repoPath, p.credentials); err != nil {
		return "", err
	}
	if err := localworkspace.RememberRepoPath(p.workspaceKey, repo.Name, repoPath); err != nil {
		return "", fmt.Errorf("remember repo path: %w", err)
	}
	return repoPath, nil
}

func isGitCheckout(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	return false
}

func repoRemote(repo *domain.Repo) string {
	if repo == nil || strings.TrimSpace(repo.Remote) == "" {
		return "origin"
	}
	return strings.TrimSpace(repo.Remote)
}
