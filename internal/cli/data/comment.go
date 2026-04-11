package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
)

var commentAuthor string

var commentCmd = &cobra.Command{
	Use:   "comment <issue-id> <text>",
	Short: "Add a comment to an issue (HTTP)",
	Args:  cobra.ExactArgs(2),
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
		params := backend.CommentAddParams{
			IssueID: args[0],
			Author:  commentAuthor,
			Text:    args[1],
		}
		if _, err := ab.AddComment(ctx, params); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "comment added to "+args[0], outputFormat)
	},
}

func init() {
	commentCmd.Flags().StringVar(&commentAuthor, "author", "", "Comment author (defaults to server-side session user)")
}
