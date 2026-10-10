package git

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

var rejectWorkspace string
var rejectLead string
var rejectReason string
var rejectChange string
var rejectRevision int
var rejectResolver = cli.NewResolver
var rejectLocal = func(ctx context.Context, workspace, lead, change string, revision int, headSHA, reason string, actor review.Actor) error {
	store, err := review.OpenLocal()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	_, err = store.SubmitForLead(ctx, workspace, change, revision, headSHA, "reject", reason, actor, lead)
	return err
}

var rejectCmd = &cobra.Command{
	Use:   "reject <task> | reject <change> <revision>",
	Short: "Reject a task's current code so it goes back for another attempt",
	Long: `Reject the code a task is in review with: its newest revision in each repo.
Loom prints exactly what it rejects and pins that code; if a new attempt
arrives in between, the rejection is refused as stale and nothing is recorded.
<change> <revision> (or --change and --revision) rejects one revision.

The rejection records whoever runs it (D42): a normal shell is a human, the
lead's session is the lead and a task agent is an agent.`,
	GroupID: "git",
	Args:    cobra.RangeArgs(0, 2),
	RunE:    runReject,
}

func init() {
	rejectCmd.Flags().StringVarP(&rejectWorkspace, "workspace", "W", "", "Workspace to operate on")
	rejectCmd.Flags().StringVar(&rejectLead, "lead", "lead", "Lead working area (default: the lead running the command, else lead)")
	rejectCmd.Flags().StringVar(&rejectReason, "reason", "", "What to fix")
	rejectCmd.Flags().StringVar(&rejectChange, "change", "", "Reject this change (advanced; needs --revision)")
	rejectCmd.Flags().IntVar(&rejectRevision, "revision", 0, "Revision of --change to reject (advanced)")
	cli.RegisterCommand(rejectCmd)
}

func runReject(cmd *cobra.Command, args []string) error {
	resolver, err := resolverFor(rejectWorkspace, rejectResolver)
	if err != nil {
		return err
	}
	if rejectWorkspace != "" {
		if err := resolver.SetWorkspace(rejectWorkspace); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	actor := resolveCommandActor(rejectLead)
	lead := verdictLead(cmd, rejectLead, actor)
	targets, _, err := resolveVerdictTargets(cmd.Context(), workspace.ID, args, rejectChange, rejectRevision)
	if err != nil {
		return err
	}
	if err := printVerdictPlan(cmd.OutOrStdout(), "Rejecting", actor, lead, targets); err != nil {
		return err
	}
	for _, target := range targets {
		if err := rejectLocal(cmd.Context(), workspace.ID, lead, target.Change, target.Number, target.HeadSHA, rejectReason, actor.Actor); err != nil {
			return staleVerdictError(target, err)
		}
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), "Rejected")
	return err
}
