package data

import (
	"os"

	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show <issue-id>",
	Short: "Show a single issue by ID (HTTP)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		detail, err := ib.Get(ctx, args[0])
		if err != nil {
			return err
		}
		return printIssueDetail(os.Stdout, detail, outputFormat)
	},
}
