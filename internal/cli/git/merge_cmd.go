package git

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

var mergeWorkspace string
var mergeResolver = cli.NewResolver
var queueMergeLocal = publish.QueueMergeUpToLocal

var mergeCmd = &cobra.Command{
	Use:   "merge <task>",
	Short: "Lead: queue a merge of a task's PR and the approved PRs below it",
	Long: `Queue "merge up to here" for the task's PR: it and every approved PR below
it in its stack merge bottom up, each once its checks and reviews pass. The
request joins the same server queue as the Merge up to here button on the
Pull Requests page.

Only the lead runs this. With Lead may merge on, the queued merge runs; with
it off, Loom refuses with "Lead may merge is off" and queues nothing. A human
merges from the Pull Requests page.`,
	GroupID: "git",
	Args:    cobra.ExactArgs(1),
	RunE:    runMerge,
}

func init() {
	mergeCmd.Flags().StringVarP(&mergeWorkspace, "workspace", "W", "", "Workspace to operate on")
	cli.RegisterCommand(mergeCmd)
}

func runMerge(cmd *cobra.Command, args []string) error {
	// D42: the lead is whoever runs this from the lead's agent session.
	actor := resolveCommandActor("lead")
	if actor.Kind != "lead" {
		return fmt.Errorf("loom merge is the lead's command; a human merges with Merge up to here on the Pull Requests page")
	}
	resolver, err := resolverFor(mergeWorkspace, mergeResolver)
	if err != nil {
		return err
	}
	if mergeWorkspace != "" {
		if err := resolver.SetWorkspace(mergeWorkspace); err != nil {
			return err
		}
	}
	workspace := resolver.Config.Workspaces[resolver.WorkspaceName()]
	changes, err := mergeTaskChanges(cmd, workspace.ID, args[0])
	if err != nil {
		return err
	}
	for _, change := range changes {
		view, err := queueMergeLocal(cmd.Context(), workspace.ID, change, publish.MergeActor{Kind: "lead", ID: actor.ID})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "merge queued: stack %s up to %s (%s)\n", view.StackID, change, mergePhase(view)); err != nil {
			return err
		}
		for _, layer := range view.Layers {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %s %s %s\n", layer.Change, layer.State, layer.PRURL); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeTaskChanges names the change of each repo the task has code in; an
// argument that is not a task is taken as a change.
func mergeTaskChanges(cmd *cobra.Command, workspace, task string) ([]string, error) {
	revisions, err := verdictTaskRevisions(cmd.Context(), workspace, task)
	if err != nil && !review.IsNotFound(err) {
		return nil, err
	}
	var changes []string
	seen := map[string]bool{}
	for _, revision := range revisions {
		if !seen[revision.ChangeID] {
			seen[revision.ChangeID] = true
			changes = append(changes, revision.ChangeID)
		}
	}
	if len(changes) == 0 {
		changes = []string{task}
	}
	return changes, nil
}

func mergePhase(view publish.MergeStackView) string {
	if view.Reason != "" {
		return view.Phase + ": " + view.Reason
	}
	return view.Phase
}
