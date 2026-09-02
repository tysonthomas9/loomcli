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

var (
	updateStatus       string
	updateAssignee     string
	updateNotes        string
	updateDesign       string
	updateDesignFormat string
	updatePriority     int
	updateTitle        string
	updateDescription  string
	updateDescFile     string
	updateAddDeps      []string
	updateRemoveDeps   []string
	updateAddLabels    []string
	updateRemoveLabels []string
	updateForce        bool
	updateParent       string
	updateSourceRepo   string
)

var updateCmd = &cobra.Command{
	Use:   "update <issue-id>",
	Short: "Update issue fields",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		params, fieldsChanged, err := updateParamsFromFlags(cmd)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		ib, err := getIssueBackend(ctx)
		if err != nil {
			return err
		}
		// Field updates go through Update; dependency edges are a separate
		// backend resource and are composed below. When no flags at all were
		// given, still call Update so the backend's canonical "no fields"
		// validation error surfaces.
		depsChanged := len(updateAddDeps) > 0 || len(updateRemoveDeps) > 0
		if fieldsChanged || !depsChanged {
			if err := enforceBlockReason(ctx, ib, args[0], params); err != nil {
				return err
			}
			if err := ib.Update(ctx, args[0], params); err != nil {
				return err
			}
		}
		if err := applyDependencyFlags(ctx, ib, args[0]); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "updated "+args[0], outputFormat)
	},
}

// updateParamsFromFlags builds UpdateParams from the changed field flags. The
// boolean reports whether any field-level flag was set; dependency flags are
// handled separately (see applyDependencyFlags).
func updateParamsFromFlags(cmd *cobra.Command) (backend.UpdateParams, bool, error) {
	descFromFlag := cmd.Flags().Changed("description")
	descFromFile := cmd.Flags().Changed("description-from-file")
	if descFromFlag && descFromFile {
		return backend.UpdateParams{}, false, fmt.Errorf("--description and --description-from-file are mutually exclusive")
	}
	params := backend.UpdateParams{}
	changed := false
	if cmd.Flags().Changed("status") {
		params.Status = &updateStatus
		changed = true
	}
	if cmd.Flags().Changed("assignee") {
		params.Assignee = &updateAssignee
		changed = true
	}
	if cmd.Flags().Changed("notes") {
		params.Notes = &updateNotes
		changed = true
	}
	if cmd.Flags().Changed("design") {
		params.Design = &updateDesign
		changed = true
	}
	if applied, err := applyDesignFormatFlag(cmd, &params); err != nil {
		return backend.UpdateParams{}, false, err
	} else if applied {
		changed = true
	}
	if cmd.Flags().Changed("priority") {
		params.Priority = &updatePriority
		changed = true
	}
	if cmd.Flags().Changed("title") {
		params.Title = &updateTitle
		changed = true
	}
	// Changed(), not a non-empty check: --parent "" must mean "detach", which
	// is indistinguishable from "flag omitted" by value alone.
	if cmd.Flags().Changed("parent") {
		params.Parent = &updateParent
		changed = true
	}
	if cmd.Flags().Changed("source-repo") {
		params.Repo = &updateSourceRepo
		changed = true
	}
	if applyLabelFlags(cmd, &params) {
		changed = true
	}
	if applied, err := applyDescriptionFlags(cmd, &params, descFromFlag, descFromFile); err != nil {
		return backend.UpdateParams{}, false, err
	} else if applied {
		changed = true
	}
	return params, changed, nil
}

// applyDescriptionFlags resolves --description / --description-from-file into
// params.Description, reporting whether either was given. The caller rejects
// the two as mutually exclusive before this runs, so at most one applies.
func applyDescriptionFlags(cmd *cobra.Command, params *backend.UpdateParams, fromFlag, fromFile bool) (bool, error) {
	if fromFlag {
		params.Description = &updateDescription
		return true, nil
	}
	if !fromFile {
		return false, nil
	}
	body, err := readDescriptionFile(updateDescFile, cmd.InOrStdin())
	if err != nil {
		return false, err
	}
	params.Description = &body
	return true, nil
}

// applyLabelFlags copies the repeatable --add-label/--remove-label occurrences
// into the label deltas on params, reporting whether either flag was given.
// Labels are deltas, not a replacement: additions and removals name individual
// labels and leave every other label on the issue untouched, so SetLabels is
// never populated and the current label set is never read back first.
//
// The reported bool must feed updateParamsFromFlags' changed result: RunE only
// calls Update when fieldsChanged || !depsChanged, so a label flag combined
// with a dependency flag would otherwise skip Update, silently dropping the
// label while still reporting success.
func applyLabelFlags(cmd *cobra.Command, params *backend.UpdateParams) bool {
	changed := false
	if cmd.Flags().Changed("add-label") {
		params.AddLabels = updateAddLabels
		changed = true
	}
	if cmd.Flags().Changed("remove-label") {
		params.RemoveLabels = updateRemoveLabels
		changed = true
	}
	// --force is a modifier on the label deltas, not a field of its own: it
	// never counts as a change, so `update <id> --force` alone still reaches
	// the backend's "no fields" validation error instead of silently
	// succeeding.
	if cmd.Flags().Changed("force") {
		params.Force = updateForce
	}
	return changed
}

