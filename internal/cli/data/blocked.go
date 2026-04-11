package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
)

var (
	blockedLimit int
	blockedType  string
)

var blockedCmd = &cobra.Command{
	Use:   "blocked",
	Short: "List issues that are blocked by other open issues (HTTP)",
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
		opts := backend.BlockedOpts{
			Limit: blockedLimit,
			Type:  blockedType,
		}
		items, err := ab.Blocked(ctx, opts)
		if err != nil {
			return err
		}
		return printIssueList(os.Stdout, items, outputFormat)
	},
}

func init() {
	blockedCmd.Flags().IntVar(&blockedLimit, "limit", 0, "Maximum number of results (0 = server default)")
	blockedCmd.Flags().StringVar(&blockedType, "type", "", "Filter by issue type")
}
