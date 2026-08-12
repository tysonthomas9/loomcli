package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

var (
	readyLimit    int
	readyAssignee string
	readyType     string
	readyParent   string
)

var readyCmd = &cobra.Command{
	Use:   "ready",
	Short: "List issues that are ready to work on (HTTP)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		opts := workitems.AvailabilityQuery{
			Limit:     readyLimit,
			Assignee:  readyAssignee,
			IssueType: readyType,
			ParentID:  readyParent,
		}
		ready, ok := ib.(workitems.ReadyQueries)
		if !ok {
			return workitems.ErrUnavailable
		}
		items, err := ready.Ready(ctx, opts)
		if err != nil {
			return err
		}
		return printWorkItemSummaries(os.Stdout, items, outputFormat)
	},
}

func init() {
	readyCmd.Flags().IntVar(&readyLimit, "limit", 0, "Maximum number of results (0 = server default)")
	readyCmd.Flags().StringVar(&readyAssignee, "assignee", "", "Filter by assignee")
	readyCmd.Flags().StringVar(&readyType, "type", "", "Filter by issue type")
	readyCmd.Flags().StringVar(&readyParent, "parent", "", "Filter by parent issue ID")
}
