package cleanup

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

var sessionsOlderThan string

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Manage agent sessions",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

var sessionsCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove old session data",
	RunE: func(cmd *cobra.Command, args []string) error {
		dur, err := parseDayDuration(sessionsOlderThan)
		if err != nil {
			return fmt.Errorf("invalid --older-than value %q: %w", sessionsOlderThan, err)
		}

		archive, err := sessions.OpenArchive(cmd.Context(), cli.GetWorkspaceRuntimeDir())
		if err != nil {
			return fmt.Errorf("open session store: %w", err)
		}

		result, err := archive.Cleanup(sessions.CleanupOptions{OlderThan: dur})
		if err != nil {
			return fmt.Errorf("purge sessions: %w", err)
		}

		fmt.Printf("Purged %d sessions older than %s\n", result.Purged, sessionsOlderThan)
		return nil
	},
}

// parseDayDuration parses a duration string that supports a "d" suffix for days
// (e.g. "30d" -> 30*24h). Falls back to time.ParseDuration for standard units.
func parseDayDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func init() {
	sessionsCleanCmd.Flags().StringVar(&sessionsOlderThan, "older-than", "30d", "Remove sessions older than this duration (e.g. 30d, 720h)")
	sessionsCmd.AddCommand(sessionsCleanCmd)
	cli.RegisterCommand(sessionsCmd)
}
