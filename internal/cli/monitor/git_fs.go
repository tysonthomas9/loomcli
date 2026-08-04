package monitor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveGitDir returns the .git directory path. For linked worktrees,
// follows the gitdir: pointer in the .git file.
func resolveGitDir(worktreePath string) (string, error) {
	gitPath := filepath.Join(worktreePath, ".git")
	fi, err := os.Lstat(gitPath)
	if err != nil {
		return "", fmt.Errorf("stat .git: %w", err)
	}

	if fi.IsDir() {
		// Regular repository: .git is a directory
		return gitPath, nil
	}

	// Linked worktree: .git is a file containing "gitdir: <path>"
	data, err := os.ReadFile(gitPath) //nolint:gosec // worktreePath comes from worktree discovery, not user input
	if err != nil {
		return "", fmt.Errorf("read .git file: %w", err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("unexpected .git file content: %q", line)
	}
	gitDir := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	return filepath.Clean(gitDir), nil
}

// resolveCommonGitDir returns the common git directory (where refs/ lives).
// For linked worktrees, reads the commondir file.
func resolveCommonGitDir(worktreeGitDir string) (string, error) {
	commondirPath := filepath.Join(worktreeGitDir, "commondir")
	data, err := os.ReadFile(commondirPath) //nolint:gosec // worktreeGitDir resolved from worktree discovery
	if err != nil {
		if os.IsNotExist(err) {
			// No commondir file — this is the main repo, gitdir is the common dir
			return worktreeGitDir, nil
		}
		return "", fmt.Errorf("read commondir: %w", err)
	}
	commonDir := strings.TrimSpace(string(data))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(worktreeGitDir, commonDir)
	}
	return filepath.Clean(commonDir), nil
}

// ReadBranchFromFS reads the current branch name from .git/HEAD directly.
// For linked worktrees, resolves the .git file -> gitdir chain.
// Returns "" for detached HEAD.
func ReadBranchFromFS(worktreePath string) (string, error) {
	gitDir, err := resolveGitDir(worktreePath)
	if err != nil {
		return "", err
	}

	headPath := filepath.Join(gitDir, "HEAD")
	data, err := os.ReadFile(headPath) //nolint:gosec // gitDir resolved from worktree discovery
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}

	line := strings.TrimSpace(string(data))
	const refPrefix = "ref: refs/heads/"
	if strings.HasPrefix(line, refPrefix) {
		return strings.TrimPrefix(line, refPrefix), nil
	}

	// Detached HEAD (raw SHA or other ref format)
	return "", nil
}

// ReadRefSHA reads a git ref (e.g., "refs/heads/main") from loose refs or packed-refs.
func ReadRefSHA(commonGitDir, refName string) (string, error) {
	// Try loose ref first
	loosePath := filepath.Join(commonGitDir, refName)
	data, err := os.ReadFile(loosePath) //nolint:gosec // commonGitDir + refName resolved from worktree discovery
	if err == nil {
		sha := strings.TrimSpace(string(data))
		if len(sha) >= 40 {
			return sha, nil
		}
	}

	// Fall back to packed-refs
	packedPath := filepath.Join(commonGitDir, "packed-refs")
	f, err := os.Open(packedPath) //nolint:gosec // commonGitDir resolved from worktree discovery
	if err != nil {
		return "", fmt.Errorf("ref %q not found in loose refs or packed-refs", refName)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip comments and peel lines (^...)
		if len(line) == 0 || line[0] == '#' || line[0] == '^' {
			continue
		}
		// Format: "<sha> <refname>"
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 && parts[1] == refName {
			return parts[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan packed-refs: %w", err)
	}

	return "", fmt.Errorf("ref %q not found in loose refs or packed-refs", refName)
}
