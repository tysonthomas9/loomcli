package data

import (
	"context"
	"fmt"
	"io"
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

Source repo: with --parent and no --source-repo, the parent's source repo is
inherited. A task with no source repo is never claimed by an agent (routing
scores the repo match) and cannot be repaired afterwards — "data update" has no
--source-repo flag — so an omitted repo on a child task is a task that silently
never runs. An explicit --source-repo always wins.

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
			ib, err := getIssueBackend(ctx)
			if err != nil {
				return err
			}
			// Inheritance runs BEFORE the idempotency key is derived, because
			// the key must hash the body actually sent (see applyCreateIdempotency).
			inheritSourceRepoFromParent(ctx, cmd.ErrOrStderr(), ib, &params)
			applyCreateIdempotency(&params, flags)
			created, err := ib.Create(ctx, params)
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
	params.Force = flags.force
	return params, nil
}

// applyCreateIdempotency stamps the idempotency key. It is separate from
// createParamsFromFlags, and called later, because the default key is a hash of
// the create BODY: anything that still mutates params — inheritance below — has
// to happen first, or the key would describe a body that was never sent. That is
// not cosmetic. computeCreateKey deliberately hashes the same projection the
// wire request is built from so a default key can never trip fleet-db's
// "key was already used with a different request body" 409; keying off a
// pre-inheritance body would reintroduce exactly that conflict.
func applyCreateIdempotency(params *backend.CreateParams, flags createIssueFlags) {
	if flags.noIdempotency {
		return
	}
	if flags.idempotencyKey != "" {
		params.IdempotencyKey = flags.idempotencyKey
		return
	}
	if key, err := computeCreateKey(*params); err == nil {
		// Best-effort: an unhashable body just skips dedup.
		params.IdempotencyKey = key
	}
}

// inheritSourceRepoFromParent fills in SourceRepo from the parent issue when the
// caller named a parent but no repo.
//
// A child created without a source repo is accepted, renders normally on the
// board, and is then claimed by nobody: agent routing scores the repo match, so
// a repo-less task never matches an agent. It is also unrepairable after the
// fact — `data update` has no --source-repo flag — so the only fix is to delete
// the task and create it again.
//
// That combination makes the omission worth closing here rather than in each
// caller's documentation. It was found when a decomposition produced three
// perfectly good child tasks that simply never ran, and nothing about them
// looked wrong.
//
// An explicit --source-repo always wins, so this can only ever fill a gap, never
// override an intent. Inheriting from the parent is the right default because a
// child task is work on the same codebase as the parent by construction; a child
// that genuinely belongs to another repo still says so explicitly.
func inheritSourceRepoFromParent(ctx context.Context, warn io.Writer, ib backend.IssueBackend, params *backend.CreateParams) {
	if params.Parent == "" || params.SourceRepo != "" {
		return
	}
	parent, err := ib.Get(ctx, params.Parent)
	if err != nil {
		// Deliberately not fatal: creates that work today must keep working, and
		// the parent may be unreadable for reasons that have nothing to do with
		// this create (permissions, a backend hiccup). But it is warned about,
		// because the result is a task that will silently never be claimed.
		fmt.Fprintf(warn, "warning: could not read parent %s to inherit --source-repo (%v); "+
			"creating without a source repo, which means no agent will claim this task\n", params.Parent, err)
		return
	}
	if parent == nil || parent.SourceRepo == "" {
		// A parent with no repo of its own has nothing to lend. Silent: the
		// caller is no worse off than before, and a warning here would fire on
		// every child of every repo-less epic.
		return
	}
	params.SourceRepo = parent.SourceRepo
}
