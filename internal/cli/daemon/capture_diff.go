package daemon

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// captureGitDiff runs `git diff HEAD` and returns the output truncated.
func captureGitDiff(worktreePath string, maxBytes int) string {
	resolver := cli.GetDefaultResolver()
	if resolver.Mode == cli.ModeWorkspace {
		worktrees, err := resolver.DiscoverWorktrees()
		if err == nil && len(worktrees) > 0 {
			return captureMultiRepoDiff(worktrees, maxBytes)
		}
	}
	return captureSingleRepoDiff(worktreePath, maxBytes)
}

func captureSingleRepoDiff(repoPath string, maxBytes int) string {
	output, err := cli.RunGitCommand(repoPath, "diff", "HEAD")
	if err != nil {
		return ""
	}
	output = strings.TrimSpace(output)
	return config.TruncateDiff(output, maxBytes)
}

func captureMultiRepoDiff(worktrees []cli.WorktreeInfo, maxBytes int) string {
	var sb strings.Builder
	for _, wt := range worktrees {
		output, err := cli.RunGitCommand(wt.Path, "diff", "HEAD")
		if err != nil {
			continue
		}
		output = strings.TrimSpace(output)
		if output == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- repo: %s ---\n", wt.Name))
		sb.WriteString(output)
		sb.WriteString("\n")
	}
	return config.TruncateDiff(sb.String(), maxBytes)
}
