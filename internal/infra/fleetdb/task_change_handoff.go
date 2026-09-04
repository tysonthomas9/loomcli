package fleetdb

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var _ store.TaskChangeHandoffStore = (*Client)(nil)

func (c *Client) PutTaskBranch(ctx context.Context, branch domain.TaskBranch) (*domain.TaskBranch, error) {
	body := map[string]any{
		"branch_name": branch.BranchName, "admitted_base_sha": branch.AdmittedBaseSHA,
		"expected_remote_head_sha":  branch.ExpectedRemoteHeadSHA,
		"confirmed_remote_head_sha": branch.ConfirmedRemoteHeadSHA,
	}
	var out domain.TaskBranch
	path := "/api/v1/" + pathEscape(branch.WorkspaceKey) + "/tasks/" + pathEscape(branch.TaskID) + "/branches/" + pathEscape(branch.RepoName)
	if err := c.do(ctx, "PUT", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetTaskBranch(ctx context.Context, workspaceKey, taskID, repoName string) (*domain.TaskBranch, error) {
	var out domain.TaskBranch
	path := "/api/v1/" + pathEscape(workspaceKey) + "/tasks/" + pathEscape(taskID) + "/branches/" + pathEscape(repoName)
	if err := c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateTaskChangeSet(ctx context.Context, changeSet domain.TaskChangeSet) (*domain.TaskChangeSet, error) {
	var out domain.TaskChangeSet
	path := "/api/v1/" + pathEscape(changeSet.WorkspaceKey) + "/tasks/" + pathEscape(changeSet.TaskID) + "/change-sets"
	if err := c.do(ctx, "POST", path, map[string]any{"version": changeSet.Version, "entries": changeSet.Entries}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetTaskChangeSet(ctx context.Context, workspaceKey, taskID string, version int) (*domain.TaskChangeSet, error) {
	if version < 1 {
		return nil, fmt.Errorf("change set version must be positive: %w", domain.ErrInvalid)
	}
	var out domain.TaskChangeSet
	path := fmt.Sprintf("/api/v1/%s/tasks/%s/change-sets/%d", pathEscape(workspaceKey), pathEscape(taskID), version)
	if err := c.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
