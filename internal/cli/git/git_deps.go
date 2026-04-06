package git

// git_deps.go contains deps-aware variants of git wrapper functions.
// Production functions in tested call chains use these instead of the
// exported wrappers in git.go. The exported functions delegate to these.

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

func gitFetch(deps *cli.Deps, dir string) error {
	fmt.Println("Fetching from origin...")
	return runGitOutput(deps, dir, "fetch", "origin")
}

func gitCheckout(deps *cli.Deps, dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Checking out %s...\n", branch)
	return runGitOutput(deps, dir, "checkout", branch)
}

func gitPull(deps *cli.Deps, dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pulling from origin/%s...\n", branch)
	return runGitOutput(deps, dir, "pull", "origin", branch)
}

func gitMerge(deps *cli.Deps, dir, branch, message string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Merging %s...\n", branch)
	return runGitOutput(deps, dir, "merge", "-m", message, "--", branch)
}

func gitMergeOrigin(deps *cli.Deps, dir, branch, message string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Merging origin/%s...\n", branch)
	return runGitOutput(deps, dir, "merge", "origin/"+branch, "-m", message)
}

func gitPush(deps *cli.Deps, dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pushing to origin/%s...\n", branch)
	return runGitOutput(deps, dir, "push", "origin", branch)
}

func gitPushForce(deps *cli.Deps, dir, branch string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Force pushing to origin/%s...\n", branch)
	return runGitOutput(deps, dir, "push", "origin", branch, "--force")
}

func gitReset(deps *cli.Deps, dir, ref string) error {
	if err := validateGitRef(ref); err != nil {
		return err
	}
	fmt.Printf("Resetting to %s...\n", ref)
	return runGitOutput(deps, dir, "reset", "--hard", ref)
}

func gitClean(deps *cli.Deps, dir string) error {
	fmt.Println("Cleaning untracked files...")
	return runGitOutput(deps, dir, "clean", "-fd")
}

func gitCleanDryRun(deps *cli.Deps, dir string) (string, error) {
	return runGit(deps, dir, "clean", "-fdn")
}

func gitCleanExclude(deps *cli.Deps, dir string, excludes []string) error {
	args := []string{"clean", "-fd"}
	for _, ex := range excludes {
		args = append(args, "--exclude="+ex)
	}
	fmt.Println("Cleaning untracked files (preserving runtime state)...")
	return runGitOutput(deps, dir, args...)
}

func gitCleanDryRunExclude(deps *cli.Deps, dir string, excludes []string) (string, error) {
	args := []string{"clean", "-fdn"}
	for _, ex := range excludes {
		args = append(args, "--exclude="+ex)
	}
	return runGit(deps, dir, args...)
}

func getConflictedFilesDeps(deps *cli.Deps, dir string) ([]string, error) {
	output, err := runGit(deps, dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	return lines, nil
}

func hasCommitsBetweenDeps(deps *cli.Deps, dir, target, source string) (bool, error) {
	if err := validateGitRef(target); err != nil {
		return false, err
	}
	if err := validateGitRef(source); err != nil {
		return false, err
	}
	output, err := runGit(deps, dir, "log", fmt.Sprintf("%s..%s", target, source), "--oneline")
	if err != nil {
		return true, nil
	}
	return strings.TrimSpace(output) != "", nil
}

func IsCleanWorkingTreeDeps(deps *cli.Deps, dir string) (bool, error) {
	output, err := runGit(deps, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "", nil
}

func gitFetchRemote(deps *cli.Deps, dir, remote string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	fmt.Printf("Fetching from %s...\n", r)
	return runGitOutput(deps, dir, "fetch", r)
}

func gitMergeRemote(deps *cli.Deps, dir, remote, branch, message string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(branch); err != nil {
		return err
	}
	ref := r + "/" + branch
	fmt.Printf("Merging %s...\n", ref)
	return runGitOutput(deps, dir, "merge", ref, "-m", message)
}

func gitPushRemote(deps *cli.Deps, dir, remote, branch string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pushing to %s/%s...\n", r, branch)
	return runGitOutput(deps, dir, "push", r, branch)
}

func gitPullRemote(deps *cli.Deps, dir, remote, branch string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(branch); err != nil {
		return err
	}
	fmt.Printf("Pulling from %s/%s...\n", r, branch)
	return runGitOutput(deps, dir, "pull", r, branch)
}

func hasCommitsBetweenRemoteDeps(deps *cli.Deps, dir, remote, target, source string) (bool, error) {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return false, err
	}
	if err := validateGitRef(target); err != nil {
		return false, err
	}
	if err := validateGitRef(source); err != nil {
		return false, err
	}
	output, err := runGit(deps, dir, "log", fmt.Sprintf("%s/%s..%s", r, target, source), "--oneline")
	if err != nil {
		return true, nil
	}
	return strings.TrimSpace(output) != "", nil
}

func isRefCheckedOutInWorktreeDeps(deps *cli.Deps, dir, branch string) (bool, string, error) {
	output, err := runGit(deps, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return false, "", err
	}
	var currentWorktree string
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			currentWorktree = ""
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			currentWorktree = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "branch refs/heads/") {
			branchName := strings.TrimPrefix(line, "branch refs/heads/")
			if branchName == branch {
				return true, currentWorktree, nil
			}
		}
	}
	return false, "", nil
}

