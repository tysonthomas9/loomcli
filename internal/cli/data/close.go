package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
)

var (
	closeReason  string
	closeSession string
	closeForce   bool
)

var closeCmd = &cobra.Command{
	Use:   "close <issue-id>",
	Short: "Close an issue (HTTP)",
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
		params := backend.CloseParams{
			Reason:  closeReason,
			Session: closeSession,
			Force:   closeForce,
		}
		if _, err := ab.Close(ctx, args[0], params); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "closed "+args[0], outputFormat)
	},
}

func init() {
	closeCmd.Flags().StringVar(&closeReason, "reason", "", "Reason for closing")
	closeCmd.Flags().StringVar(&closeSession, "session", "", "Session identifier to attach to the close event")
	closeCmd.Flags().BoolVar(&closeForce, "force", false, "Force close even if blocked or dependencies open")
}
