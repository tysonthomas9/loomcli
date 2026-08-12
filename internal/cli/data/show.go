package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

var showCmd = &cobra.Command{
	Use:   "show <issue-id>",
	Short: "Show a single issue by ID (HTTP)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		taskRunClient, active, err := taskRunDataClientFromEnv()
		if err != nil {
			return err
		}
		if active {
			detail, err := taskRunClient.getTask(ctx, args[0])
			if err != nil {
				return err
			}
			return printIssueDetail(os.Stdout, detail, outputFormat)
		}
		itemsAPI, err := getWorkItems(ctx)
		if err != nil {
			return err
		}
		detail, err := itemsAPI.Get(ctx, workitems.GetQuery{IssueID: args[0]})
		if err != nil {
			return err
		}
		return printIssueDetail(os.Stdout, detail, outputFormat)
	},
}
