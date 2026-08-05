// Package workspacematerialization owns the process and repository mechanics
// used to attach local Git repositories to a Loom workspace.
package workspacematerialization

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

// ErrRepositoryNotUsable identifies a source repository that cannot answer
// the basic Git queries required for materialization.
var ErrRepositoryNotUsable = gitbranch.ErrRepositoryNotUsable

// CreatedWorktree records the resources created while attaching one source
// repository so the caller can preserve its existing rollback bookkeeping.
type CreatedWorktree struct {
	OriginalRepositoryPath string
	WorktreePath           string
	// Branch is non-empty only when this operation created the branch.
	Branch string
}

// PathContains reports whether path is contained by root.
func PathContains(root, path string) bool {
	return localworkspace.PathContains(root, path)
}

// CreateWorktreeContext creates or recovers the requested isolation branch and
// attaches it as a workspace worktree. Cancellation is checked before and
// after each correctness-critical repository mutation.
//
//nolint:funlen // Branch recovery, attachment, cancellation, and rollback form one fenced operation.
func CreateWorktreeContext(
	ctx context.Context,
	repositoryPath,
	worktreePath,
	branch string,
) (CreatedWorktree, error) {
	if cause := context.Cause(ctx); cause != nil {
		return CreatedWorktree{}, cause
	}
	info, err := gitbranch.InspectContext(ctx, repositoryPath, branch)
	if err != nil {
		return CreatedWorktree{}, err
	}
	if cause := context.Cause(ctx); cause != nil {
		return CreatedWorktree{}, cause
	}

	baseRef := ""
	createBranch := info.State != gitbranch.StateHealthy
	if info.State == gitbranch.StateBroken {
		recoveryBase := RecoveryBaseContext(ctx, repositoryPath, branch)
		recovery, recoverErr := gitbranch.RecoverContext(
			ctx,
			repositoryPath,
			branch,
			recoveryBase,
			info,
		)
		if recoverErr != nil {
			return CreatedWorktree{}, recoverErr
		}
		baseRef = recovery.BaseSHA
	} else if info.State == gitbranch.StateHealthy {
		// A source branch is commonly checked out already. Attach the workspace
		// worktree detached at its exact healthy tip so Git's branch lock remains
		// authoritative.
		baseRef = info.BaseSHA
	}

	createdBranch := ""
	if createBranch {
		args := []string{"branch", branch}
		if baseRef != "" {
			args = append(args, baseRef)
		}
		if _, err := RunGitContext(ctx, repositoryPath, args...); err != nil {
			return CreatedWorktree{}, err
		}
		createdBranch = branch
	}

	if err := AddWorktreeContext(
		ctx,
		repositoryPath,
		worktreePath,
		branch,
		baseRef,
		createBranch,
	); err != nil {
		if context.Cause(ctx) != nil {
			_, _ = cli.RunGitCommand(
				repositoryPath,
				"worktree",
				"remove",
				"--force",
				worktreePath,
			)
			_ = os.RemoveAll(worktreePath)
		}
		// Delete only a branch created by this operation. If another process
		// claimed it concurrently, Git refuses deletion and preserves its owner.
		if createdBranch != "" {
			_, _ = cli.RunGitCommand(repositoryPath, "branch", "-D", createdBranch)
		}
		return CreatedWorktree{}, err
	}

	created := CreatedWorktree{
		OriginalRepositoryPath: repositoryPath,
		WorktreePath:           worktreePath,
		Branch:                 createdBranch,
	}
	if cause := context.Cause(ctx); cause != nil {
		cleanupAttachedWorktree(created)
		return CreatedWorktree{}, cause
	}
	return created, nil
}

// RecoveryBase returns a usable current branch to seed recovery, unless that
// branch is empty or is the branch being recovered.
func RecoveryBase(repositoryPath, targetBranch string) string {
	return RecoveryBaseContext(context.Background(), repositoryPath, targetBranch)
}

// RecoveryBaseContext is the cancellation-aware form of RecoveryBase.
func RecoveryBaseContext(
	ctx context.Context,
	repositoryPath,
	targetBranch string,
) string {
	out, err := RunGitContext(ctx, repositoryPath, "branch", "--show-current")
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(out)
	if base == "" || base == targetBranch {
		return ""
	}
	return base
}

// AddWorktree attaches a worktree using either a newly created branch or a
// detached exact base.
func AddWorktree(
	repositoryPath,
	worktreePath,
	branch,
	baseRef string,
	createBranch bool,
) error {
	return AddWorktreeContext(
		context.Background(),
		repositoryPath,
		worktreePath,
		branch,
		baseRef,
		createBranch,
	)
}

// AddWorktreeContext is the cancellation-aware form of AddWorktree.
func AddWorktreeContext(
	ctx context.Context,
	repositoryPath,
	worktreePath,
	branch,
	baseRef string,
	createBranch bool,
) error {
	args := []string{"worktree", "add"}
	if createBranch {
		args = append(args, worktreePath, branch)
	} else {
		args = append(args, "--detach", worktreePath)
		if baseRef != "" {
			args = append(args, baseRef)
		}
	}
	_, err := RunGitContext(ctx, repositoryPath, args...)
	return err
}

// RunGitContext runs one Git command with process-group cancellation configured
// so timed-out materialization cannot leave a mutating child process behind.
func RunGitContext(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error) {
	if ctx == nil {
		return "", context.Canceled
	}
	command := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed Git executable; args are internal worktree coordinates.
	command.Dir = dir
	localworkspace.ConfigureGitProcessCancellation(command)
	output, err := command.CombinedOutput()
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return string(output), cause
		}
		return string(output), fmt.Errorf(
			"git %s failed: %w: %s",
			strings.Join(args, " "),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return string(output), nil
}

func cleanupAttachedWorktree(created CreatedWorktree) {
	_, _ = cli.RunGitCommand(
		created.OriginalRepositoryPath,
		"worktree",
		"remove",
		created.WorktreePath,
	)
	if created.Branch != "" {
		_, _ = cli.RunGitCommand(
			created.OriginalRepositoryPath,
			"branch",
			"-D",
			created.Branch,
		)
	}
}
