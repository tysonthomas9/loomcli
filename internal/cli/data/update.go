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
	if descFromFlag {
		params.Description = &updateDescription
		changed = true
	}
	if descFromFile {
		body, err := readDescriptionFile(updateDescFile, cmd.InOrStdin())
		if err != nil {
			return backend.UpdateParams{}, false, err
		}
		params.Description = &body
		changed = true
	}
	return params, changed, nil
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
	updateCmd.Flags().StringVar(&updateDescription, "description", "", "Set description")
	updateCmd.Flags().StringVar(&updateDescFile, "description-from-file", "", "Read description from file (use - for stdin)")
	updateCmd.Flags().StringArrayVar(&updateAddDeps, "depends-on", nil, "Add dependency on issue ID (repeatable)")
	updateCmd.Flags().StringArrayVar(&updateRemoveDeps, "remove-depends-on", nil, "Remove dependency on issue ID (repeatable)")
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
