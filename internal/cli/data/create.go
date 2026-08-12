package data

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
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
	idempotencyKey     string
	noIdempotency      bool
	force              bool
}

var createCmd = newCreateCmd()

func newCreateCmd() *cobra.Command {
	var flags createIssueFlags
	cmd := &cobra.Command{
		Use:   "create --title <title>",
		Short: "Create an issue",
		Long: `Create an issue.

Idempotency: by default the create sends an X-Idempotency-Key derived from
the issue content and the current UTC date, so re-running the exact same
command (e.g. after a lost response) returns the already-created issue
instead of minting a duplicate. Use --idempotency-key to supply your own
key, or --no-idempotency to disable deduplication entirely.

--force only bypasses the server's soft-duplicate guard (same title+type
created moments ago); with an identical command the idempotency key still
returns the existing issue — combine with --no-idempotency to intentionally
create an identical duplicate.

Output: on success the last line of stdout is "CREATED <issue-id>" in text
mode. With -o json, stdout stays pure JSON (the created issue) and the
"CREATED <issue-id>" line goes to stderr. The exit code is non-zero only on
real failure, so success is checkable from the exit code alone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params, err := createParamsFromFlags(cmd, flags)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			itemsAPI, err := getWorkItems(ctx)
			if err != nil {
				return err
			}
			created, err := itemsAPI.Create(ctx, params)
			if err != nil {
				return err
			}
			return printCreatedIssue(os.Stdout, os.Stderr, created, outputFormat)
		},
	}

	registerCreateFlags(cmd, &flags)
	return cmd
}

func registerCreateFlags(cmd *cobra.Command, flags *createIssueFlags) {
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
	cmd.Flags().StringVar(&flags.idempotencyKey, "idempotency-key", "", "Explicit idempotency key (default: content hash of the create body + UTC date)")
	cmd.Flags().BoolVar(&flags.noIdempotency, "no-idempotency", false, "Disable idempotent create (always mint a new issue)")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Bypass the soft-duplicate guard only; identical duplicates also need --no-idempotency")
}

func createParamsFromFlags(cmd *cobra.Command, flags createIssueFlags) (workitems.CreateCommand, error) {
	flags.title = strings.TrimSpace(flags.title)
	if flags.title == "" {
		return workitems.CreateCommand{}, fmt.Errorf("--title is required")
	}
	params := workitems.CreateCommand{
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
	params.Force = flags.force
	if !flags.noIdempotency {
		if flags.idempotencyKey != "" {
			params.IdempotencyKey = flags.idempotencyKey
		} else if key, err := computeCreateKey(params); err == nil {
			// Best-effort: an unhashable body just skips dedup.
			params.IdempotencyKey = key
		}
	}
	return params, nil
}
