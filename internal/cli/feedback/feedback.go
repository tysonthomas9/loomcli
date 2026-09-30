package feedback

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	loomfeedback "github.com/tysonthomas9/loomcli/internal/loomgit/feedback"
)

var addressCmd *cobra.Command

func init() {
	var workspace, deliveryID, target, attempt string
	var captureSHA string
	command := &cobra.Command{Use: "feedback", Short: "Manage feedback on published changes"}
	addressCmd = &cobra.Command{
		Use: "address <change>", Short: "Create a task copy and request a feedback revision",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := loomfeedback.Request(cmd.Context(), workspace, args[0], deliveryID, target, attempt)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Task copy: %s\nBase: %s\nRevision request: %s\n%s\n",
				request.Target, request.BaseSHA, request.RequestID, request.Prompt)
			return err
		},
	}
	addressCmd.Flags().StringVar(&workspace, "workspace", "", "Workspace ID")
	addressCmd.Flags().StringVar(&deliveryID, "delivery-id", "", "Verified feedback delivery ID")
	addressCmd.Flags().StringVar(&target, "target", "", "New task-copy path")
	addressCmd.Flags().StringVar(&attempt, "attempt", "", "Agent attempt ID")
	_ = addressCmd.MarkFlagRequired("workspace")
	_ = addressCmd.MarkFlagRequired("delivery-id")
	_ = addressCmd.MarkFlagRequired("target")
	_ = addressCmd.MarkFlagRequired("attempt")
	command.AddCommand(addressCmd)
	complete := &cobra.Command{
		Use: "complete <delivery-id>", Short: "Record the captured feedback revision",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			revision, err := loomfeedback.Complete(cmd.Context(), workspace, args[0], captureSHA)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Feedback addressed by revision %d of %s\n", revision.Number, revision.Change)
			return err
		},
	}
	complete.Flags().StringVar(&workspace, "workspace", "", "Workspace ID")
	complete.Flags().StringVar(&captureSHA, "capture-sha", "", "Captured commit SHA")
	_ = complete.MarkFlagRequired("workspace")
	_ = complete.MarkFlagRequired("capture-sha")
	command.AddCommand(complete)
	cli.RegisterCommand(command)
}
