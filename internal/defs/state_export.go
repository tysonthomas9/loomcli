package defs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	runtimeStateExportSchema = "loom.runtime-state.v1"
	runtimeStateExportPath   = ".loom/state/workspace-runtime-state.json"
)

type RuntimeStateExport struct {
	Schema               string                      `json:"schema"`
	Root                 string                      `json:"root,omitempty"`
	AgentInstances       []AgentInstanceModule       `json:"agent_instances,omitempty"`
	Nodes                []NodeModule                `json:"nodes,omitempty"`
	AgentSessions        []AgentSessionModule        `json:"agent_sessions,omitempty"`
	AgentLeases          []AgentLeaseModule          `json:"agent_leases,omitempty"`
	AgentOwnershipLeases []AgentOwnershipLeaseModule `json:"agent_ownership_leases,omitempty"`
	AgentCommands        []AgentCommandModule        `json:"agent_commands,omitempty"`
	TerminalSessions     []TerminalSessionModule     `json:"terminal_sessions,omitempty"`
	Artifacts            []ArtifactModule            `json:"artifacts,omitempty"`
	WorkflowRuns         []WorkflowRunModule         `json:"workflow_runs,omitempty"`
	TaskRuns             []TaskRunModule             `json:"task_runs,omitempty"`
	RunEvents            []RunEventModule            `json:"run_events,omitempty"`
}

func ExportRuntimeStateFiles(plan *Plan) ([]SourceExportFile, error) {
	if plan == nil {
		return nil, fmt.Errorf("definition plan required")
	}
	state := runtimeStateFromPlan(plan)
	if runtimeStateEmpty(state) {
		return nil, nil
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode runtime state export: %w", err)
	}
	return []SourceExportFile{{
		Path:     runtimeStateExportPath,
		Contents: string(data) + "\n",
	}}, nil
}

func WriteRuntimeStateExport(root string, plan *Plan, force bool) ([]SourceExportFile, error) {
	files, err := ExportRuntimeStateFiles(plan)
	if err != nil {
		return nil, err
	}
	return writeExportFiles(root, files, force)
}

func mergeRuntimeStateExport(root string, plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("definition plan required")
	}
	path := filepath.Join(root, filepath.FromSlash(runtimeStateExportPath))
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read runtime state export: %w", err)
	}
	var state RuntimeStateExport
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode runtime state export %s: %w", path, err)
	}
	if schema := strings.TrimSpace(state.Schema); schema != "" && schema != runtimeStateExportSchema {
		return fmt.Errorf("decode runtime state export %s: unsupported schema %q", path, schema)
	}
	mergeRuntimeState(plan, state)
	return nil
}

func runtimeStateFromPlan(plan *Plan) RuntimeStateExport {
	return RuntimeStateExport{
		Schema:               runtimeStateExportSchema,
		Root:                 plan.Root,
		AgentInstances:       append([]AgentInstanceModule(nil), plan.AgentInstances...),
		Nodes:                append([]NodeModule(nil), plan.Nodes...),
		AgentSessions:        append([]AgentSessionModule(nil), plan.AgentSessions...),
		AgentLeases:          append([]AgentLeaseModule(nil), plan.AgentLeases...),
		AgentOwnershipLeases: append([]AgentOwnershipLeaseModule(nil), plan.AgentOwnershipLeases...),
		AgentCommands:        append([]AgentCommandModule(nil), plan.AgentCommands...),
		TerminalSessions:     append([]TerminalSessionModule(nil), plan.TerminalSessions...),
		Artifacts:            append([]ArtifactModule(nil), plan.Artifacts...),
		WorkflowRuns:         append([]WorkflowRunModule(nil), plan.WorkflowRuns...),
		TaskRuns:             append([]TaskRunModule(nil), plan.TaskRuns...),
		RunEvents:            append([]RunEventModule(nil), plan.RunEvents...),
	}
}

func runtimeStateEmpty(state RuntimeStateExport) bool {
	return len(state.AgentInstances) == 0 &&
		len(state.Nodes) == 0 &&
		len(state.AgentSessions) == 0 &&
		len(state.AgentLeases) == 0 &&
		len(state.AgentOwnershipLeases) == 0 &&
		len(state.AgentCommands) == 0 &&
		len(state.TerminalSessions) == 0 &&
		len(state.Artifacts) == 0 &&
		len(state.WorkflowRuns) == 0 &&
		len(state.TaskRuns) == 0 &&
		len(state.RunEvents) == 0
}

func mergeRuntimeState(plan *Plan, state RuntimeStateExport) {
	plan.AgentInstances = append(plan.AgentInstances, state.AgentInstances...)
	plan.Nodes = append(plan.Nodes, state.Nodes...)
	plan.AgentSessions = append(plan.AgentSessions, state.AgentSessions...)
	plan.AgentLeases = append(plan.AgentLeases, state.AgentLeases...)
	plan.AgentOwnershipLeases = append(plan.AgentOwnershipLeases, state.AgentOwnershipLeases...)
	plan.AgentCommands = append(plan.AgentCommands, state.AgentCommands...)
	plan.TerminalSessions = append(plan.TerminalSessions, state.TerminalSessions...)
	plan.Artifacts = append(plan.Artifacts, state.Artifacts...)
	plan.WorkflowRuns = append(plan.WorkflowRuns, state.WorkflowRuns...)
	plan.TaskRuns = append(plan.TaskRuns, state.TaskRuns...)
	plan.RunEvents = append(plan.RunEvents, state.RunEvents...)
}
