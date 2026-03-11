package cli

import (
	"os/exec"
	"strconv"
	"strings"
)

// DiffStats holds the summary of changes between two git refs.
type DiffStats struct {
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
}

// ComputeDiffStats runs `git diff --numstat fromRef..HEAD` in worktreePath
// and returns aggregate statistics. Returns zero DiffStats on any error.
func ComputeDiffStats(worktreePath, fromRef string) DiffStats {
	if fromRef == "" {
		return DiffStats{}
	}

	// #nosec G204 - fromRef is from git rev-parse HEAD, not user input
	cmd := exec.Command("git", "diff", "--numstat", fromRef+"..HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return DiffStats{}
	}

	return parseDiffNumstat(string(out))
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
		// Skip binary files (shown as - - filename)
		if fields[0] == "-" || fields[1] == "-" {
			stats.FilesChanged++
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
	}
	return stats
}
