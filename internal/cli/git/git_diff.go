package git

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

const (
	maxDiffPatchBytes = 500 * 1024 // 500KB cap on single-file patches
	maxDiffFiles      = 500        // cap on number of files returned
)

// ResolveMergeBase returns the merge-base commit hash between branch and HEAD.
func ResolveMergeBase(worktreePath, branch string) (string, error) {
	if err := validateGitRef(branch); err != nil {
		return "", err
	}
	out, err := cli.RunGitCommand(worktreePath, "merge-base", branch, "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolving merge-base: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// DiffCommits returns the list of commits between mergeBase and HEAD.
// Format: %H|%h|%an|%ae|%aI|%s — subject is last so pipes in it are preserved.
//
// Ordering contract: uses --topo-order so descendants appear before ancestors.
// This is required by the pushed/unpushed positional split in diff_service.go:
// unpushed commits (descendants of the last pushed commit) must come first.
func DiffCommits(worktreePath, mergeBase string, limit int) ([]ops.DiffCommitResult, error) {
	if err := validateGitRef(mergeBase); err != nil {
		return nil, err
	}
	args := []string{"log", mergeBase + "..HEAD", "--topo-order", "--format=%H|%h|%an|%ae|%aI|%s"}
	if limit > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", limit))
	}
	out, err := cli.RunGitCommand(worktreePath, args...)
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	results := make([]ops.DiffCommitResult, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 6 {
			continue
		}
		results = append(results, ops.DiffCommitResult{
			Hash:      parts[0],
			ShortHash: parts[1],
			Author:    parts[2],
			Email:     parts[3],
			Date:      parts[4],
			Subject:   parts[5],
		})
	}
	return results, nil
}

// UnpushedCount returns the number of commits on HEAD that are not reachable from
// origin/<targetBranch>. Returns -1 on error (e.g., no remote tracking branch).
func UnpushedCount(worktreePath, targetBranch string) (int, error) {
	if err := validateGitRef(targetBranch); err != nil {
		return -1, err
	}
	out, err := cli.RunGitCommand(worktreePath, "rev-list", "--count", "origin/"+targetBranch+"..HEAD")
	if err != nil {
		return -1, fmt.Errorf("counting unpushed commits: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1, fmt.Errorf("parsing unpushed count %q: %w", out, err)
	}
	return n, nil
}

// DiffFiles returns the list of changed files between two refs with status and stats.
func DiffFiles(worktreePath, from, to string) ([]ops.DiffFileResult, error) {
	if err := validateGitRef(from); err != nil {
		return nil, err
	}
	if err := validateGitRef(to); err != nil {
		return nil, err
	}
	diffRange := from + ".." + to

	nameStatusOut, err := cli.RunGitCommand(worktreePath, "diff", "--name-status", diffRange)
	if err != nil {
		return nil, fmt.Errorf("diff name-status: %w", err)
	}

	numstatOut, err := cli.RunGitCommand(worktreePath, "diff", "--numstat", diffRange)
	if err != nil {
		return nil, fmt.Errorf("diff numstat: %w", err)
	}

	statsMap := parseNumstatToMap(numstatOut)
	return parseNameStatusWithStats(nameStatusOut, statsMap), nil
}

// diffFileStat holds line additions/deletions for a single file.
type diffFileStat struct {
	additions int
	deletions int
}

// parseNumstatToMap parses git diff --numstat output into a map keyed by file path.
func parseNumstatToMap(numstatOut string) map[string]diffFileStat {
	statsMap := make(map[string]diffFileStat)
	for _, line := range strings.Split(strings.TrimSpace(numstatOut), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		if strings.Contains(path, " => ") {
			path = parseNumstatRenamePath(path)
		}
		var s diffFileStat
		if fields[0] != "-" && fields[1] != "-" {
			s.additions, _ = strconv.Atoi(fields[0])
			s.deletions, _ = strconv.Atoi(fields[1])
		}
		statsMap[path] = s
	}
	return statsMap
}

// parseNameStatusWithStats merges git diff --name-status output with numstat data.
func parseNameStatusWithStats(nameStatusOut string, statsMap map[string]diffFileStat) []ops.DiffFileResult {
	results := make([]ops.DiffFileResult, 0)
	for _, line := range strings.Split(strings.TrimSpace(nameStatusOut), "\n") {
		if line == "" {
			continue
		}
		if len(results) >= maxDiffFiles {
			break
		}
		result, ok := parseNameStatusLine(line)
		if !ok {
			continue
		}
		if s, found := statsMap[result.Path]; found {
			result.Additions = s.additions
			result.Deletions = s.deletions
		}
		results = append(results, result)
	}
	return results
}

// parseNameStatusLine parses a single line from git diff --name-status.
func parseNameStatusLine(line string) (ops.DiffFileResult, bool) {
	fields := strings.Split(line, "\t")
	if len(fields) < 2 {
		return ops.DiffFileResult{}, false
	}
	status := fields[0]
	if strings.HasPrefix(status, "R") {
		if len(fields) < 3 {
			return ops.DiffFileResult{}, false
		}
		return ops.DiffFileResult{Status: "R", OldPath: fields[1], Path: fields[2]}, true
	}
	return ops.DiffFileResult{Status: status, Path: fields[1]}, true
}

// parseNumstatRenamePath extracts the new path from numstat rename output.
// Handles formats like: "old => new" and "{prefix/old => prefix/new}/suffix"
func parseNumstatRenamePath(s string) string {
	// Handle brace syntax: "{old => new}/rest" or "prefix/{old => new}/rest"
	braceStart := strings.Index(s, "{")
	braceEnd := strings.Index(s, "}")
	if braceStart >= 0 && braceEnd > braceStart {
		inner := s[braceStart+1 : braceEnd]
		prefix := s[:braceStart]
		suffix := s[braceEnd+1:]
		parts := strings.SplitN(inner, " => ", 2)
		if len(parts) == 2 {
			return prefix + parts[1] + suffix
		}
	}
	// Simple "old => new"
	parts := strings.SplitN(s, " => ", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return s
}

// DiffFilePatch returns the unified diff patch for a single file between two refs.
func DiffFilePatch(worktreePath, from, to, path string) (*ops.DiffFilePatchResult, error) {
	if err := validateGitRef(from); err != nil {
		return nil, err
	}
	if err := validateGitRef(to); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("path must not be empty")
	}
	diffRange := from + ".." + to

	// Get numstat for this file to detect binary and get stats
	numstatOut, err := cli.RunGitCommand(worktreePath, "diff", "--numstat", diffRange, "--", path)
	if err != nil {
		return nil, fmt.Errorf("diff numstat for file: %w", err)
	}

	result := &ops.DiffFilePatchResult{}
	numstatLine := strings.TrimSpace(numstatOut)
	if numstatLine != "" {
		fields := strings.SplitN(numstatLine, "\t", 3)
		if len(fields) >= 2 {
			if fields[0] == "-" && fields[1] == "-" {
				result.IsBinary = true
				return result, nil
			}
			result.Additions, _ = strconv.Atoi(fields[0])
			result.Deletions, _ = strconv.Atoi(fields[1])
		}
	}

	// Get the actual patch
	patchOut, err := cli.RunGitCommand(worktreePath, "diff", diffRange, "--", path)
	if err != nil {
		return nil, fmt.Errorf("diff patch for file: %w", err)
	}

	if len(patchOut) > maxDiffPatchBytes {
		result.IsTooLarge = true
		result.Patch = ""
		return result, nil
	}

	result.Patch = patchOut
	return result, nil
}
