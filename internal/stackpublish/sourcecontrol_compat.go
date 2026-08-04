package stackpublish

import (
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// These projections are the temporary compatibility edge while the pure
// stack planning types move into Source Control. Persistence and mutations do
// not cross this adapter.
func legacyStack(value sourcecontrol.Stack) stacklineage.Stack {
	return stacklineage.Stack{
		ID: stacklineage.StackID(value.ID), WorkspaceKey: value.WorkspaceKey,
		RepoName: value.Repository, RootBase: value.RootBase,
		DefaultCommitMode: stacklineage.CommitMode(value.DefaultCommitMode),
		CreatedAt:         value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func legacyStackNodes(values []sourcecontrol.StackNode) []stacklineage.Node {
	result := make([]stacklineage.Node, len(values))
	for index, value := range values {
		result[index] = stacklineage.Node{
			StackID: stacklineage.StackID(value.StackID), TaskID: value.TaskID, BaseTaskID: value.BaseTaskID,
			OutputBranch: value.OutputBranch, CommitMode: stacklineage.CommitMode(value.CommitMode),
			State: stacklineage.NodeState(value.State), PRNumber: value.PRNumber, PRURL: value.PRURL,
			OutputSHA: value.OutputSHA, LastPublishedAt: value.LastPublishedAt,
			CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		}
	}
	return result
}
