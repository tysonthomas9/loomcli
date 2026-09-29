package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

func CleanupFreshClone(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) == 0 {
		return os.Remove(path)
	}
	gitDir := filepath.Join(path, ".git")
	if info, err := os.Lstat(gitDir); err != nil || !info.IsDir() {
		return fmt.Errorf("partial clone retained at %s: not a verified fresh clone", path)
	}
	runner, err := gitexec.New(path, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return fmt.Errorf("partial clone retained at %s: %w", path, err)
	}
	ctx := context.Background()
	if err := verifyFreshCloneState(ctx, runner, path); err != nil {
		return err
	}
	defaultRef, err := runner.Run(ctx, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return fmt.Errorf("partial clone retained at %s: unknown default branch: %w", path, err)
	}
	defaultBranch := strings.TrimPrefix(strings.TrimSpace(string(defaultRef)), "refs/remotes/origin/")
	branches, err := runner.Run(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return fmt.Errorf("partial clone retained at %s: %w", path, err)
	}
	for _, branch := range strings.Fields(string(branches)) {
		if branch != defaultBranch && !refname.IsInteractiveLeadBranch(branch) {
			return fmt.Errorf("partial clone retained at %s: extra branch %s", path, branch)
		}
		out, err := runner.Run(ctx, "rev-list", "--count", branch, "--not", "--remotes")
		if err != nil || strings.TrimSpace(string(out)) != "0" {
			return fmt.Errorf("partial clone retained at %s: unpushed branch %s: %v", path, branch, err)
		}
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("partial clone retained at %s: %w", path, err)
	}
	return nil
}

func verifyFreshCloneState(ctx context.Context, runner *gitexec.Runner, path string) error {
	for _, args := range [][]string{{"status", "--porcelain", "--ignored"}, {"for-each-ref", "--format=%(refname)", "refs/stash"}} {
		out, err := runner.Run(ctx, args...)
		if err != nil || len(out) != 0 {
			return fmt.Errorf("partial clone retained at %s: working files or stash present: %v", path, err)
		}
	}
	out, err := runner.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil || strings.Count(string(out), "worktree ") != 1 {
		return fmt.Errorf("partial clone retained at %s: additional worktree or inspection failure: %v", path, err)
	}
	return nil
}
