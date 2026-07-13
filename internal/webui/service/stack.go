package service

import "context"

// StackService defines read-only stack lineage operations for the web UI.
type StackService interface {
	// ListStacks returns stack lineage visible to a workspace.
	ListStacks(ctx context.Context, wsID string) (*WorkspaceStacksResult, error)
}

// WorkspaceStacksResult is the response data for GET /stacks.
type WorkspaceStacksResult struct {
	Stacks []WorkspaceStack `json:"stacks"`
}

// WorkspaceStack describes one task stack in a workspace repo.
type WorkspaceStack struct {
	ID       string               `json:"id"`
	Repo     string               `json:"repo"`
	RootBase string               `json:"root_base"`
	Nodes    []WorkspaceStackNode `json:"nodes"`
}

// WorkspaceStackNode describes one task lineage node plus its resolved base ref.
type WorkspaceStackNode struct {
	TaskID       string `json:"task_id"`
	BaseTaskID   string `json:"base_task_id,omitempty"`
	OutputBranch string `json:"output_branch"`
	BaseRef      string `json:"base_ref,omitempty"`
	Position     int    `json:"position"`
}
