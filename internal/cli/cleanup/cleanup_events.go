package cleanup

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// eventFileRe matches event filenames like "events-2025-01-15.jsonl" and
// optional rotation suffixes like "events-2025-01-15.jsonl.1".
var eventFileRe = regexp.MustCompile(`^events-(\d{4}-\d{2}-\d{2})\.jsonl(?:\.\d+)?$`)

// cleanupEvents purges old day-based event JSONL files.
// Returns (purged count, error).
func cleanupEvents(maxAge time.Duration, dryRun bool) (int, error) {
	eventsDir := resolveEventsDir()
	if eventsDir == "" {
		return 0, nil
	}
	return purgeEventFiles(eventsDir, maxAge, dryRun)
}

// purgeEventFiles is the core logic for cleaning up event files in a given
// directory. Extracted so it can be tested without the full config stack.
func purgeEventFiles(eventsDir string, maxAge time.Duration, dryRun bool) (int, error) {
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read events dir: %w", err)
	}

	cutoff := time.Now().UTC().Add(-maxAge)
	today := time.Now().UTC().Format("2006-01-02")

	purged := 0
	for _, entry := range entries {
		if shouldPurgeEventFile(entry, cutoff, today) {
			if dryRun {
				purged++
				continue
			}
			if err := os.Remove(filepath.Join(eventsDir, entry.Name())); err != nil {
				slog.Warn("cleanup: failed to remove event file", "file", entry.Name(), "error", err)
				continue
			}
			purged++
		}
	}

	return purged, nil
}

// shouldPurgeEventFile checks if a directory entry is an event file older than cutoff.
func shouldPurgeEventFile(entry os.DirEntry, cutoff time.Time, today string) bool {
	if entry.IsDir() {
		return false
	}
	matches := eventFileRe.FindStringSubmatch(entry.Name())
	if matches == nil {
		return false
	}
	dateStr := matches[1]
	if dateStr == today {
		return false
	}
	fileDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		slog.Warn("cleanup: skipping event file with unparseable date", "file", entry.Name())
		return false
	}
	return fileDate.Before(cutoff)
}

func resolveEventsDir() string {
	loomDir := cli.GetWorkspaceRuntimeDir()
	if loomDir == "" {
		return ""
	}
	return filepath.Join(loomDir, "events")
}
