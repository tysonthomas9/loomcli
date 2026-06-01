package defs

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/store"
)

func applyRuntimeStateRecords(ctx context.Context, st store.Store, workspaceKey string, plan *Plan) error {
	if err := applyNodes(ctx, st, workspaceKey, plan.Nodes); err != nil {
		return err
	}
	if err := applyWorkflowRuns(ctx, st, workspaceKey, plan.WorkflowRuns); err != nil {
		return err
	}
	if err := applyTaskRuns(ctx, st, workspaceKey, plan.TaskRuns); err != nil {
		return err
	}
	if err := applyRunEvents(ctx, st, workspaceKey, plan.RunEvents); err != nil {
		return err
	}
	if err := applyAgentSessions(ctx, st, workspaceKey, plan.AgentSessions); err != nil {
		return err
	}
	if err := applyAgentSessionOperations(ctx, st, workspaceKey, plan.AgentSessionOperations); err != nil {
		return err
	}
	if err := applyAgentSessionToolCalls(ctx, st, workspaceKey, plan.AgentSessionToolCalls); err != nil {
		return err
	}
	if err := applyAgentLeases(ctx, st, workspaceKey, plan.AgentLeases); err != nil {
		return err
	}
	if err := applyAgentOwnershipLeases(ctx, st, workspaceKey, plan.AgentOwnershipLeases); err != nil {
		return err
	}
	if err := applyAgentCommands(ctx, st, workspaceKey, plan.AgentCommands); err != nil {
		return err
	}
	if err := applyTerminalSessions(ctx, st, workspaceKey, plan.TerminalSessions); err != nil {
		return err
	}
	return applyArtifacts(ctx, st, workspaceKey, plan.Artifacts)
}
