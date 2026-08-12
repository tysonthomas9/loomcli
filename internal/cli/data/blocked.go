package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
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
		itemsAPI, err := getWorkItems(ctx)
		if err != nil {
			return err
		}
		query := workitems.AvailabilityQuery{
			Limit:     blockedLimit,
			IssueType: blockedType,
			ParentID:  blockedParent,
		}
		items, err := itemsAPI.Blocked(ctx, query)
		if err != nil {
			return err
		}
		return printWorkItemSummaries(os.Stdout, items, outputFormat)
	},
}

func init() {
	blockedCmd.Flags().IntVar(&blockedLimit, "limit", 0, "Maximum number of results (0 = server default)")
	blockedCmd.Flags().StringVar(&blockedType, "type", "", "Filter by issue type")
	blockedCmd.Flags().StringVar(&blockedParent, "parent", "", "Filter by parent issue ID")
}
