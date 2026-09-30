package git

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

var approveWorkspace string
var approveLead string
var approveResolver = cli.NewResolver
var approveLocal = apply.ApproveLocal

var approveCmd = &cobra.Command{
	Use:     "approve <change> <revision>",
	Short:   "Approve a revision and follow it in a lead working area",
	GroupID: "git",
	Args:    cobra.ExactArgs(2),
	RunE:    runApprove,
}

func init() {
	approveCmd.Flags().StringVarP(&approveWorkspace, "workspace", "W", "", "Workspace to operate on")
	approveCmd.Flags().StringVar(&approveLead, "lead", "lead", "Lead working area")
	cli.RegisterCommand(approveCmd)
}

func runApprove(cmd *cobra.Command, args []string) error {
	number, err := strconv.Atoi(args[1])
	if err != nil || number < 1 {
		return fmt.Errorf("revision must be a positive number")
	}
	resolver, err := approveResolver()
	if err != nil {
		return err
	}
	if approveWorkspace != "" {
		if err := resolver.SetWorkspace(approveWorkspace); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	result, err := approveLocal(cmd.Context(), workspace.ID, approveLead, args[0], number,
		review.Actor{Kind: "human", ID: "local-user"})
	if err != nil {
		return err
	}
	if len(result.Pending) > 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Approved; waiting for the lead working area")
	} else {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Approved and added to the lead working area")
	}
	return err
}
