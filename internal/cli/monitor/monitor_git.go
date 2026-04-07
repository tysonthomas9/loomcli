package monitor

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// monitorGitTimeout is the per-command timeout for git subprocesses in the
// monitor collection path. Prevents indefinite hangs from git index locks.
const monitorGitTimeout = 10 * time.Second

// runMonitorGit runs a git command with a timeout, scoped to monitor collection.
// Runs through deps.Git so tests can mock git; enforces timeout via goroutine+timer.
func runMonitorGit(deps *cli.Deps, path string, args ...string) (string, error) {
	type result struct {
		out string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := cli.RunGit(deps, path, args...)
		ch <- result{out, err}
	}()
	timer := time.NewTimer(monitorGitTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.out, r.err
	case <-timer.C:
		return "", fmt.Errorf("git %s timed out after %v", strings.Join(args, " "), monitorGitTimeout)
	}
}

func GetWorktreeGitSyncStatus(path, defaultBranch string, overrideBranch string) (ahead, behind int) {
	return GetWorktreeGitSyncStatusDeps(cli.GetDeps(nil), path, defaultBranch, overrideBranch)
}

func GetWorktreeGitSyncStatusDeps(deps *cli.Deps, path, defaultBranch string, overrideBranch string) (ahead, behind int) {
	branch := overrideBranch
	if branch == "" {
		branch = defaultBranch
	}

	// Count commits ahead/behind integration branch
	// Format: "behind\tahead" (from HEAD's perspective)
	output, err := runMonitorGit(deps, path, "rev-list", "--left-right", "--count",
		fmt.Sprintf("origin/%s...HEAD", branch))
	if err != nil {
		return 0, 0
	}

	// Parse "4\t2" format
	parts := strings.Fields(output)
	if len(parts) == 2 {
		behind, _ = strconv.Atoi(parts[0])
		ahead, _ = strconv.Atoi(parts[1])
	}
	return ahead, behind
}

// getGitHubRemoteURL returns the GitHub HTTPS URL for the origin remote.
// Returns empty string if not a GitHub remote or on error.
func getGitHubRemoteURL(path string) string {
	return getGitHubRemoteURLDeps(cli.GetDeps(nil), path)
}

func getGitHubRemoteURLDeps(deps *cli.Deps, path string) string {
	output, err := runMonitorGit(deps, path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(output)

	// Convert SSH URL: git@github.com:user/repo.git -> https://github.com/user/repo
	if strings.HasPrefix(url, "git@github.com:") {
		url = strings.TrimPrefix(url, "git@github.com:")
		url = strings.TrimSuffix(url, ".git")
		return "https://github.com/" + url
	}

	// Handle HTTPS URL: https://github.com/user/repo.git -> https://github.com/user/repo
	if strings.Contains(url, "github.com") {
		url = strings.TrimSuffix(url, ".git")
		return url
	}

	return ""
}

// getWorktreeCommitDetails returns the recent commits ahead of the integration branch.
func getWorktreeCommitDetails(path, defaultBranch string, limit int, githubURL string, overrideBranch string) []CommitDetail {
	return getWorktreeCommitDetailsDeps(cli.GetDeps(nil), path, defaultBranch, limit, githubURL, overrideBranch)
}

func getWorktreeCommitDetailsDeps(deps *cli.Deps, path, defaultBranch string, limit int, githubURL string, overrideBranch string) []CommitDetail {
	branch := overrideBranch
	if branch == "" {
		branch = defaultBranch
	}

	output, err := runMonitorGit(deps, path, "log",
		fmt.Sprintf("origin/%s..HEAD", branch),
		fmt.Sprintf("--format=%%h|%%s"),
		"-n", strconv.Itoa(limit))
	if err != nil {
		return nil
	}

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	commits := make([]CommitDetail, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		commit := CommitDetail{
			Hash:    parts[0],
			Message: parts[1],
		}
		if githubURL != "" {
			commit.URL = githubURL + "/commit/" + parts[0]
		}
		commits = append(commits, commit)
	}
	return commits
}

// getWorktreeStatus runs git status --porcelain once and returns all derived values:
// clean (no changes), uncommitted count, and file change list.
// Replaces the previous pattern of calling IsCleanWorkingTree + getUncommittedChanges +
// getWorktreeFileChanges as three separate git subprocesses.
func getWorktreeStatus(deps *cli.Deps, path string) (clean bool, uncommittedCount int, fileChanges []FileChange) {
	output, err := runMonitorGit(deps, path, "status", "--porcelain")
	if err != nil {
		// Match prior IsCleanWorkingTreeDeps behavior: a git error means we
		// can't confirm cleanliness, so report "not clean" rather than silently
		// labeling a broken worktree as ready.
		return false, 0, nil
	}
	trimmed := strings.TrimRight(output, " \t\n\r")
	if trimmed == "" {
		return true, 0, nil
	}
	lines := strings.Split(trimmed, "\n")
	changes := make([]FileChange, 0, len(lines))
	for i, line := range lines {
		if i >= 20 {
			break
		}
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		filePath := line[3:]
		changes = append(changes, FileChange{Status: status, Path: filePath})
	}
	return false, len(lines), changes
}

// getWorktreeFileChanges returns uncommitted file changes from git status.
func getWorktreeFileChanges(path string) []FileChange {
	return getWorktreeFileChangesDeps(cli.GetDeps(nil), path)
}

func getWorktreeFileChangesDeps(deps *cli.Deps, path string) []FileChange {
	output, err := runMonitorGit(deps, path, "status", "--porcelain")
	if err != nil {
		return nil
	}

	// Only trim trailing whitespace — leading spaces are significant in porcelain format
	trimmed := strings.TrimRight(output, " \t\n\r")
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	changes := make([]FileChange, 0, len(lines))
	for i, line := range lines {
		if i >= 20 { // Limit to 20 files
			break
		}
		if len(line) < 4 {
			continue
		}
		// Porcelain format: XY filename (first 2 chars are status, then space, then path)
		status := strings.TrimSpace(line[:2])
		filePath := line[3:]
		changes = append(changes, FileChange{
			Status: status,
			Path:   filePath,
		})
	}
	return changes
}
