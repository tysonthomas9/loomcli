package cleanup

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// DefaultRetentionAge is the default data retention period used by both
// loom cleanup and the serve startup purge.
const DefaultRetentionAge = 30 * 24 * time.Hour

var (
	cleanupSessionsAge string
	cleanupUsageAge    string
	cleanupEventsAge   string
	cleanupDryRun      bool
)

var cleanupCmd = &cobra.Command{
	Use:     "cleanup",
	Short:   "Purge old data from sessions, usage, and event stores",
	GroupID: "workspace",
	Long: `Purge old data from all JSONL stores with configurable retention periods.

By default, data older than 30 days is removed. Each store has its own
retention flag. A dry-run mode previews what would be deleted.

Stores cleaned:
  - Sessions: old session directories + index.jsonl compaction
  - Usage:    old records from usage.jsonl
  - Events:   old day-based event JSONL files

Does NOT touch runtime-owned files (issues.jsonl, interactions.jsonl).`,
	RunE: runCleanup,
}

func init() {
	cleanupCmd.Flags().StringVar(&cleanupSessionsAge, "sessions-older-than", "30d", "Purge sessions older than this (e.g. 30d, 720h)")
	cleanupCmd.Flags().StringVar(&cleanupUsageAge, "usage-older-than", "30d", "Purge usage records older than this (e.g. 30d, 720h)")
	cleanupCmd.Flags().StringVar(&cleanupEventsAge, "events-older-than", "30d", "Purge event files older than this (e.g. 30d, 720h)")
	cleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Preview what would be deleted without modifying files")
	cli.RegisterCommand(cleanupCmd)
}

func runCleanup(cmd *cobra.Command, _ []string) error {
	runtimeDir := cli.GetWorkspaceRuntimeDir()

	sessDur, err := parseDayDuration(cleanupSessionsAge)
	if err != nil {
		return fmt.Errorf("invalid --sessions-older-than %q: %w", cleanupSessionsAge, err)
	}
	usageDur, err := parseDayDuration(cleanupUsageAge)
	if err != nil {
		return fmt.Errorf("invalid --usage-older-than %q: %w", cleanupUsageAge, err)
	}
	eventsDur, err := parseDayDuration(cleanupEventsAge)
	if err != nil {
		return fmt.Errorf("invalid --events-older-than %q: %w", cleanupEventsAge, err)
	}

	var hasError bool

	sp, sc, se := cleanupSessions(cmd.Context(), runtimeDir, sessDur, cleanupDryRun)
	hasError = printCleanupResult("Sessions", sp, sc, se) || hasError

	up, ue := cleanupUsage(runtimeDir, usageDur, cleanupDryRun)
	hasError = printCleanupResult("Usage", up, 0, ue) || hasError

	ep, ee := cleanupEvents(eventsDur, cleanupDryRun)
	hasError = printCleanupResult("Events", ep, 0, ee) || hasError

	if hasError {
		return fmt.Errorf("one or more cleanup steps failed")
	}
	return nil
}

func printCleanupResult(store string, purged int, extra int, err error) bool {
	if err != nil {
		fmt.Printf("%s: error: %v\n", store, err)
		return true
	}
	verb := "purged"
	if cleanupDryRun {
		verb = "would purge"
	}
	if extra > 0 || store == "Sessions" {
		compactVerb := "compacted"
		if cleanupDryRun {
			compactVerb = "would compact"
		}
		fmt.Printf("%s: %s %d, %s %d index entries\n", store, verb, purged, compactVerb, extra)
	} else {
		unit := "records"
		if store == "Events" {
			unit = "files"
		}
		fmt.Printf("%s: %s %d %s\n", store, verb, purged, unit)
	}
	return false
}
