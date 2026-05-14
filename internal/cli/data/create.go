package data

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type createIssueFlags struct {
	parent             string
	title              string
	description        string
	status             string
	issueType          string
	priority           int
	design             string
	acceptanceCriteria string
	notes              string
	assignee           string
	owner              string
	createdBy          string
	externalRef        string
	estimatedMinutes   int
	labels             []string
	dependencies       []string
	sourceRepo         string
	dueAt              string
	deferUntil         string
}

var createCmd = newCreateCmd()

func newCreateCmd() *cobra.Command {
	var flags createIssueFlags
	cmd := &cobra.Command{
		Use:   "create --title <title>",
		Short: "Create an issue",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params, err := createParamsFromFlags(cmd, flags)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ib, err := getIssueBackend(ctx)
			if err != nil {
				return err
			}
			created, err := ib.Create(ctx, params)
			if err != nil {
				return err
			}
			return printCreatedIssue(os.Stdout, created, outputFormat)
		},
	}

	cmd.Flags().StringVar(&flags.parent, "parent", "", "Parent issue ID")
	cmd.Flags().StringVar(&flags.title, "title", "", "Issue title")
	cmd.Flags().StringVar(&flags.description, "description", "", "Issue description")
	cmd.Flags().StringVar(&flags.status, "status", "", "Initial status")
	cmd.Flags().StringVar(&flags.issueType, "type", "task", "Issue type (task|bug|feature|epic|chore)")
	cmd.Flags().IntVar(&flags.priority, "priority", 2, "Priority (0 critical, 1 high, 2 medium, 3 low, 4 backlog)")
	cmd.Flags().StringVar(&flags.design, "design", "", "Design notes")
	cmd.Flags().StringVar(&flags.acceptanceCriteria, "acceptance-criteria", "", "Acceptance criteria")
	cmd.Flags().StringVar(&flags.notes, "notes", "", "Internal notes")
	cmd.Flags().StringVar(&flags.assignee, "assignee", "", "Assignee")
	cmd.Flags().StringVar(&flags.owner, "owner", "", "Owner")
	cmd.Flags().StringVar(&flags.createdBy, "created-by", "", "Creator")
	cmd.Flags().StringVar(&flags.externalRef, "external-ref", "", "External reference")
	cmd.Flags().IntVar(&flags.estimatedMinutes, "estimated-minutes", 0, "Estimated effort in minutes")
	cmd.Flags().StringArrayVar(&flags.labels, "label", nil, "Label to attach (repeatable)")
	cmd.Flags().StringArrayVar(&flags.dependencies, "depends-on", nil, "Dependency issue ID (repeatable)")
	cmd.Flags().StringVar(&flags.sourceRepo, "source-repo", "", "Source repository ID")
	cmd.Flags().StringVar(&flags.dueAt, "due-at", "", "Due timestamp")
	cmd.Flags().StringVar(&flags.deferUntil, "defer-until", "", "Defer until timestamp")
	return cmd
}

func createParamsFromFlags(cmd *cobra.Command, flags createIssueFlags) (backend.CreateParams, error) {
	flags.title = strings.TrimSpace(flags.title)
	if flags.title == "" {
		return backend.CreateParams{}, fmt.Errorf("--title is required")
	}
	params := backend.CreateParams{
		Parent:             flags.parent,
		Title:              flags.title,
		Description:        flags.description,
		Status:             flags.status,
		IssueType:          flags.issueType,
		Priority:           flags.priority,
		Design:             flags.design,
		AcceptanceCriteria: flags.acceptanceCriteria,
		Notes:              flags.notes,
		Assignee:           flags.assignee,
		Owner:              flags.owner,
		CreatedBy:          flags.createdBy,
		ExternalRef:        flags.externalRef,
		Labels:             flags.labels,
		Dependencies:       flags.dependencies,
		SourceRepo:         flags.sourceRepo,
		DueAt:              flags.dueAt,
		DeferUntil:         flags.deferUntil,
	}
	if cmd.Flags().Changed("estimated-minutes") {
		params.EstimatedMinutes = &flags.estimatedMinutes
	}
	return params, nil
}
