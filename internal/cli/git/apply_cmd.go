package git

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
)

var applyAll bool
var applyWorkspace string
var applyLead string
var applyRequestID string
var applyRevision = apply.ApplyLocal
var applyResolver = cli.NewResolver

var applyCmd = &cobra.Command{
	Use:     "apply <change> <revision>",
	Short:   "Apply an approved revision to the local working area",
	GroupID: "git",
	// Plumbing: approve applies; this repairs and tests (S3).
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE:   runApply,
}

func init() {
	applyCmd.Flags().BoolVarP(&applyAll, "all", "a", false, "Unavailable for revisions; apply one change at a time")
	applyCmd.Flags().StringVarP(&applyWorkspace, "workspace", "W", "", "Workspace to operate on")
	applyCmd.Flags().StringVar(&applyLead, "lead", "", "Lead working area (default: workspace lead)")
	applyCmd.Flags().StringVar(&applyRequestID, "request-id", "", "Idempotency key for this apply")
	cli.RegisterCommand(applyCmd)
}

func runApply(cmd *cobra.Command, args []string) error {
	if applyAll {
		return fmt.Errorf("--all is unavailable; provide change, revision and lead to Apply")
	}
	revision, err := strconv.Atoi(args[1])
	if err != nil || revision < 1 {
		return fmt.Errorf("revision must be a positive number; branch input is unavailable")
	}
	resolver, err := resolverFor(applyWorkspace, applyResolver)
	if err != nil {
		return err
	}
	if applyWorkspace != "" {
		if err := resolver.SetWorkspace(applyWorkspace); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.Workspace]
	requestID := applyRequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	result, err := applyRevision(cmd.Context(), apply.Request{
		Workspace: workspace.ID, Change: args[0], Revision: revision,
		Lead: applyLead, RequestID: requestID,
	})
	if err != nil {
		return applyError(result, err)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "Applied revision to local working area")
	return err
}

func applyError(result apply.Result, err error) error {
	if len(result.Paths) > 0 {
		return fmt.Errorf("%w\nConflicting files:\n  %s", err, strings.Join(result.Paths, "\n  "))
	}
	return err
}
