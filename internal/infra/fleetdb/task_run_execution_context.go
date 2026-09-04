package fleetdb

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var _ store.TaskRunExecutionContextStore = (*Client)(nil)

func (c *Client) UpdateTaskRunExecutionContext(ctx context.Context, workspaceKey, taskRunID string, update store.TaskRunExecutionContextUpdate) (*domain.TaskRun, error) {
	var out domain.TaskRun
	path := "/api/v1/" + pathEscape(workspaceKey) + "/task-runs/" + pathEscape(taskRunID) + "/execution-context"
	body := map[string]any{
		"root_state": update.RootState, "root_node_id": update.RootNodeID,
		"root_fencing_token": update.RootFencingToken, "backend_kind": update.BackendKind,
		"backend_session_ref": update.BackendSessionRef,
	}
	if err := c.do(ctx, "PATCH", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
