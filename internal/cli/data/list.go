package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
)

var (
	listStatus   string
	listType     string
	listPriority int
	listLimit    int
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List issues (HTTP)",
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
		opts := backend.ListOpts{
			Status:    listStatus,
			IssueType: listType,
			Limit:     listLimit,
		}
		if cmd.Flags().Changed("priority") {
			p := listPriority
			opts.Priority = &p
		}
		items, err := ab.List(ctx, opts)
		if err != nil {
			return err
		}
		return printIssueList(os.Stdout, items, outputFormat)
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (open|in_progress|review|closed|...)")
	listCmd.Flags().StringVar(&listType, "type", "", "Filter by issue type (task|bug|feature|epic|...)")
	listCmd.Flags().IntVar(&listPriority, "priority", 0, "Filter by priority (0-4)")
	listCmd.Flags().IntVar(&listLimit, "limit", 0, "Maximum number of results (0 = server default)")
}
