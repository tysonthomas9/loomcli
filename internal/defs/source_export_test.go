package defs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWriteSourceExportCodifiesWorkspaceDefinitions(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CODEGEN", Name: "Codify"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	maxConcurrency := 3
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey:   "CODEGEN",
		Name:           "triage",
		Description:    "Direct control-plane triage role.",
		Backend:        "codex",
		Model:          "openai/gpt-5",
		Skills:         []string{"triage"},
		AllowedTools:   []string{"github_issue_read", "github.issue.read"},
		MaxConcurrency: &maxConcurrency,
		ReadOnly:       true,
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	runtime := RuntimeModule{
		Name:       "remote-e2b",
		Version:    "runtime-v1",
		SourcePath: "control-plane:runtime/remote-e2b",
		SourceHash: "runtime-hash",
		Provider:   domain.RuntimeProviderE2B,
		Image:      "node:22",
		CWD:        ".",
		Repos:      []string{"app"},
		Env:        []string{"NODE_ENV"},
		Workspace: &RuntimeWorkspace{
			ProviderWorkspaceID: "e2b-workspace-1",
			Owner:               "loom",
			Cleanup:             &RuntimeCleanupPolicy{Mode: "after_ttl", TTL: "24h"},
			Filesystem:          &RuntimeFilesystemSpec{Persistence: "session", Durability: "provider", Retention: "2d"},
		},
	}
	if _, err := st.RuntimeProfiles().Upsert(ctx, store.RuntimeProfileUpsert{
		WorkspaceKey: "CODEGEN",
		Name:         runtime.Name,
		Version:      runtime.Version,
		Provider:     runtime.Provider,
		Image:        runtime.Image,
		Repos:        runtime.Repos,
		Env:          runtime.Env,
		Manifest:     mustJSON(runtime),
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert runtime profile: %v", err)
	}
	workflowManifest := mustJSON(WorkflowModule{
		Name:               "slack-runner",
		Version:            "workflow-v1",
		SourcePath:         "control-plane:workflow/slack-runner",
		SourceHash:         "workflow-hash",
		Description:        "Run Slack child tasks.",
		RuntimeProfileName: "remote-e2b",
		Builtin:            "run-parent-work-items",
		SingletonPolicy:    "parent:${parentId}",
		Tools:              []string{"github_issue_read", "taskRuns.ensure"},
		Repos:              []string{"app"},
		Env:                []string{"NODE_ENV"},
	})
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey:       "CODEGEN",
		Name:               "slack-runner",
		Version:            "workflow-v1",
		Description:        "Run Slack child tasks.",
		RuntimeProfileName: "remote-e2b",
		Manifest:           workflowManifest,
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert workflow definition: %v", err)
	}
	if _, err := st.RouteBindings().Upsert(ctx, store.RouteBindingUpsert{
		WorkspaceKey:   "CODEGEN",
		DefinitionName: "slack-runner",
		DefinitionType: domain.DefinitionTypeWorkflow,
		Path:           "/workflows/slack-runner/run",
		Method:         "POST",
		AuthPolicy:     "workspace",
		Status:         domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert route binding: %v", err)
	}
	if _, err := st.TriggerBindings().Upsert(ctx, store.TriggerBindingUpsert{
		WorkspaceKey: "CODEGEN",
		WorkflowName: "slack-runner",
		EventType:    "issue.label_added",
		Filter:       json.RawMessage(`{"label":"slack","type":"epic"}`),
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert trigger binding: %v", err)
	}
	tool := ToolModule{
		Name:        "github_issue_read",
		Description: "Read one GitHub issue.",
		Version:     "tool-v1",
		SourcePath:  "control-plane:tool/github_issue_read",
		SourceHash:  "tool-hash",
		Parameters:  map[string]any{"type": "object", "required": []string{"number"}},
		Handler:     "workflow",
		Runtime:     "remote-e2b",
		Env:         []string{"GITHUB_TOKEN"},
		ReadOnly:    true,
	}
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       "CODEGEN",
		DefinitionType:     domain.DefinitionTypeTool,
		DefinitionName:     tool.Name,
		Version:            tool.Version,
		SourceHash:         tool.SourceHash,
		Manifest:           mustJSON(tool),
		CapabilityManifest: toolCapabilityManifest(tool),
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("apply tool definition: %v", err)
	}
	skill := SkillModule{
		Name:         "triage",
		Description:  "Triage incoming work.",
		Version:      "skill-v1",
		SourcePath:   "control-plane:skill/triage",
		SourceHash:   "skill-hash",
		Instructions: "Review priority, labels, and blocked state.",
	}
	if _, err := st.DefinitionVersions().Apply(ctx, store.DefinitionVersionApply{
		WorkspaceKey:       "CODEGEN",
		DefinitionType:     domain.DefinitionTypeSkill,
		DefinitionName:     skill.Name,
		Version:            skill.Version,
		SourceHash:         skill.SourceHash,
		Manifest:           mustJSON(skill),
		CapabilityManifest: skillCapabilityManifest(skill),
		Status:             domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("apply skill definition: %v", err)
	}

	workspacePlan, err := PlanFromWorkspace(ctx, st, "CODEGEN")
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	exportRoot := t.TempDir()
	files, err := WriteSourceExport(exportRoot, workspacePlan, false)
	if err != nil {
		t.Fatalf("WriteSourceExport() error = %v", err)
	}
	paths := sourceExportPaths(files)
	for _, want := range []string{
		".loom/agents/triage.ts",
		".loom/runtimes/remote-e2b.ts",
		".loom/skills/triage/SKILL.md",
		".loom/tools/github_issue_read.ts",
		".loom/workflows/slack-runner.ts",
	} {
		if !containsString(paths, want) {
			t.Fatalf("exported paths = %+v, missing %s", paths, want)
		}
	}

	exportedPlan, err := Load(exportRoot)
	if err != nil {
		t.Fatalf("Load(exportRoot) error = %v", err)
	}
	if got := Summary(exportedPlan); got != "agents=1 workflows=1 runtimes=1 skills=1 tools=1" {
		t.Fatalf("Summary(exportedPlan) = %q, want source-codified definitions", got)
	}
	agent := exportedPlan.Agents[0]
	if agent.Name != "triage" || agent.Model != "openai/gpt-5" || agent.Backend != "codex" ||
		!containsString(agent.Tools, "github_issue_read") || !containsString(agent.Tools, "github.issue.read") ||
		!containsString(agent.Skills, "triage") ||
		agent.MaxConcurrency != 3 || !agent.ReadOnly {
		t.Fatalf("exported agent = %+v, want control-plane role codified as source", agent)
	}
	workflow := exportedPlan.Workflows[0]
	if workflow.Name != "slack-runner" || workflow.RuntimeProfileName != "remote-e2b" ||
		workflow.Builtin != "run-parent-work-items" ||
		workflow.RoutePath != "/workflows/slack-runner/run" ||
		workflow.RouteAuth != "workspace" ||
		workflow.TriggerEvent != "issue.label_added" ||
		workflow.TriggerFilter["label"] != "slack" ||
		!containsString(workflow.Tools, "github_issue_read") {
		t.Fatalf("exported workflow = %+v, want route/trigger/tool source codification", workflow)
	}
	exportedRuntime := exportedPlan.Runtimes[0]
	if exportedRuntime.Name != "remote-e2b" || exportedRuntime.Provider != domain.RuntimeProviderE2B ||
		exportedRuntime.Workspace == nil ||
		exportedRuntime.Workspace.ProviderWorkspaceID != "e2b-workspace-1" ||
		exportedRuntime.Workspace.Filesystem == nil ||
		exportedRuntime.Workspace.Filesystem.Durability != "provider" {
		t.Fatalf("exported runtime = %+v, want runtime workspace policy codified", exportedRuntime)
	}
	if exportedPlan.Tools[0].Name != "github_issue_read" || exportedPlan.Tools[0].Handler != "workflow" ||
		exportedPlan.Tools[0].Runtime != "remote-e2b" || !exportedPlan.Tools[0].ReadOnly {
		t.Fatalf("exported tools = %+v, want typed tool source codification", exportedPlan.Tools)
	}
	if exportedPlan.Skills[0].Name != "triage" || !strings.Contains(exportedPlan.Skills[0].Instructions, "Review priority") {
		t.Fatalf("exported skills = %+v, want skill source codification", exportedPlan.Skills)
	}

	imported := memstore.New()
	if _, err := imported.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "REPLAY", Name: "Replay"}); err != nil {
		t.Fatalf("create replay workspace: %v", err)
	}
	if err := Apply(ctx, imported, "REPLAY", "test", exportedPlan); err != nil {
		t.Fatalf("Apply(exportedPlan) error = %v", err)
	}
	importedRoutes, err := imported.RouteBindings().List(ctx, "REPLAY", store.RouteBindingFilter{
		DefinitionName: "slack-runner",
		Status:         domain.DefinitionStatusActive,
	})
	if err != nil {
		t.Fatalf("list imported route bindings: %v", err)
	}
	if len(importedRoutes) != 1 || importedRoutes[0].Path != "/workflows/slack-runner/run" ||
		importedRoutes[0].Method != "POST" || importedRoutes[0].AuthPolicy != "workspace" {
		t.Fatalf("imported routes = %+v, want source replay to recreate durable route binding", importedRoutes)
	}
	importedTriggers, err := imported.TriggerBindings().List(ctx, "REPLAY", store.TriggerBindingFilter{
		WorkflowName: "slack-runner",
		Status:       domain.DefinitionStatusActive,
	})
	if err != nil {
		t.Fatalf("list imported trigger bindings: %v", err)
	}
	if len(importedTriggers) != 1 || importedTriggers[0].EventType != "issue.label_added" ||
		!strings.Contains(string(importedTriggers[0].Filter), `"label":"slack"`) {
		t.Fatalf("imported triggers = %+v, want source replay to recreate durable trigger binding", importedTriggers)
	}
	replayedPlan, err := PlanFromWorkspace(ctx, imported, "REPLAY")
	if err != nil {
		t.Fatalf("PlanFromWorkspace(replay) error = %v", err)
	}
	if len(replayedPlan.Workflows) != 1 ||
		replayedPlan.Workflows[0].RoutePath != "/workflows/slack-runner/run" ||
		replayedPlan.Workflows[0].TriggerEvent != "issue.label_added" ||
		replayedPlan.Workflows[0].TriggerFilter["type"] != "epic" {
		t.Fatalf("replayed workflow plan = %+v, want imported source to plan back with route/trigger semantics", replayedPlan.Workflows)
	}

	agentPath := filepath.Join(exportRoot, ".loom", "agents", "triage.ts")
	if err := os.WriteFile(agentPath, []byte("// user edit\n"), 0o644); err != nil {
		t.Fatalf("mutate exported agent: %v", err)
	}
	if _, err := WriteSourceExport(exportRoot, workspacePlan, false); err == nil ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("WriteSourceExport() error = %v, want non-force collision", err)
	}
	if _, err := WriteSourceExport(exportRoot, workspacePlan, true); err != nil {
		t.Fatalf("WriteSourceExport(force) error = %v", err)
	}
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read force-overwritten agent: %v", err)
	}
	if !strings.Contains(string(data), "createAgent") || strings.Contains(string(data), "user edit") {
		t.Fatalf("force-overwritten agent source = %s, want generated source", data)
	}
	if !strings.Contains(string(data), "from '@loom/sdk'") {
		t.Fatalf("force-overwritten agent source = %s, want SDK import path", data)
	}
	runtimeData, err := os.ReadFile(filepath.Join(exportRoot, ".loom", "runtimes", "remote-e2b.ts"))
	if err != nil {
		t.Fatalf("read generated runtime: %v", err)
	}
	if !strings.Contains(string(runtimeData), "defineRuntimeProfile") ||
		!strings.Contains(string(runtimeData), "from '@loom/sdk'") {
		t.Fatalf("generated runtime source = %s, want SDK defineRuntimeProfile export", runtimeData)
	}
}

