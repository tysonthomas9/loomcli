package git

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

var approveWorkspace string
var approveLead string
var approveOnly bool
var approveResolver = cli.NewResolver
var approveLocal = func(ctx context.Context, workspace, lead, change string, revision int, actor review.Actor) (apply.FollowResult, error) {
	return apply.ApproveLocalPublishing(ctx, workspace, lead, change, revision, actor, !approveOnly)
}
var publishApprovedLocal = publish.PublishApproved

var approveCmd = &cobra.Command{
	Use:   "approve <change> <revision>",
	Short: "Approve a revision, follow it in a lead working area and open its PR",
	Long: `Approve a revision and apply it as the new top layer of the lead's working area.
Once applied, its PR opens straight away: the next PR of the stack (Stacked PRs),
or its own PR to trunk (PR per task). --only applies it without opening a PR.`,
	GroupID: "git",
	Args:    cobra.ExactArgs(2),
	RunE:    runApprove,
}

func init() {
	approveCmd.Flags().StringVarP(&approveWorkspace, "workspace", "W", "", "Workspace to operate on")
	approveCmd.Flags().StringVar(&approveLead, "lead", "lead", "Lead working area")
	approveCmd.Flags().BoolVar(&approveOnly, "only", false, "Approve only: apply without opening a PR")
	cli.RegisterCommand(approveCmd)
}

func runApprove(cmd *cobra.Command, args []string) error {
	number, err := strconv.Atoi(args[1])
	if err != nil || number < 1 {
		return fmt.Errorf("revision must be a positive number")
	}
	resolver, err := resolverFor(approveWorkspace, approveResolver)
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
		return err
	}
	if approveOnly {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Approved and added to the lead working area")
		return err
	}
	outcomes, publishErr := publishApprovedLocal(cmd.Context(), workspace.ID, approveLead, stackstore.Declared())
	for _, outcome := range outcomes {
		if outcome.Change != args[0] {
			continue
		}
		switch outcome.Status {
		case "published":
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Approved and opened PR #%d %s\n", outcome.PRNumber, outcome.PRURL)
			return err
		case "pending":
			return fmt.Errorf("approved and added to the lead working area; PR not opened yet, Loom retries: %s", outcome.Reason)
		default:
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Approved and added to the lead working area; %s\n", outcome.Reason)
			return err
		}
	}
	if publishErr != nil {
		return fmt.Errorf("approved and added to the lead working area; PR not opened yet: %w", publishErr)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "Approved and added to the lead working area")
	return err
}
