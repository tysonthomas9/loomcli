package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
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
		opts := backend.ReadyOpts{
			Limit:    readyLimit,
			Assignee: readyAssignee,
			Type:     readyType,
			ParentID: readyParent,
		}
		items, err := ib.Ready(ctx, opts)
		if err != nil {
			return err
		}
		return printIssueList(os.Stdout, items, outputFormat)
	},
}

func init() {
	readyCmd.Flags().IntVar(&readyLimit, "limit", 0, "Maximum number of results (0 = server default)")
	readyCmd.Flags().StringVar(&readyAssignee, "assignee", "", "Filter by assignee")
	readyCmd.Flags().StringVar(&readyType, "type", "", "Filter by issue type")
	readyCmd.Flags().StringVar(&readyParent, "parent", "", "Filter by parent issue ID")
}
