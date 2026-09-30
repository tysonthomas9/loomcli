package retention

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	loomretention "github.com/tysonthomas9/loomcli/internal/loomgit/retention"
)

var retentionCmd = &cobra.Command{
	Use:     "retention-sweep",
	Short:   "Report task copies eligible for retention cleanup",
	GroupID: "git",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		apply, _ := cmd.Flags().GetBool("apply")
		return runRetention(cmd.Context(), filepath.Join(config.GetConfigDir(), "loomgit", "store.db"), apply, cmd)
	},
}

func init() {
	retentionCmd.Flags().Bool("apply", false, "Remove eligible copies after showing the report")
	cli.RegisterCommand(retentionCmd)
}

func runRetention(ctx context.Context, path string, apply bool, cmd *cobra.Command) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No Loom Git retention records")
		return err
	} else if err != nil {
		return err
	}
	results, err := loomretention.RunAt(ctx, path, apply)
	for _, result := range results {
		if _, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s: %s\n", result.Action,
			result.Workspace, result.Change, result.Path, result.Reason); writeErr != nil {
			return writeErr
		}
	}
	return err
}
