package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend/api"
)

var showCmd = &cobra.Command{
	Use:   "show <issue-id>",
	Short: "Show a single issue by ID (HTTP)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cli, url, err := getHTTPClient()
		if err != nil {
			return err
		}
		wsID, err := resolveWorkspaceID(ctx, cli, url)
		if err != nil {
			return err
		}
		ab, err := api.New(api.Config{BaseURL: url, WorkspaceID: wsID, HTTPClient: cli})
		if err != nil {
			return err
		}
		detail, err := ab.Get(ctx, args[0])
		if err != nil {
			return err
		}
		return printIssueDetail(os.Stdout, detail, outputFormat)
	},
}
