package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func dispatchTaskRuns(ctx context.Context, st store.Store, run *domain.WorkflowRun, input ParentWorkItemsInput, taskRuns []*domain.TaskRun) ([]*domain.TaskRun, int, error) {
	if len(taskRuns) == 0 || st == nil || st.Agents() == nil || st.AgentCommands() == nil {
		return taskRuns, 0, nil
	}
	out := make([]*domain.TaskRun, 0, len(taskRuns))
	dispatched := 0
	for _, taskRun := range taskRuns {
		updated, didDispatch, err := dispatchTaskRun(ctx, st, run, input, taskRun)
		if err != nil {
			return nil, dispatched, err
		}
		out = append(out, updated)
		if didDispatch {
			dispatched++
		}
	}
	return out, dispatched, nil
}

func dispatchTaskRun(ctx context.Context, st store.Store, run *domain.WorkflowRun, input ParentWorkItemsInput, taskRun *domain.TaskRun) (*domain.TaskRun, bool, error) {
	if run == nil || taskRun == nil {
		return taskRun, false, nil
	}
	if taskRun.CommandID != "" || taskRun.Status != domain.TaskRunQueued {
		return taskRun, false, nil
	}
	agentName := taskWorkerName(input.ParentID, taskRun.WorkItemID)
	agent, err := createOrLoadTaskWorker(ctx, st, run.WorkspaceKey, input, agentName)
	if err != nil {
		return nil, false, err
	}
	cmd, err := st.AgentCommands().Create(ctx, store.AgentCommandCreate{
		WorkspaceKey:  run.WorkspaceKey,
		TargetAgentID: agent.Name,
		Type:          "start",
		Payload: map[string]string{
			"task_id":         taskRun.WorkItemID,
			"parent_id":       input.ParentID,
			"workflow_name":   run.WorkflowName,
			"workflow_run_id": run.RunID,
			"task_run_id":     taskRun.TaskRunID,
		},
	})
	if err != nil {
		return nil, false, fmt.Errorf("enqueue start command for task run %s: %w", taskRun.TaskRunID, err)
	}
	status := domain.TaskRunStarting
	agentID := agent.Name
	claimActor := agent.Name
	commandID := cmd.CommandID
	updated, err := st.TaskRuns().Update(ctx, run.WorkspaceKey, taskRun.TaskRunID, store.TaskRunUpdate{
		Status:     &status,
		AgentID:    &agentID,
		ClaimActor: &claimActor,
		CommandID:  &commandID,
	})
	if err != nil {
		return nil, false, fmt.Errorf("mark task run %s dispatched: %w", taskRun.TaskRunID, err)
	}
	_, _ = st.RunEvents().Append(ctx, store.RunEventAppend{
		WorkspaceKey:  run.WorkspaceKey,
		WorkflowRunID: run.RunID,
		TaskRunID:     taskRun.TaskRunID,
		Type:          "task_run_dispatched",
		Message:       "derived daemon start command from task run",
		Data:          mustJSON(map[string]string{"agent_id": agent.Name, "command_id": cmd.CommandID, "work_item_id": taskRun.WorkItemID}),
	})
	return updated, true, nil
}

func createOrLoadTaskWorker(ctx context.Context, st store.Store, workspace string, input ParentWorkItemsInput, name string) (*domain.Agent, error) {
	mode := domain.AgentModeEphemeral
	desired := domain.AgentDesiredStopped
	agent, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:   workspace,
		Name:           name,
		RoleName:       input.Role,
		Auto:           true,
		Parent:         input.ParentID,
		Mode:           mode,
		MaxConcurrency: 1,
		DesiredState:   desired,
	})
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, domain.ErrAlreadyExists) {
		return nil, fmt.Errorf("create task worker %s: %w", name, err)
	}
	agent, err = st.Agents().Get(ctx, workspace, name)
	if err != nil {
		return nil, fmt.Errorf("get existing task worker %s: %w", name, err)
	}
	if agent.Mode != domain.AgentModeEphemeral || agent.Parent != input.ParentID || agent.RoleName != input.Role {
		return nil, fmt.Errorf("task worker name %s already exists for role %q parent %q mode %q", name, agent.RoleName, agent.Parent, agent.Mode)
	}
	return agent, nil
}

func taskWorkerName(parentID, taskID string) string {
	hashBytes := sha256.Sum256([]byte(taskID))
	hash := hex.EncodeToString(hashBytes[:])[:8]
	base := sanitizeWorkerName(parentID + "-" + taskID)
	const maxNameLen = 63
	suffix := "-" + hash
	if len(base)+len(suffix) > maxNameLen {
		base = strings.TrimRight(base[:maxNameLen-len(suffix)], "-")
	}
	if base == "" {
		base = "task"
	}
	return base + suffix
}

func sanitizeWorkerName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
