package data

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var (
	updateStatus   string
	updateAssignee string
	updateNotes    string
	updateDesign   string
	updatePriority int
)

var updateCmd = &cobra.Command{
	Use:   "update <issue-id>",
	Short: "Update issue fields",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		params := backend.UpdateParams{}
		if cmd.Flags().Changed("status") {
			params.Status = &updateStatus
		}
		if cmd.Flags().Changed("assignee") {
			params.Assignee = &updateAssignee
		}
		if cmd.Flags().Changed("notes") {
			params.Notes = &updateNotes
		}
		if cmd.Flags().Changed("design") {
			params.Design = &updateDesign
		}
		if cmd.Flags().Changed("priority") {
			params.Priority = &updatePriority
		}
		if err := ib.Update(ctx, args[0], params); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "updated "+args[0], outputFormat)
	},
}

func init() {
	updateCmd.Flags().StringVar(&updateStatus, "status", "", "Set status")
	updateCmd.Flags().StringVar(&updateAssignee, "assignee", "", "Set assignee")
	updateCmd.Flags().StringVar(&updateNotes, "notes", "", "Set notes")
	updateCmd.Flags().StringVar(&updateDesign, "design", "", "Set design")
	updateCmd.Flags().IntVar(&updatePriority, "priority", 0, "Set priority")
}
