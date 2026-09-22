package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/taskdelivery"
)

// managesTaskDelivery reports whether the runner implements Loom's delivery
// plan/receipt protocol. Custom runners keep their existing result contract;
// they can opt in once the protocol is part of their manifest contract.
func managesTaskDelivery(req TaskExecRequest) bool {
	switch strings.TrimSpace(req.RunnerEntrypoint) {
	case LocalTaskRunnerEntrypoint, DaytonaTaskRunnerEntrypoint:
		return true
	default:
		return false
	}
}

func enforceTaskDelivery(plan taskdelivery.Plan, result TaskExecResult, execErr error) TaskExecResult {
	if execErr != nil || result.Status != domain.TaskRunCompleted {
		return result
	}
	receipt, err := taskdelivery.AcceptRuntimeMetadata(plan, result.RuntimeMetadata)
	if err != nil {
		return taskDeliveryFailure(err)
	}
	encoded, err := taskdelivery.EncodeReceipt(receipt)
	if err != nil {
		return taskDeliveryFailure(err)
	}
	if result.RuntimeMetadata == nil {
		result.RuntimeMetadata = map[string]string{}
	}
	result.RuntimeMetadata["task_delivery_plan_id"] = plan.PlanID
	result.RuntimeMetadata["task_delivery_requirement"] = string(plan.Requirement)
	result.RuntimeMetadata["task_delivery_policy_source"] = string(plan.PolicySource)
	result.RuntimeMetadata["task_delivery_receipt"] = encoded
	return result
}

func (e HostBridgeTaskExecutor) resolveTaskDeliveryPlan(ctx context.Context, req TaskExecRequest, worktree TaskWorktree) (taskdelivery.Plan, error) {
	if e.Store == nil {
		return taskdelivery.Plan{}, nil
	}
	workspace, err := e.Store.Workspaces().Get(ctx, req.WorkspaceKey)
	if err != nil {
		return taskdelivery.Plan{}, fmt.Errorf("resolve task delivery workspace: %w", err)
	}
	repoName := strings.TrimSpace(worktree.RepoName)
	var repoRequirement domain.TaskDeliveryRequirement
	var repoErr error
	if repoName != "" {
		repo, repoErr := e.Store.Repos().Get(ctx, req.WorkspaceKey, repoName)
		if repoErr == nil {
			repoRequirement = repo.TaskDeliveryRequirement
		}
	}
	override, err := taskDeliveryOverride(req.Input)
	if err != nil {
		return taskdelivery.Plan{}, err
	}
	resolution, err := taskdelivery.Resolve(taskdelivery.ResolveInput{
		WorkspaceRequirement:  workspace.TaskDeliveryRequirement,
		RepositoryRequirement: repoRequirement,
		RunOverride:           override,
	})
	if err != nil {
		return taskdelivery.Plan{}, err
	}
	if resolution.Requirement == domain.TaskDeliveryPullRequest && repoErr != nil {
		return taskdelivery.Plan{}, fmt.Errorf("resolve task delivery repository %q: %w", repoName, repoErr)
	}
	return taskdelivery.Freeze(taskdelivery.FreezeInput{
		RunID:        req.TaskRunID,
		WorkspaceKey: req.WorkspaceKey,
		Repository:   repoName,
		Resolution:   resolution,
	})
}

func taskDeliveryOverride(input json.RawMessage) (domain.TaskDeliveryRequirement, error) {
	if len(input) == 0 || string(input) == "null" {
		return "", nil
	}
	var payload struct {
		Requirement domain.TaskDeliveryRequirement `json:"taskDeliveryRequirement"`
		OpenPR      bool                           `json:"openPullRequest"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return "", fmt.Errorf("decode task delivery override: %w", err)
	}
	if payload.OpenPR {
		return domain.TaskDeliveryPullRequest, nil
	}
	return payload.Requirement, nil
}

func taskDeliveryFailure(err error) TaskExecResult {
	return TaskExecResult{
		Status:       domain.TaskRunFailed,
		ExitCode:     1,
		ErrorClass:   "task_delivery_unsatisfied",
		ErrorMessage: err.Error(),
		RuntimeMetadata: map[string]string{
			"task_delivery_status": "rejected",
		},
	}
}
