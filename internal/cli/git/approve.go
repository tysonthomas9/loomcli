package git

import (
	"context"
	"fmt"

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
var approveChange string
var approveRevision int
var approveResolver = cli.NewResolver
var approveLocal = func(ctx context.Context, workspace, lead, change string, revision int, headSHA string, actor review.Actor) (apply.FollowResult, error) {
	return apply.ApproveLocalPinned(ctx, workspace, lead, change, revision, headSHA, actor, !approveOnly)
}
var publishApprovedLocal = publish.PublishApproved

var approveCmd = &cobra.Command{
	Use:   "approve <task> | approve <change> <revision>",
	Short: "Approve a task's current code, follow it in a lead working area and open its PR",
	Long: `Approve the code a task is in review with: its newest revision in each repo.
Loom prints exactly what it approves and pins that code; if a new attempt
arrives in between, the approval is refused as stale and nothing is recorded.
<change> <revision> (or --change and --revision) approves one revision.

The approval records whoever runs it (D42): a normal shell is a human, the
lead's session is the lead (refused while Lead may approve is off) and a task
agent is an agent (refused for its own task).

Once applied, its PR opens straight away: the next PR of the stack (Stacked PRs),
or its own PR to trunk (PR per task). --only applies it without opening a PR.`,
	GroupID: "git",
	Args:    cobra.RangeArgs(0, 2),
	RunE:    runApprove,
}

func init() {
	approveCmd.Flags().StringVarP(&approveWorkspace, "workspace", "W", "", "Workspace to operate on")
	approveCmd.Flags().StringVar(&approveLead, "lead", "lead", "Lead working area (default: the lead running the command, else lead)")
	approveCmd.Flags().BoolVar(&approveOnly, "only", false, "Approve only: apply without opening a PR")
	approveCmd.Flags().StringVar(&approveChange, "change", "", "Approve this change (advanced; needs --revision)")
	approveCmd.Flags().IntVar(&approveRevision, "revision", 0, "Revision of --change to approve (advanced)")
	cli.RegisterCommand(approveCmd)
}

// verdictLead is the lead working area a verdict targets: --lead when given,
// else the lead running the command, else the default lead.
func verdictLead(cmd *cobra.Command, flagLead string, actor commandActor) string {
	if cmd.Flags().Changed("lead") || actor.Kind != "lead" {
		return flagLead
	}
	return actor.ID
}

func runApprove(cmd *cobra.Command, args []string) error {
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
	actor := resolveCommandActor(approveLead)
	lead := verdictLead(cmd, approveLead, actor)
	targets, task, err := resolveVerdictTargets(cmd.Context(), workspace.ID, args, approveChange, approveRevision)
	if err != nil {
		return err
	}
	if err := refuseOwnTask("approve", actor, task); err != nil {
		return err
	}
	if err := printVerdictPlan(cmd.OutOrStdout(), "Approving", actor, lead, targets); err != nil {
		return err
	}
	for _, target := range targets {
		if err := approveTarget(cmd, workspace.ID, lead, target, actor.Actor); err != nil {
			return err
		}
	}
	return nil
}

func approveTarget(cmd *cobra.Command, workspace, lead string, target verdictTarget, actor review.Actor) error {
	result, err := approveLocal(cmd.Context(), workspace, lead, target.Change, target.Number, target.HeadSHA, actor)
	if err != nil {
		return staleVerdictError(target, err)
	}
	if len(result.Pending) > 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Approved; waiting for the lead working area")
		return err
	}
	if approveOnly {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Approved and added to the lead working area")
		return err
	}
	outcomes, publishErr := publishApprovedLocal(cmd.Context(), workspace, lead, stackstore.Declared())
	for _, outcome := range outcomes {
		if outcome.Change != target.Change {
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
