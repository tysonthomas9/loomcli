package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

var (
	listStatus   string
	listType     string
	listParent   string
	listPriority int
	listLimit    int
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List issues (HTTP)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		itemsAPI, err := getWorkItems(ctx)
		if err != nil {
			return err
		}
		filter := workitems.ListFilter{
			Status:    listStatus,
			IssueType: listType,
			ParentID:  listParent,
			Limit:     listLimit,
		}
		if cmd.Flags().Changed("priority") {
			p := listPriority
			filter.Priority = &p
		}
		result, err := itemsAPI.List(ctx, workitems.ListQuery{Filter: filter})
		if err != nil {
			return err
		}
		items := make([]workitems.IssueSummary, len(result.Issues))
		for index := range result.Issues {
			items[index] = result.Issues[index].IssueSummary
		}
		return printWorkItemSummaries(os.Stdout, items, outputFormat)
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (open|in_progress|review|closed|...)")
	listCmd.Flags().StringVar(&listType, "type", "", "Filter by issue type (task|bug|feature|epic|...)")
	listCmd.Flags().StringVar(&listParent, "parent", "", "Filter by parent issue ID")
	listCmd.Flags().IntVar(&listPriority, "priority", 0, "Filter by priority (0-4)")
	listCmd.Flags().IntVar(&listLimit, "limit", 0, "Maximum number of results (0 = server default)")
}
