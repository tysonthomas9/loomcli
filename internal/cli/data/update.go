package data

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
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
	updateExternalRef  string
	updateAddLabels    []string
	updateRemoveLabels []string
	updateAddDeps      []string
	updateRemoveDeps   []string
)

var updateCmd = &cobra.Command{
	Use:   "update <issue-id>",
	Short: "Update issue fields",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		taskRunClient, active, err := taskRunDataClientFromEnv()
		if err != nil {
			return err
		}
		if active {
			design, format, err := taskRunDesignUpdateFromFlags(cmd)
			if err != nil {
				return err
			}
			if err := taskRunClient.updateDesign(ctx, args[0], design, format); err != nil {
				return err
			}
			return printMessageResult(os.Stdout, "updated "+args[0], outputFormat)
		}
		params, fieldsChanged, err := updateParamsFromFlags(cmd)
		if err != nil {
			return err
		}
		itemsAPI, err := getWorkItems(ctx)
		if err != nil {
			return err
		}
		// Field updates go through Update; dependency edges are a separate
		// backend resource and are composed below. When no flags at all were
		// given, still call Update so the backend's canonical "no fields"
		// validation error surfaces.
		depsChanged := len(updateAddDeps) > 0 || len(updateRemoveDeps) > 0
		if fieldsChanged || !depsChanged {
			params.IssueID = args[0]
			if err := enforceBlockReason(ctx, itemsAPI, params); err != nil {
				return err
			}
			if _, err := itemsAPI.Patch(ctx, params); err != nil {
				return err
			}
		}
		if err := applyDependencyFlags(ctx, itemsAPI, args[0]); err != nil {
			return err
		}
		return printMessageResult(os.Stdout, "updated "+args[0], outputFormat)
	},
}

// taskRunDesignUpdateFromFlags admits only the one Work Item mutation exposed
// to a model-controlled TaskRun. Inspecting local flags (rather than enumerating
// today's other fields) makes future update flags fail closed automatically.
func taskRunDesignUpdateFromFlags(cmd *cobra.Command) (string, *string, error) {
	var forbidden []string
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Changed && flag.Name != "design" && flag.Name != "design-format" {
			forbidden = append(forbidden, "--"+flag.Name)
		}
	})
	if len(forbidden) > 0 {
		return "", nil, fmt.Errorf("task-run data update only permits --design and optional --design-format; rejected %s", strings.Join(forbidden, ", "))
	}
	if !cmd.Flags().Changed("design") {
		return "", nil, fmt.Errorf("task-run data update requires --design")
	}
	if strings.TrimSpace(updateDesign) == "" {
		return "", nil, fmt.Errorf("task-run data update requires a nonblank --design")
	}
	var format *string
	if cmd.Flags().Changed("design-format") {
		value := strings.TrimSpace(updateDesignFormat)
		if value != "" {
			if err := validateDesignFormat(value); err != nil {
				return "", nil, err
			}
			format = &value
		}
	}
	return updateDesign, format, nil
}

// updateParamsFromFlags builds UpdateParams from the changed field flags. The
// boolean reports whether any field-level flag was set; dependency flags are
// handled separately (see applyDependencyFlags).
func updateParamsFromFlags(cmd *cobra.Command) (workitems.PatchCommand, bool, error) {
	descFromFlag := cmd.Flags().Changed("description")
	descFromFile := cmd.Flags().Changed("description-from-file")
	if descFromFlag && descFromFile {
		return workitems.PatchCommand{}, false, fmt.Errorf("--description and --description-from-file are mutually exclusive")
	}
	params := workitems.PatchCommand{}
	changed := applyDirectUpdateFlags(cmd, &params)
	if applied, err := applyDesignFormatFlag(cmd, &params); err != nil {
		return workitems.PatchCommand{}, false, err
	} else if applied {
		changed = true
	}
	if descFromFlag {
		params.Description = &updateDescription
		changed = true
	}
	if descFromFile {
		body, err := readDescriptionFile(updateDescFile, cmd.InOrStdin())
		if err != nil {
			return workitems.PatchCommand{}, false, err
		}
		params.Description = &body
		changed = true
	}
	return params, changed, nil
}

func applyDirectUpdateFlags(cmd *cobra.Command, params *workitems.PatchCommand) bool {
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
	if cmd.Flags().Changed("priority") {
		params.Priority = &updatePriority
		changed = true
	}
	if cmd.Flags().Changed("title") {
		params.Title = &updateTitle
		changed = true
	}
	if cmd.Flags().Changed("external-ref") {
		params.ExternalRef = &updateExternalRef
		changed = true
	}
	if cmd.Flags().Changed("add-label") {
		params.AddLabels = append([]string(nil), updateAddLabels...)
		changed = true
	}
	if cmd.Flags().Changed("remove-label") {
		params.RemoveLabels = append([]string(nil), updateRemoveLabels...)
		changed = true
	}
	return changed
}

func applyDesignFormatFlag(cmd *cobra.Command, params *workitems.PatchCommand) (bool, error) {
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
func enforceBlockReason(ctx context.Context, itemsAPI workitems.API, params workitems.PatchCommand) error {
	if params.Status == nil || *params.Status != "blocked" {
		return nil
	}
	if params.Notes != nil && strings.TrimSpace(*params.Notes) != "" {
		return nil
	}
	cur, err := itemsAPI.Get(ctx, workitems.GetQuery{IssueID: params.IssueID})
	if err != nil || cur == nil {
		return nil // can't verify existing notes; don't block a legitimate update
	}
	if strings.TrimSpace(cur.Notes) != "" {
		return nil
	}
	return fmt.Errorf(
		"refusing to set %s to blocked without a reason: pass --notes \"BLOCKED: <why + what unblocks it>\" "+
			"(or set notes first) so a human can see why it's blocked and what clears it — a bare blocked issue gives no signal on the board",
		params.IssueID)
}

// applyDependencyFlags adds/removes dependency edges for the update command.
// Dependencies are not part of the issue PATCH schema — they are a separate
// resource with dedicated endpoints — so the update command composes the
// Work Items dependency-command port implemented by both real transports.
func applyDependencyFlags(ctx context.Context, itemsAPI workitems.API, id string) error {
	for _, dep := range updateAddDeps {
		if err := itemsAPI.AddDependency(ctx, workitems.AddDependencyCommand{IssueID: id, DependsOnID: dep, Type: "blocks"}); err != nil {
			return err
		}
	}
	for _, dep := range updateRemoveDeps {
		if err := itemsAPI.RemoveDependency(ctx, workitems.RemoveDependencyCommand{IssueID: id, DependsOnID: dep, Type: "blocks"}); err != nil {
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
	updateCmd.Flags().StringVar(&updateExternalRef, "external-ref", "", "Set external reference")
	updateCmd.Flags().StringArrayVar(&updateAddLabels, "add-label", nil, "Add label (repeatable)")
	updateCmd.Flags().StringArrayVar(&updateRemoveLabels, "remove-label", nil, "Remove label (repeatable)")
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
