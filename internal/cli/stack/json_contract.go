package stack

import (
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// stackJSON and stackNodeJSON are the CLI's stable JSON contract. Source
// Control's owner models remain transport-neutral and are mapped explicitly at
// this delivery boundary.
type stackJSON struct {
	ID                sourcecontrol.StackID    `json:"id"`
	WorkspaceKey      string                   `json:"workspaceKey"`
	Repository        string                   `json:"repoName"`
	RootBase          string                   `json:"rootBase"`
	DefaultCommitMode sourcecontrol.CommitMode `json:"defaultCommitMode,omitempty"`
	CreatedAt         time.Time                `json:"createdAt"`
	UpdatedAt         time.Time                `json:"updatedAt"`
}

type stackNodeJSON struct {
	StackID         sourcecontrol.StackID    `json:"stackId"`
	TaskID          string                   `json:"taskId"`
	BaseTaskID      string                   `json:"baseTaskId,omitempty"`
	OutputBranch    string                   `json:"outputBranch"`
	CommitMode      sourcecontrol.CommitMode `json:"commitMode,omitempty"`
	State           sourcecontrol.NodeState  `json:"state"`
	PRNumber        int                      `json:"prNumber,omitempty"`
	PRURL           string                   `json:"prUrl,omitempty"`
	OutputSHA       string                   `json:"outputSha,omitempty"`
	LastPublishedAt *time.Time               `json:"lastPublishedAt,omitempty"`
	CreatedAt       time.Time                `json:"createdAt"`
	UpdatedAt       time.Time                `json:"updatedAt"`
}

func stackForJSON(value sourcecontrol.Stack) stackJSON {
	return stackJSON{
		ID: value.ID, WorkspaceKey: value.WorkspaceKey, Repository: value.Repository,
		RootBase: value.RootBase, DefaultCommitMode: value.DefaultCommitMode,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func stacksForJSON(values []sourcecontrol.Stack) []stackJSON {
	result := make([]stackJSON, len(values))
	for index, value := range values {
		result[index] = stackForJSON(value)
	}
	return result
}

func stackNodeForJSON(value sourcecontrol.StackNode) stackNodeJSON {
	return stackNodeJSON{
		StackID: value.StackID, TaskID: value.TaskID, BaseTaskID: value.BaseTaskID,
		OutputBranch: value.OutputBranch, CommitMode: value.CommitMode, State: value.State,
		PRNumber: value.PRNumber, PRURL: value.PRURL, OutputSHA: value.OutputSHA,
		LastPublishedAt: value.LastPublishedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func stackNodesForJSON(values []sourcecontrol.StackNode) []stackNodeJSON {
	result := make([]stackNodeJSON, len(values))
	for index, value := range values {
		result[index] = stackNodeForJSON(value)
	}
	return result
}
