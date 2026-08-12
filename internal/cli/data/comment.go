package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

var commentAuthor string

var commentCmd = &cobra.Command{
	Use:   "comment <issue-id> <text>",
	Short: "Add a comment to an issue (HTTP)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		itemsAPI, err := getWorkItems(ctx)
		if err != nil {
			return err
		}
		command := workitems.AddCommentCommand{
			IssueID: args[0],
			Author:  commentAuthor,
			Text:    args[1],
		}
		if _, err := itemsAPI.AddComment(ctx, command); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "comment added to "+args[0], outputFormat)
	},
}

func init() {
	commentCmd.Flags().StringVar(&commentAuthor, "author", "", "Comment author (defaults to server-side session user)")
}
