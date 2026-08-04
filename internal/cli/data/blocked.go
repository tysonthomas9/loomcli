package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var (
	blockedLimit  int
	blockedType   string
	blockedParent string
)

var blockedCmd = &cobra.Command{
	Use:   "blocked",
	Short: "List issues that are blocked by other open issues (HTTP)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		opts := backend.BlockedOpts{
			Limit:    blockedLimit,
			Type:     blockedType,
			ParentID: blockedParent,
		}
		items, err := ib.Blocked(ctx, opts)
		if err != nil {
			return err
		}
		return printIssueList(os.Stdout, items, outputFormat)
	},
}

func init() {
	blockedCmd.Flags().IntVar(&blockedLimit, "limit", 0, "Maximum number of results (0 = server default)")
	blockedCmd.Flags().StringVar(&blockedType, "type", "", "Filter by issue type")
	blockedCmd.Flags().StringVar(&blockedParent, "parent", "", "Filter by parent issue ID")
}
