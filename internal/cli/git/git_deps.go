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

func gitReset(deps *cli.Deps, dir, ref string) error {
	if err := validateGitRef(ref); err != nil {
		return err
	}
	fmt.Printf("Resetting to %s...\n", ref)
	// P4.11 AC deviation: allowlisted raw writer for the P1.5 capture-first
	// reset until Reset moves into loomgit (design §2.4 follow-up).
	return runGitOutput(deps, dir, "reset", "--hard", ref)
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

func IsCleanWorkingTreeDeps(deps *cli.Deps, dir string) (bool, error) {
	output, err := runGit(deps, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) == "", nil
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
