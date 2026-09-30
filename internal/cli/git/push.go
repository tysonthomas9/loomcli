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

var pushAll bool
var pushWorkspace string
var pushLead string
var pushRequestID string
var pushApply = apply.ApplyLocal
var pushResolver = cli.NewResolver

var pushCmd = &cobra.Command{
	Use:     "push <change> <revision>",
	Short:   "Apply an approved revision to the local working area",
	GroupID: "git",
	Args:    cobra.ExactArgs(2),
	RunE:    runPush,
}

func init() {
	pushCmd.Flags().BoolVarP(&pushAll, "all", "a", false, "Unavailable for revisions; apply one change at a time")
	pushCmd.Flags().StringVarP(&pushWorkspace, "workspace", "W", "", "Workspace to operate on")
	pushCmd.Flags().StringVar(&pushLead, "lead", "", "Lead working area (default: workspace lead)")
	pushCmd.Flags().StringVar(&pushRequestID, "request-id", "", "Idempotency key for this apply")
	cli.RegisterCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) error {
	if pushAll {
		return fmt.Errorf("--all is unavailable; provide change, revision and lead to Apply")
	}
	revision, err := strconv.Atoi(args[1])
	if err != nil || revision < 1 {
		return fmt.Errorf("revision must be a positive number; branch Push is unavailable")
	}
	resolver, err := pushResolver()
	if err != nil {
		return err
	}
	if pushWorkspace != "" {
		if err := resolver.SetWorkspace(pushWorkspace); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.Workspace]
	requestID := pushRequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	result, err := pushApply(cmd.Context(), apply.Request{
		Workspace: workspace.ID, Change: args[0], Revision: revision,
		Lead: pushLead, RequestID: requestID,
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
