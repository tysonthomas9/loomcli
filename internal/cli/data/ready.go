package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
)

var (
	readyLimit    int
	readyAssignee string
	readyType     string
)

var readyCmd = &cobra.Command{
	Use:   "ready",
	Short: "List issues that are ready to work on (HTTP)",
	Args:  cobra.NoArgs,
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
		opts := backend.ReadyOpts{
			Limit:    readyLimit,
			Assignee: readyAssignee,
			Type:     readyType,
		}
		items, err := ab.Ready(ctx, opts)
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
}
