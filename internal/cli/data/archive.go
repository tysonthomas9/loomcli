package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var archiveReason string

var archiveCmd = &cobra.Command{
	Use:   "archive <issue-id>",
	Short: "Archive an issue, hiding it from the default views (HTTP)",
	Long: `Archive an issue by moving it to the terminal "tombstone" status.

Archiving hides an issue from the default views without deleting it; use
'loom data unarchive' to restore it. Archive is idempotent — archiving an
already-archived issue succeeds. The reason is only recorded when the issue
does not already carry a close reason, so archiving a closed issue preserves
what the close said.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		if err := ib.Archive(ctx, args[0], backend.ArchiveParams{Reason: archiveReason}); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "archived "+args[0], outputFormat)
	},
}

var unarchiveCmd = &cobra.Command{
	Use:   "unarchive <issue-id>",
	Short: "Restore an archived issue (HTTP)",
	Long: `Restore an archived issue to its pre-archive status.

Unlike archive this is strict: restoring an issue that was never archived is
an error, not a no-op.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		if err := ib.Unarchive(ctx, args[0]); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "unarchived "+args[0], outputFormat)
	},
}

func init() {
	archiveCmd.Flags().StringVar(&archiveReason, "reason", "", "Reason for archiving")
}
