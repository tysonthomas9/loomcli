package git

import (
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// DiffStats holds the summary of changes between two git refs.
type DiffStats struct {
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
	FilesTouched []string
}

// ComputeDiffStats runs `git diff --numstat fromRef..HEAD` in worktreePath
// and returns aggregate statistics. Returns zero DiffStats on any error.
func ComputeDiffStats(worktreePath, fromRef string) DiffStats {
	if fromRef == "" {
		return DiffStats{}
	}

	out, err := cli.RunGitCommand(worktreePath, "diff", "--numstat", fromRef+"..HEAD")
	if err != nil {
		return DiffStats{}
	}

	return parseDiffNumstat(out)
}

// parseDiffNumstat parses the output of `git diff --numstat`.
// Each line is: <added>\t<removed>\t<filename>
// Binary files show: -\t-\t<filename>
func parseDiffNumstat(output string) DiffStats {
	var stats DiffStats
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}

		// Normalize rename paths to the new name.
		path := fields[2]
		if strings.Contains(path, " => ") {
			path = parseNumstatRenamePath(path)
		}

		// Binary files (shown as - - filename): count but skip line stats.
		if fields[0] == "-" || fields[1] == "-" {
			stats.FilesChanged++
			stats.FilesTouched = append(stats.FilesTouched, path)
			continue
		}
		added, err1 := strconv.Atoi(fields[0])
		removed, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		stats.FilesChanged++
		stats.LinesAdded += added
		stats.LinesRemoved += removed
		stats.FilesTouched = append(stats.FilesTouched, path)
	}
	return stats
}