func gitCheckoutDetached(deps *cli.Deps, dir, ref string) error {
	if err := validateGitRef(ref); err != nil {
		return err
	}
	fmt.Printf("Checking out %s (detached)...\n", ref)
	return runGitOutput(deps, dir, "checkout", "--detach", ref)
}

func gitCreateBranchFromHead(deps *cli.Deps, dir, name string) error {
	if err := validateGitRef(name); err != nil {
		return err
	}
	fmt.Printf("Creating branch %s...\n", name)
	return runGitOutput(deps, dir, "checkout", "-b", name)
}

func gitDeleteBranch(deps *cli.Deps, dir, name string, force bool) error {
	if err := validateGitRef(name); err != nil {
		return err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	return runGitOutput(deps, dir, "branch", flag, "--", name)
}

func gitPushRefspec(deps *cli.Deps, dir, remote, localRef, remoteRef string) error {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return err
	}
	if err := validateGitRef(localRef); err != nil {
		return err
	}
	if err := validateGitRef(remoteRef); err != nil {
		return err
	}
	refspec := localRef + ":" + remoteRef
	fmt.Printf("Pushing %s to %s/%s...\n", localRef, r, remoteRef)
	return runGitOutput(deps, dir, "push", r, refspec)
}

func getStashCountDeps(deps *cli.Deps, dir string) (int, error) {
	output, err := runGit(deps, dir, "stash", "list")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

func gitStash(deps *cli.Deps, dir string) (bool, error) {
	countBefore, err := getStashCountDeps(deps, dir)
	if err != nil {
		return false, err
	}
	fmt.Println("Stashing local changes...")
	if err := runGitOutput(deps, dir, "stash"); err != nil {
		return false, fmt.Errorf("failed to stash changes: %w", err)
	}
	countAfter, err := getStashCountDeps(deps, dir)
	if err != nil {
		return false, err
	}
	return countAfter > countBefore, nil
}

func gitStashPop(deps *cli.Deps, dir string) error {
	fmt.Println("Restoring stashed changes...")
	return runGitOutput(deps, dir, "stash", "pop")
}

func branchExistsLocallyDeps(deps *cli.Deps, dir, branch string) (bool, error) {
	if err := validateGitRef(branch); err != nil {
		return false, err
	}
	_, err := runGit(deps, dir, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func remoteBranchExistsDeps(deps *cli.Deps, dir, remote, branch string) (bool, error) {
	r := resolveRemote(remote)
	if err := validateGitRef(r); err != nil {
		return false, err
	}
	if err := validateGitRef(branch); err != nil {
		return false, err
	}
	_, err := runGit(deps, dir, "rev-parse", "--verify", "refs/remotes/"+r+"/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func gitCheckoutNewFromRef(deps *cli.Deps, dir, branch, startPoint string) error {
	if err := validateGitRef(branch); err != nil {
		return err
	}
	if err := validateGitRef(startPoint); err != nil {
		return err
	}
	fmt.Printf("Creating branch %s from %s...\n", branch, startPoint)
	return runGitOutput(deps, dir, "checkout", "-b", branch, startPoint)
}

func gitMergeAbort(deps *cli.Deps, dir string) error {
	_, err := runGit(deps, dir, "merge", "--abort")
	return err
}

func hasUnmergedFilesDeps(deps *cli.Deps, dir string) (bool, error) {
	files, err := getConflictedFilesDeps(deps, dir)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}