func applyDesignFormatFlag(cmd *cobra.Command, params *backend.UpdateParams) (bool, error) {
	if !cmd.Flags().Changed("design-format") {
		return false, nil
	}
	if err := validateDesignFormat(updateDesignFormat); err != nil {
		return false, err
	}
	params.DesignFormat = &updateDesignFormat
	return true, nil
}

func validateDesignFormat(format string) error {
	if format != "markdown" && format != "html" {
		return fmt.Errorf("--design-format must be markdown or html")
	}
	return nil
}

// enforceBlockReason refuses to move an issue to "blocked" without a reason, so
// a blocked issue always carries a human-readable explanation — the board
// surfaces that as the "blocked with notes" needs-attention state, whereas a
// bare blocked chip gives a human no signal that (or why) it needs them.
//
// A reason counts if it is supplied in this call (--notes) OR already present on
// the issue (the notes-then-status two-step flow). If the issue cannot be
// fetched to check existing notes, the update proceeds: this is best-effort
// guidance for the agent/human CLI path, not a hard gate that should break a
// legitimate update on a transient read error.
func enforceBlockReason(ctx context.Context, ib backend.IssueBackend, id string, params backend.UpdateParams) error {
	if params.Status == nil || *params.Status != "blocked" {
		return nil
	}
	if params.Notes != nil && strings.TrimSpace(*params.Notes) != "" {
		return nil
	}
	cur, err := ib.Get(ctx, id)
	if err != nil || cur == nil {
		return nil // can't verify existing notes; don't block a legitimate update
	}
	if strings.TrimSpace(cur.Notes) != "" {
		return nil
	}
	return fmt.Errorf(
		"refusing to set %s to blocked without a reason: pass --notes \"BLOCKED: <why + what unblocks it>\" "+
			"(or set notes first) so a human can see why it's blocked and what clears it — a bare blocked issue gives no signal on the board",
		id)
}

// applyDependencyFlags adds/removes dependency edges for the update command.
// Dependencies are not part of the issue PATCH schema — they are a separate
// resource with dedicated endpoints — so the update command composes the
// backend's Add/RemoveDependency calls, which both the direct fleet-db and
// the serve-API transports implement.
func applyDependencyFlags(ctx context.Context, ib backend.IssueBackend, id string) error {
	for _, dep := range updateAddDeps {
		if err := ib.AddDependency(ctx, backend.DepAddParams{FromID: id, ToID: dep, DepType: "blocks"}); err != nil {
			return err
		}
	}
	for _, dep := range updateRemoveDeps {
		if err := ib.RemoveDependency(ctx, backend.DepRemoveParams{FromID: id, ToID: dep, DepType: "blocks"}); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	updateCmd.Flags().StringVar(&updateStatus, "status", "", "Set status")
	updateCmd.Flags().StringVar(&updateAssignee, "assignee", "", "Set assignee")
	updateCmd.Flags().StringVar(&updateNotes, "notes", "", "Set notes")
	updateCmd.Flags().StringVar(&updateDesign, "design", "", "Set design")
	updateCmd.Flags().StringVar(&updateDesignFormat, "design-format", "", "Set design format (markdown or html)")
	updateCmd.Flags().IntVar(&updatePriority, "priority", 0, "Set priority")
	updateCmd.Flags().StringVar(&updateTitle, "title", "", "Set title")
	updateCmd.Flags().StringVar(&updateParent, "parent", "", "Set parent issue ID (\"\" detaches); requires a fleet-db that accepts parent_id on PATCH — against an older server the whole update fails, not just this field")
	updateCmd.Flags().StringVar(&updateDescription, "description", "", "Set description")
	updateCmd.Flags().StringVar(&updateDescFile, "description-from-file", "", "Read description from file (use - for stdin)")
	updateCmd.Flags().StringArrayVar(&updateAddDeps, "depends-on", nil, "Add dependency on issue ID (repeatable)")
	updateCmd.Flags().StringArrayVar(&updateRemoveDeps, "remove-depends-on", nil, "Remove dependency on issue ID (repeatable)")
	updateCmd.Flags().StringArrayVar(&updateAddLabels, "add-label", nil, "Add label (repeatable); other labels are preserved")
	updateCmd.Flags().StringArrayVar(&updateRemoveLabels, "remove-label", nil, "Remove label (repeatable); other labels are preserved. Reserved labels (e.g. \"operator\") also need --force")
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "Allow --remove-label to remove a reserved label such as \"operator\", which parks an issue for a human")
	updateCmd.Flags().StringVar(&updateSourceRepo, "source-repo", "", "Set source repo (pass an empty value to clear it)")
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