func TestExportSourceFilesRejectsSanitizedPathCollisions(t *testing.T) {
	_, err := ExportSourceFiles(&Plan{
		Agents: []AgentModule{
			{Name: "review/bot"},
			{Name: "review bot"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), ".loom/agents/review-bot.ts") {
		t.Fatalf("ExportSourceFiles() error = %v, want sanitized path collision", err)
	}
}

func TestWriteRuntimeStateExportCodifiesMutableRuntimeRecords(t *testing.T) {
	now := time.Date(2026, 5, 30, 16, 0, 0, 0, time.UTC)
	finished := now.Add(3 * time.Minute)
	exitCode := 0
	plan := &Plan{
		Root: "workspace:STATE",
		AgentInstances: []AgentInstanceModule{{
			Name:         "worker",
			RoleName:     "task",
			SourcePath:   "control-plane:agent/worker",
			SourceHash:   "agent-instance-hash",
			Version:      "agent-instance-v1",
			State:        domain.AgentStateActive,
			DesiredState: domain.AgentDesiredRunning,
		}},
		Nodes: []NodeModule{{
			NodeID:          "node-1",
			SourcePath:      "control-plane:node/node-1",
			OwnerActor:      "daemon",
			RuntimeProvider: domain.RuntimeProviderLocal,
			Labels:          []string{"local"},
			Capabilities:    []string{"task-run"},
			Version:         "node-v1",
			LastHeartbeat:   &now,
		}},
		AgentSessions: []AgentSessionModule{{
			SessionID:     "session-1",
			AgentID:       "worker",
			SourcePath:    "control-plane:agent-session/session-1",
			SourceHash:    "session-hash",
			Version:       "session-v1",
			NodeID:        "node-1",
			Kind:          domain.AgentSessionKindTask,
			Status:        domain.AgentSessionRunning,
			LastHeartbeat: &now,
			FinishedAt:    &finished,
			ExitCode:      &exitCode,
			Metadata:      map[string]string{"workflow_run_id": "wrun-1"},
		}},
		AgentSessionOperations: []AgentSessionOperationModule{{
			OperationID:   "op-1",
			SessionID:     "session-1",
			AgentID:       "worker",
			SourcePath:    "control-plane:agent-session-operation/op-1",
			SourceHash:    "operation-hash",
			Version:       "operation-v1",
			WorkflowRunID: "wrun-1",
			TaskRunID:     "trun-1",
			TaskID:        "TASK-1",
			Kind:          "prompt",
			Status:        domain.AgentSessionOperationCompleted,
			Result:        json.RawMessage(`{"summary":"done"}`),
			Usage:         json.RawMessage(`{"totalTokens":12}`),
			StartedAt:     &now,
			CompletedAt:   &finished,
			DurationMS:    180000,
			Metadata:      map[string]string{"visibility": "audit"},
		}},
		AgentSessionToolCalls: []AgentSessionToolCallModule{{
			CallID:              "call-1",
			OperationID:         "op-1",
			SessionID:           "session-1",
			AgentID:             "worker",
			SourcePath:          "control-plane:agent-session-tool-call/call-1",
			SourceHash:          "tool-call-hash",
			Version:             "tool-call-v1",
			WorkflowRunID:       "wrun-1",
			TaskRunID:           "trun-1",
			TaskID:              "TASK-1",
			Name:                "lookup_issue",
			Status:              "completed",
			AuthorizationStatus: "authorized",
			Args:                json.RawMessage(`{"issue":"TASK-1"}`),
			Result:              json.RawMessage(`{"title":"Task"}`),
			StartedAt:           &now,
			CompletedAt:         &finished,
			DurationMS:          180000,
		}},
		AgentLeases: []AgentLeaseModule{{
			LeaseID:       "lease-1",
			SessionID:     "session-1",
			SourcePath:    "control-plane:agent-lease/lease-1",
			SourceHash:    "lease-hash",
			Version:       "lease-v1",
			AgentID:       "worker",
			NodeID:        "node-1",
			Token:         "token-1",
			FencingToken:  9,
			Status:        domain.AgentLeaseActive,
			ExpiresAt:     &finished,
			LastHeartbeat: &now,
		}},
		AgentOwnershipLeases: []AgentOwnershipLeaseModule{{
			AgentID:         "worker",
			LeaseID:         "owner-lease-1",
			OwnerID:         "daemon",
			SourcePath:      "control-plane:agent-ownership-lease/worker",
			SourceHash:      "owner-lease-hash",
			Version:         "owner-lease-v1",
			RuntimeProvider: domain.RuntimeProviderLocal,
			NodeID:          "node-1",
			Token:           "owner-token-1",
			FencingToken:    11,
			Status:          domain.AgentLeaseActive,
			ExpiresAt:       &finished,
			LastHeartbeat:   &now,
		}},
		AgentCommands: []AgentCommandModule{{
			CommandID:     "cmd-1",
			SourcePath:    "control-plane:agent-command/cmd-1",
			SourceHash:    "cmd-hash",
			Version:       "cmd-v1",
			Cursor:        7,
			TargetAgentID: "worker",
			TargetNodeID:  "node-1",
			SessionID:     "session-1",
			Type:          "start-task",
			Status:        domain.AgentCommandSucceeded,
			Result:        "started",
		}},
		TerminalSessions: []TerminalSessionModule{{
			TerminalID:      "term-1",
			SourcePath:      "control-plane:terminal-session/term-1",
			SourceHash:      "terminal-hash",
			Version:         "terminal-v1",
			AgentID:         "worker",
			SessionID:       "session-1",
			NodeID:          "node-1",
			Title:           "Worker terminal",
			Kind:            "pty",
			Status:          domain.TerminalSessionOpen,
			PTYProvider:     "local",
			StreamRef:       "stream://term-1",
			TranscriptRef:   "transcript://term-1",
			AttachedClients: 1,
			LastSeenAt:      &now,
		}},
		Artifacts: []ArtifactModule{{
			ArtifactID: "artifact-1",
			SourcePath: "control-plane:artifact/artifact-1",
			SourceHash: "artifact-hash",
			Version:    "artifact-v1",
			AgentID:    "worker",
			SessionID:  "session-1",
			Type:       "report",
			URI:        "artifact://report.json",
			Summary:    "runtime report",
		}},
		WorkflowRuns: []WorkflowRunModule{{
			RunID:        "wrun-1",
			WorkflowName: "epic-runner",
			SourcePath:   "control-plane:workflow-run/wrun-1",
			SourceHash:   "workflow-run-hash",
			Version:      "workflow-run-v1",
			Input:        json.RawMessage(`{"parentId":"EPIC-1"}`),
			Status:       domain.WorkflowRunRunning,
			LeaseToken:   "workflow-token",
			StartedAt:    &now,
		}},
		TaskRuns: []TaskRunModule{{
			TaskRunID:     "trun-1",
			WorkflowRunID: "wrun-1",
			WorkItemID:    "TASK-1",
			RoleName:      "task",
			SourcePath:    "control-plane:task-run/trun-1",
			SourceHash:    "task-run-hash",
			Version:       "task-run-v1",
			Status:        domain.TaskRunRunning,
			AgentID:       "worker",
			NodeID:        "node-1",
			SessionID:     "session-1",
			StartedAt:     &now,
		}},
		RunEvents: []RunEventModule{{
			EventID:       "evt-1",
			WorkflowRunID: "wrun-1",
			TaskRunID:     "trun-1",
			SourcePath:    "control-plane:run-event/evt-1",
			SourceHash:    "event-hash",
			Version:       "event-v1",
			EventIndex:    1,
			Type:          "task_run_dispatched",
			Data:          json.RawMessage(`{"task_run_id":"trun-1"}`),
		}},
	}
	exportRoot := t.TempDir()
	files, err := WriteRuntimeStateExport(exportRoot, plan, false)
	if err != nil {
		t.Fatalf("WriteRuntimeStateExport() error = %v", err)
	}
	if len(files) != 1 || files[0].Path != runtimeStateExportPath {
		t.Fatalf("state files = %+v, want runtime state snapshot", files)
	}
	data, err := os.ReadFile(filepath.Join(exportRoot, ".loom", "state", "workspace-runtime-state.json"))
	if err != nil {
		t.Fatalf("read runtime state snapshot: %v", err)
	}
	if !strings.Contains(string(data), runtimeStateExportSchema) ||
		!strings.Contains(string(data), `"agent_instances"`) ||
		!strings.Contains(string(data), `"agent_session_operations"`) ||
		!strings.Contains(string(data), `"agent_session_tool_calls"`) ||
		!strings.Contains(string(data), `"workflow_runs"`) {
		t.Fatalf("runtime state snapshot = %s, want reviewable mutable runtime records", data)
	}

	loaded, err := Load(exportRoot)
	if err != nil {
		t.Fatalf("Load(exportRoot) error = %v", err)
	}
	if got := Summary(loaded); got != "agents=0 workflows=0 runtimes=0 agent_instances=1 nodes=1 agent_sessions=1 agent_session_operations=1 agent_session_tool_calls=1 agent_leases=1 agent_ownership_leases=1 agent_commands=1 terminal_sessions=1 workflow_runs=1 task_runs=1 run_events=1 artifacts=1" {
		t.Fatalf("Summary(loaded) = %q, want mutable runtime state imported from reviewable artifact", got)
	}
	if loaded.AgentInstances[0].Name != "worker" ||
		loaded.WorkflowRuns[0].RunID != "wrun-1" ||
		!strings.Contains(string(loaded.WorkflowRuns[0].Input), `"parentId"`) ||
		loaded.TaskRuns[0].TaskRunID != "trun-1" ||
		loaded.RunEvents[0].EventID != "evt-1" ||
		loaded.AgentSessionOperations[0].OperationID != "op-1" ||
		loaded.AgentSessionToolCalls[0].CallID != "call-1" ||
		loaded.AgentSessions[0].ExitCode == nil ||
		*loaded.AgentSessions[0].ExitCode != 0 {
		t.Fatalf("loaded runtime state = %+v, want source-loadable mutable records", loaded)
	}

	imported := memstore.New()
	if _, err := imported.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "STATE-IMPORT", Name: "State Import"}); err != nil {
		t.Fatalf("create state import workspace: %v", err)
	}
	if err := Apply(context.Background(), imported, "STATE-IMPORT", "test", loaded); err != nil {
		t.Fatalf("Apply(loaded runtime state) error = %v", err)
	}
	importedRun, err := imported.WorkflowRuns().Get(context.Background(), "STATE-IMPORT", "wrun-1")
	if err != nil {
		t.Fatalf("get imported workflow run: %v", err)
	}
	importedSession, err := imported.AgentSessions().Get(context.Background(), "STATE-IMPORT", "session-1")
	if err != nil {
		t.Fatalf("get imported agent session: %v", err)
	}
	importedOperation, err := imported.AgentSessionOperations().Get(context.Background(), "STATE-IMPORT", "op-1")
	if err != nil {
		t.Fatalf("get imported agent session operation: %v", err)
	}
	importedToolCall, err := imported.AgentSessionToolCalls().Get(context.Background(), "STATE-IMPORT", "call-1")
	if err != nil {
		t.Fatalf("get imported agent session tool call: %v", err)
	}
	importedArtifact, err := imported.Artifacts().Get(context.Background(), "STATE-IMPORT", "artifact-1")
	if err != nil {
		t.Fatalf("get imported artifact: %v", err)
	}
	if importedRun.Status != domain.WorkflowRunRunning ||
		importedSession.Status != domain.AgentSessionRunning ||
		importedOperation.Status != domain.AgentSessionOperationCompleted ||
		!strings.Contains(string(importedOperation.Result), `"summary": "done"`) ||
		importedToolCall.Status != "completed" ||
		!strings.Contains(string(importedToolCall.Result), `"title": "Task"`) ||
		importedArtifact.Summary != "runtime report" {
		t.Fatalf("imported runtime state run=%+v session=%+v operation=%+v tool_call=%+v artifact=%+v, want durable state replay", importedRun, importedSession, importedOperation, importedToolCall, importedArtifact)
	}
}

func sourceExportPaths(files []SourceExportFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}
