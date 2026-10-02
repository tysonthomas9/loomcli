package git

import "github.com/tysonthomas9/loomcli/internal/cli"

// RunGitCommand executes a git command in the specified directory using defaultDeps.
func RunGitCommand(dir string, args ...string) (string, error) {
	return runGit(ensureDefaultDeps(), dir, args...)
}

// RunGitCommandWithOutput executes a git command and streams output to stdout/stderr.
func RunGitCommandWithOutput(dir string, args ...string) error {
	return runGitOutput(ensureDefaultDeps(), dir, args...)
}

func GitFetch(dir string) error      { return gitFetch(ensureDefaultDeps(), dir) }
func GitReset(dir, ref string) error { return gitReset(ensureDefaultDeps(), dir, ref) }

func GetConflictedFiles(dir string) ([]string, error) {
	return getConflictedFilesDeps(ensureDefaultDeps(), dir)
}

func IsCleanWorkingTree(dir string) (bool, error) {
	return IsCleanWorkingTreeDeps(ensureDefaultDeps(), dir)
}

func GetCurrentBranch(path string) (string, error) {
	return cli.GetCurrentBranch(path)
}

func getStashCount(dir string) (int, error) {
	return getStashCountDeps(ensureDefaultDeps(), dir)
}
