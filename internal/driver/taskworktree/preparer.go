// Package taskworktree owns the machine-local preparation of a task-run
// worktree together with the stack-lineage policy that selects and records its
// base/output. Repository admission and selection remain outside this package.
package taskworktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// Preparer holds the workspace-local state used throughout one task worktree
// preparation.
type Preparer struct {
	workspaceKey string
	local        bootstrap.WorkspaceLocalState
}

// Open loads and validates the machine-local state for workspaceKey.
func Open(workspaceKey string) (*Preparer, error) {
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
	return &Preparer{workspaceKey: workspaceKey, local: local}, nil
}

// Prepare creates taskRunID's detached worktree from a Source-Control-verified
// checkout and local ref. This boundary performs no network or credential
// resolution.
func (p *Preparer) Prepare(
	ctx context.Context,
	repo *domain.Repo,
	repoPath string,
	taskRunID string,
	baseRef string,
	baseCommit string,
) (string, error) {
	if p == nil || repo == nil {
		return "", fmt.Errorf("preparer and repo are required: %w", domain.ErrInvalid)
	}
	expectedPath := strings.TrimSpace(localworkspace.RepoPath(p.local, repo.Name))
	if expectedPath == "" {
		var err error
		expectedPath, err = localworkspace.RepoCheckoutPath(p.local.Path, repo.Name)
		if err != nil {
			return "", err
		}
	}
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" || filepath.Clean(repoPath) != filepath.Clean(expectedPath) {
		return "", fmt.Errorf("source control returned a different repo path for %q: %w", repo.Name, domain.ErrInvalid)
	}
	if !isGitCheckout(repoPath) {
		return "", fmt.Errorf("source control checkout for repo %q is not a git checkout", repo.Name)
	}
	target, err := localworkspace.TaskRunWorktreePath(p.local.Path, repo.Name, taskRunID)
	if err != nil {
		return "", err
	}
	if err := localworkspace.EnsureDetachedGitWorktreeAtRef(
		ctx,
		repoPath,
		target,
		baseRef,
		baseCommit,
	); err != nil {
		return "", fmt.Errorf("ensure task run worktree for repo %q: %w", repo.Name, err)
	}
	return target, nil
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
