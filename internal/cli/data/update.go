package data

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

var (
	updateStatus      string
	updateAssignee    string
	updateNotes       string
	updateDesign      string
	updatePriority    int
	updateTitle       string
	updateDescription string
	updateDescFile    string
)

var updateCmd = &cobra.Command{
	Use:   "update <issue-id>",
	Short: "Update issue fields",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		descFromFlag := cmd.Flags().Changed("description")
		descFromFile := cmd.Flags().Changed("description-from-file")
		if descFromFlag && descFromFile {
			return fmt.Errorf("--description and --description-from-file are mutually exclusive")
		}
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
		if cmd.Flags().Changed("title") {
			params.Title = &updateTitle
		}
		if descFromFlag {
			params.Description = &updateDescription
		}
		if descFromFile {
			body, err := readDescriptionFile(updateDescFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
			params.Description = &body
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
	updateCmd.Flags().StringVar(&updateTitle, "title", "", "Set title")
	updateCmd.Flags().StringVar(&updateDescription, "description", "", "Set description")
	updateCmd.Flags().StringVar(&updateDescFile, "description-from-file", "", "Read description from file (use - for stdin)")
}

func readDescriptionFile(path string, stdin io.Reader) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read description from stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: user-supplied --description-from-file path; this is the intended behavior.
	if err != nil {
		return "", fmt.Errorf("read description from %q: %w", path, err)
	}
	return string(b), nil
}
