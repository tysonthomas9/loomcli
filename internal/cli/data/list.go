package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var (
	listStatus   string
	listType     string
	listParent   string
	listPriority int
	listLimit    int
	listLabels   []string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List issues (HTTP)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		opts := backend.ListOpts{
			Status:    listStatus,
			IssueType: listType,
			ParentID:  listParent,
			Labels:    listLabels,
			Limit:     listLimit,
		}
		if cmd.Flags().Changed("priority") {
			p := listPriority
			opts.Priority = &p
		}
		items, err := ib.List(ctx, opts)
		if err != nil {
			return err
		}
		return printIssueList(os.Stdout, items, outputFormat)
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (open|in_progress|review|closed|...)")
	listCmd.Flags().StringVar(&listType, "type", "", "Filter by issue type (task|bug|feature|epic|...)")
	listCmd.Flags().StringVar(&listParent, "parent", "", "Filter by parent issue ID")
	listCmd.Flags().IntVar(&listPriority, "priority", 0, "Filter by priority (0-4)")
	listCmd.Flags().IntVar(&listLimit, "limit", 0, "Maximum number of results (0 = server default)")
	listCmd.Flags().StringArrayVar(&listLabels, "label", nil,
		"Filter by label (repeatable; an issue must carry every label given). "+
			"With more than one label, only the first is filtered server-side and the "+
			"rest are matched afterwards, so --limit bounds the pre-filter fetch.")
}
