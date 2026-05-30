package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSlackCloneEpicWorkflowEnsuresTaskRuns(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	ib := clitest.NewMockIssueBackend()
	ready := backend.IssueData{ID: "SLACK-2", Title: "Build channel sidebar", Status: "open"}
	ib.ReadyResult = []backend.IssueData{ready}
	ib.ListResult = []backend.IssueData{ready}

	input := ParentWorkItemsInput{ParentID: "SLACK-1", Role: "task", MaxConcurrency: 2}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "WS", RunParentWorkItemsName, raw, "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, ib, run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.DispatchedCount != 1 {
		t.Fatalf("DispatchedCount = %d, want 1", result.DispatchedCount)
	}

	taskRuns, err := st.TaskRuns().List(ctx, "WS", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: ready.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 {
		t.Fatalf("task runs = %d, want 1", len(taskRuns))
	}
	if taskRuns[0].Status != domain.TaskRunStarting || taskRuns[0].RoleName != "task" || taskRuns[0].AgentID == "" || taskRuns[0].CommandID == "" {
		t.Fatalf("task run = %+v, want dispatched starting task role", taskRuns[0])
	}
	cmds, err := st.AgentCommands().List(ctx, "WS", store.AgentCommandFilter{TargetAgentID: taskRuns[0].AgentID})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Payload["workflow_run_id"] != run.RunID || cmds[0].Payload["task_run_id"] != taskRuns[0].TaskRunID {
		t.Fatalf("commands = %+v, want one start command linked to workflow/task run", cmds)
	}

	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "task_run_ensured") || !hasWorkflowEvent(events, "task_run_dispatched") {
		t.Fatalf("events = %+v, want task ensure and dispatch events", events)
	}
	updated, err := st.WorkflowRuns().Get(ctx, "WS", run.RunID)
	if err != nil {
		t.Fatalf("get workflow run: %v", err)
	}
	if updated.Status != domain.WorkflowRunWaiting {
		t.Fatalf("workflow status = %q, want waiting", updated.Status)
	}
}

func TestCodeDefinedSlackCloneWorkflowDelegatesToBuiltinRunner(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	manifest := json.RawMessage(`{"name":"slack-clone-epic-runner","builtin":"run-parent-work-items"}`)
	if _, err := st.WorkflowDefinitions().Upsert(ctx, store.WorkflowDefinitionUpsert{
		WorkspaceKey: "WS",
		Name:         "slack-clone-epic-runner",
		Version:      "v1",
		Manifest:     manifest,
		Status:       domain.DefinitionStatusActive,
	}); err != nil {
		t.Fatalf("upsert workflow definition: %v", err)
	}

	ib := clitest.NewMockIssueBackend()
	task := backend.IssueData{ID: "SLACK-3", Title: "Add message composer", Status: "open"}
	ib.ReadyResult = []backend.IssueData{task}
	ib.ListResult = []backend.IssueData{task}
	raw := json.RawMessage(`{"parentId":"SLACK-1","role":"task","maxConcurrency":1}`)
	run, err := CreateOrResumeRun(ctx, st, "WS", "slack-clone-epic-runner", raw, "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := RunOnce(ctx, st, ib, run); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	taskRuns, err := st.TaskRuns().List(ctx, "WS", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: task.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 {
		t.Fatalf("task runs = %d, want 1 from code-defined workflow delegation", len(taskRuns))
	}
	if taskRuns[0].Status != domain.TaskRunStarting || taskRuns[0].CommandID == "" {
		t.Fatalf("task run = %+v, want dispatched code-defined task run", taskRuns[0])
	}
	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	foundReconcile := false
	for _, event := range events {
		if event.Type == "workflow_ts_reconciled" {
			foundReconcile = true
			break
		}
	}
	if !foundReconcile || !hasWorkflowEvent(events, "task_run_dispatched") {
		t.Fatalf("events = %+v, want workflow_ts_reconciled and dispatch events", events)
	}
}

func TestCodeDefinedWorkflowExecutesConstrainedTypeScriptContext(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-epic.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-epic',
  singleton: (input) => `+"`"+`parent:${input.parentId}`+"`"+`,
  tools: ['workItems.readyChildren', 'taskRuns.ensure'],
  async run(ctx) {
    ctx.log.info('context epic started', { parentId: ctx.input.parentId });
    const ready = await ctx.workItems.readyChildren(String(ctx.input.parentId));
    for (const issue of ready) {
      await ctx.taskRuns.ensure({
        workItemId: issue.id,
        role: 'task',
        reason: issue.title,
        metadata: { source: 'typescript' },
      });
    }
    return { ensured: ready.length };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(plan.Workflows) != 1 || plan.Workflows[0].Runner != "workflow-context-v1" {
		t.Fatalf("workflows = %+v, want one WorkflowContext runner", plan.Workflows)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSCTX", Name: "TypeScript Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSCTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	ready := backend.IssueData{ID: "CTX-2", Title: "Build workflow context", Status: "open"}
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{ready}
	ib.BlockedResult = nil
	ib.ListResult = []backend.IssueData{ready}

	run, err := CreateOrResumeRun(ctx, st, "TSCTX", "context-epic", json.RawMessage(`{"parentId":"CTX-1"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, ib, run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunWaiting || len(result.TaskRuns) != 1 || result.DispatchedCount != 1 {
		t.Fatalf("result = %+v, want waiting run with one dispatched task", result)
	}
	taskRuns, err := st.TaskRuns().List(ctx, "TSCTX", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: ready.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 || taskRuns[0].Status != domain.TaskRunStarting || taskRuns[0].Metadata["source"] != "typescript" {
		t.Fatalf("taskRuns = %+v, want TypeScript-created dispatched task run", taskRuns)
	}
	events, err := st.RunEvents().List(ctx, "TSCTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	for _, typ := range []string{"workflow_ts_context_started", "workflow_log", "task_run_ensured", "task_run_dispatched"} {
		if !hasWorkflowEvent(events, typ) {
			t.Fatalf("events = %+v, want %s", events, typ)
		}
	}
}

func TestCodeDefinedWorkflowContextWorkItemQueriesHonorOptions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKFLOW_ENV_TOKEN", "loaded-from-env")
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-queries.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-queries',
  singleton: (input) => `+"`"+`parent:${input.parentId}`+"`"+`,
  tools: ['workItems.readyChildren', 'workItems.blockedChildren', 'workItems.listChildren', 'taskRuns.ensure'],
  env: ['WORKFLOW_ENV_TOKEN'],
  async run(ctx) {
    const parentId = String(ctx.input.parentId);
    const ready = await ctx.workItems.readyChildren(parentId, { limit: 1 });
    const blocked = await ctx.workItems.blockedChildren(parentId, { limit: 2 });
    const children = await ctx.workItems.listChildren(parentId);
    ctx.log.info('context query counts', {
      ready: ready.length,
      blocked: blocked.length,
      children: children.length,
    });
    for (const issue of ready) {
      await ctx.taskRuns.ensure({
        workItemId: issue.id,
        role: 'task',
        reason: issue.title,
        metadata: {
          source: 'typescript-query',
          blocked: String(blocked.length),
          children: String(children.length),
          actor: String(ctx.req.actor),
          workflow: String(ctx.req.workflowName),
          envToken: String(ctx.env.WORKFLOW_ENV_TOKEN),
        },
      });
    }
    return { ready: ready.length, blocked: blocked.length, children: children.length };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSQ", Name: "TypeScript Query Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSQ", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	firstReady := backend.IssueData{ID: "TSQ-2", Title: "Build sidebar", Status: "open", Parent: "TSQ-1"}
	secondReady := backend.IssueData{ID: "TSQ-3", Title: "Build composer", Status: "open", Parent: "TSQ-1"}
	blocked := backend.IssueData{ID: "TSQ-4", Title: "Blocked notifications", Status: "blocked", Parent: "TSQ-1"}
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{firstReady, secondReady}
	ib.BlockedResult = []backend.IssueData{blocked}
	ib.ListResult = []backend.IssueData{firstReady, secondReady, blocked}

	run, err := CreateOrResumeRun(ctx, st, "TSQ", "context-queries", json.RawMessage(`{"parentId":"TSQ-1"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, ib, run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunWaiting || len(result.TaskRuns) != 1 {
		t.Fatalf("result = %+v, want waiting run with one limited task run", result)
	}
	firstTaskRuns, err := st.TaskRuns().List(ctx, "TSQ", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: firstReady.ID})
	if err != nil {
		t.Fatalf("list first task runs: %v", err)
	}
	if len(firstTaskRuns) != 1 ||
		firstTaskRuns[0].Metadata["source"] != "typescript-query" ||
		firstTaskRuns[0].Metadata["blocked"] != "1" ||
		firstTaskRuns[0].Metadata["children"] != "3" ||
		firstTaskRuns[0].Metadata["actor"] != "atlas" ||
		firstTaskRuns[0].Metadata["workflow"] != "context-queries" ||
		firstTaskRuns[0].Metadata["envToken"] != "loaded-from-env" {
		t.Fatalf("first task runs = %+v, want limited ready query with blocked/list metadata", firstTaskRuns)
	}
	secondTaskRuns, err := st.TaskRuns().List(ctx, "TSQ", store.TaskRunFilter{WorkflowRunID: run.RunID, WorkItemID: secondReady.ID})
	if err != nil {
		t.Fatalf("list second task runs: %v", err)
	}
	if len(secondTaskRuns) != 0 {
		t.Fatalf("second task runs = %+v, want readyChildren limit to skip second ready child", secondTaskRuns)
	}
	events, err := st.RunEvents().List(ctx, "TSQ", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "workflow_log") || !hasWorkflowEvent(events, "task_run_ensured") {
		t.Fatalf("events = %+v, want query log and ensure evidence", events)
	}
}

func TestCodeDefinedWorkflowContextExecutesDeclaredTool(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	toolPath := filepath.Join(root, ".loom", "tools", "echo-tool.ts")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte(`import { defineTool } from '@loom/runtime';

export default defineTool({
  name: 'echo_tool',
  description: 'Echo a message for workflow tests.',
  parameters: {
    type: 'object',
    required: ['message'],
    properties: { message: { type: 'string' } },
  },
  async execute(args) {
    return { echo: String(args.message ?? '') };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write tool: %v", err)
	}
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-tool.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';
import echoTool from '../tools/echo-tool';

export default defineWorkflow({
  name: 'context-tool',
  tools: [echoTool],
  async run(ctx) {
    const result = await ctx.tools.echo_tool({ message: String(ctx.input.message ?? '') });
    ctx.log.info('typed tool returned', result);
    return result;
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSTOOLCTX", Name: "TypeScript Tool Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSTOOLCTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSTOOLCTX", "context-tool", json.RawMessage(`{"message":"hello tool"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed tool workflow", result)
	}
	var data map[string]string
	if err := json.Unmarshal(result.Run.Result, &data); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if data["echo"] != "hello tool" {
		t.Fatalf("result data = %+v, want echoed tool result", data)
	}
	events, err := st.RunEvents().List(ctx, "TSTOOLCTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "tool_call") || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want tool call and completion evidence", events)
	}
}

func TestCodeDefinedWorkflowContextRecordsArtifact(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-artifact.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-artifact',
  async run(ctx) {
    const recorded = await ctx.artifacts.record({
      type: 'report',
      uri: 'artifact://workflow/context-artifact/report.json',
      summary: 'controller report',
      mimeType: 'application/json',
      sizeBytes: 123,
      taskId: String(ctx.input.taskId ?? ''),
      metadata: { source: 'workflow-context' },
    });
    return { artifactUri: recorded.uri };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSART", Name: "TypeScript Artifact Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSART", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSART", "context-artifact", json.RawMessage(`{"taskId":"TASK-ART"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed artifact workflow", result)
	}
	artifacts, err := st.Artifacts().List(ctx, "TSART", store.ArtifactFilter{TaskID: "TASK-ART", Type: "report"})
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].URI != "artifact://workflow/context-artifact/report.json" ||
		artifacts[0].MIMEType != "application/json" || artifacts[0].SizeBytes != 123 ||
		artifacts[0].Metadata["source"] != "workflow-context" {
		t.Fatalf("artifacts = %+v, want recorded controller artifact", artifacts)
	}
	events, err := st.RunEvents().List(ctx, "TSART", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "artifact_recorded") || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want artifact recording and completion evidence", events)
	}
}

func TestCodeDefinedWorkflowContextInitializesAgentSession(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-session.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { createAgent, defineWorkflow } from '@loom/runtime';

const worker = createAgent(({ id }) => ({
  name: 'worker-one',
  model: 'test/model',
  backend: 'codex',
  instructions: `+"`"+`Handle workflow run ${id}.`+"`"+`,
}));

export default defineWorkflow({
  name: 'context-session',
  async run(ctx) {
    const harness = await ctx.init(worker, { name: 'planning' });
    const session = await harness.session('review', {
      taskId: String(ctx.input.taskId ?? ''),
      phase: 'planning',
      metadata: { source: 'workflow-context' },
    });
    const report = await session.prompt({
      instruction: 'Review the current task and summarize the next step.',
      mockResult: { summary: 'ready to continue', needsFix: false },
    });
    await session.skill({ name: 'review-checklist', instruction: 'Apply the review checklist.' });
    await session.task({ instruction: 'Delegate a bounded child session.' });
    await session.shell({ command: 'npm test', mockResult: { exitCode: 0 } });
    await ctx.agents.session({
      agentId: 'worker-two',
      sessionId: 'explicit-session',
      kind: 'ad_hoc',
      status: 'idle',
      metadata: { source: 'direct-context' },
    });
    return { agentId: session.agentId, sessionName: session.sessionName, summary: report.summary };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSESSION", Name: "TypeScript Session Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSSESSION", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSSESSION", "context-session", json.RawMessage(`{"taskId":"TASK-SESSION"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed session workflow", result)
	}
	sessions, err := st.AgentSessions().List(ctx, "TSSESSION", store.AgentSessionFilter{AgentID: "worker-one", TaskID: "TASK-SESSION"})
	if err != nil {
		t.Fatalf("list worker sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Kind != domain.AgentSessionKindTask ||
		sessions[0].Status != domain.AgentSessionRunning || sessions[0].Phase != "planning" ||
		sessions[0].Metadata["source"] != "workflow-context" ||
		sessions[0].Metadata["source_agent_name"] != "worker-one" ||
		sessions[0].Metadata["workflow_run_id"] != run.RunID ||
		sessions[0].Metadata["workflow_name"] != "context-session" ||
		sessions[0].Metadata["harness"] != "planning" ||
		sessions[0].Metadata["session_name"] != "review" ||
		sessions[0].Metadata["model"] != "test/model" ||
		sessions[0].Metadata["backend"] != "codex" {
		t.Fatalf("sessions = %+v, want durable Flue-shaped workflow session", sessions)
	}
	direct, err := st.AgentSessions().Get(ctx, "TSSESSION", "explicit-session")
	if err != nil {
		t.Fatalf("get direct session: %v", err)
	}
	if direct.AgentID != "worker-two" || direct.Kind != domain.AgentSessionKindAdHoc ||
		direct.Status != domain.AgentSessionIdle || direct.Metadata["source"] != "direct-context" {
		t.Fatalf("direct session = %+v, want explicit low-level agent session", direct)
	}
	events, err := st.RunEvents().List(ctx, "TSSESSION", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "agent_session_initialized") || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want session initialization and completion evidence", events)
	}
	if countWorkflowEvents(events, "agent_session_operation") != 4 {
		t.Fatalf("events = %+v, want four model-visible session operation events", events)
	}
	promptEvent := workflowEventDataByOperation(t, events, "prompt")
	if promptEvent["status"] != "completed" || promptEvent["operationId"] == "" {
		t.Fatalf("prompt event = %+v, want completed operation with operation id", promptEvent)
	}
	promptResult, ok := promptEvent["result"].(map[string]any)
	if !ok || promptResult["summary"] != "ready to continue" || promptResult["needsFix"] != false {
		t.Fatalf("prompt result = %+v, want captured structured mock result", promptEvent["result"])
	}
	if _, ok := promptEvent["durationMs"].(float64); !ok {
		t.Fatalf("prompt event = %+v, want durationMs", promptEvent)
	}
	shellEvent := workflowEventDataByOperation(t, events, "shell")
	shellResult, ok := shellEvent["result"].(map[string]any)
	if !ok || shellResult["exitCode"] != float64(0) {
		t.Fatalf("shell result = %+v, want captured shell mock result", shellEvent["result"])
	}
}

func TestCodeDefinedWorkflowContextReadsWorkflowAndTaskRunState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-controller.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-controller',
  async run(ctx) {
    const status = await ctx.workflow.status();
    const cancelled = await ctx.workflow.cancelRequested();
    const all = await ctx.taskRuns.list();
    const passed = await ctx.taskRuns.wait({ status: 'passed' });
    ctx.log.info('controller state observed', {
      workflowStatus: status.status,
      taskRuns: all.length,
      passed: passed.length,
    });
    return {
      workflowStatus: status.status,
      cancelled,
      allCount: all.length,
      passedCount: passed.length,
    };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSCTRL", Name: "TypeScript Controller Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSCTRL", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSCTRL", "context-controller", json.RawMessage(`{"parentId":"CTRL-1"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:   "TSCTRL",
		WorkflowRunID:  run.RunID,
		WorkItemID:     "CTRL-2",
		RoleName:       "task",
		IdempotencyKey: "seed:CTRL-2",
		Status:         domain.TaskRunPassed,
	}); err != nil {
		t.Fatalf("seed task run: %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed controller workflow", result)
	}
	var output struct {
		WorkflowStatus string `json:"workflowStatus"`
		Cancelled      bool   `json:"cancelled"`
		AllCount       int    `json:"allCount"`
		PassedCount    int    `json:"passedCount"`
	}
	if err := json.Unmarshal(result.Run.Result, &output); err != nil {
		t.Fatalf("decode result %s: %v", result.Run.Result, err)
	}
	if output.WorkflowStatus != string(domain.WorkflowRunQueued) || output.Cancelled || output.AllCount != 1 || output.PassedCount != 1 {
		t.Fatalf("output = %+v, want durable workflow/task run projection", output)
	}
	events, err := st.RunEvents().List(ctx, "TSCTRL", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "task_runs_observed") || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want task run observation and completion evidence", events)
	}
}

func TestCodeDefinedWorkflowContextWaitUntilMovesRunToWaiting(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-wait.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-wait',
  async run(ctx) {
    await ctx.workflow.waitUntil('external_signal:continue');
    return { waiting: true };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSWAIT", Name: "TypeScript Wait Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSWAIT", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSWAIT", "context-wait", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunWaiting || result.Run.WaitCondition != "external_signal:continue" {
		t.Fatalf("result = %+v, want explicit wait condition", result)
	}
	events, err := st.RunEvents().List(ctx, "TSWAIT", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "workflow_wait_requested") || !hasWorkflowEvent(events, "workflow_waiting") {
		t.Fatalf("events = %+v, want wait request and waiting evidence", events)
	}
}

func TestCodeDefinedWorkflowContextCancelMovesRunToCancelled(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-cancel.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-cancel',
  async run(ctx) {
    await ctx.workflow.cancel({ reason: 'policy stop', metadata: { source: 'test' } });
    return { shouldNotComplete: true };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSCANCEL", Name: "TypeScript Cancel Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSCANCEL", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSCANCEL", "context-cancel", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCancelled || result.Run.FinishedAt == nil || !result.Done {
		t.Fatalf("result = %+v, want cancelled terminal workflow run", result)
	}
	events, err := st.RunEvents().List(ctx, "TSCANCEL", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, did not want workflow_completed after cancel", events)
	}
	cancelEvent := workflowEventDataByType(t, events, "workflow_cancelled")
	if cancelEvent["reason"] != "policy stop" || cancelEvent["source"] != "workflow_context" {
		t.Fatalf("cancel event = %+v, want workflow-context cancel reason", cancelEvent)
	}
}

func TestCodeDefinedWorkflowContextReadsTaskClaimsAndAdmitsDispatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-claims.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-claims',
  async run(ctx) {
    const claims = await ctx.taskClaims.list();
    const claim = await ctx.taskClaims.get('CLAIM-1');
    const observed = await ctx.taskClaims.wait({ workItemId: 'CLAIM-1' });
    const dispatch = await ctx.agents.dispatch('worker-one', {
      workItemId: 'CLAIM-1',
      message: 'continue from claim projection',
    });
    return {
      claims: claims.length,
      observed: observed.length,
      actor: claim?.claim_actor ?? '',
      dispatched: dispatch.accepted === true,
    };
  },
});
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSCLAIM", Name: "TypeScript Claim Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSCLAIM", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSCLAIM", "context-claims", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	if _, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:   "TSCLAIM",
		WorkflowRunID:  run.RunID,
		WorkItemID:     "CLAIM-1",
		RoleName:       "task",
		IdempotencyKey: "seed:CLAIM-1",
		ClaimActor:     "worker-one",
		ClaimEventID:   "evt-claim",
		Status:         domain.TaskRunPassed,
		AgentID:        "worker-one",
		SessionID:      "session-one",
	}); err != nil {
		t.Fatalf("seed claim projection: %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed claim workflow", result)
	}
	var output struct {
		Claims     int    `json:"claims"`
		Observed   int    `json:"observed"`
		Actor      string `json:"actor"`
		Dispatched bool   `json:"dispatched"`
	}
	if err := json.Unmarshal(result.Run.Result, &output); err != nil {
		t.Fatalf("decode result %s: %v", result.Run.Result, err)
	}
	if output.Claims != 1 || output.Observed != 1 || output.Actor != "worker-one" || !output.Dispatched {
		t.Fatalf("output = %+v, want task claim projection and dispatch admission", output)
	}
	events, err := st.RunEvents().List(ctx, "TSCLAIM", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "task_claims_observed") || !hasWorkflowEvent(events, "agent_dispatch_admitted") ||
		!hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want task claim observation, dispatch, and completion evidence", events)
	}
}

func TestParentWorkItemsWorkflowCompletesWhenNoOpenChildren(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = nil
	ib.BlockedResult = nil
	ib.ListResult = nil

	run, err := CreateOrResumeRun(ctx, st, "WS", RunParentWorkItemsName, json.RawMessage(`{"parentId":"DONE","role":"task"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunBuiltinOnce(ctx, st, ib, run)
	if err != nil {
		t.Fatalf("RunBuiltinOnce() error = %v", err)
	}
	if !result.Done || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed done run", result)
	}
	events, err := st.RunEvents().List(ctx, "WS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want workflow_completed", events)
	}
}

func TestParentWorkItemHelpers(t *testing.T) {
	input, err := decodeParentInput(json.RawMessage(`{"parentId":"EPIC","maxConcurrency":0}`))
	if err != nil {
		t.Fatalf("decodeParentInput() error = %v", err)
	}
	if input.Role != "task" || input.MaxConcurrency != 4 {
		t.Fatalf("input = %+v, want default role/task concurrency", input)
	}
	if _, err := decodeParentInput(json.RawMessage(`{"parentId":`)); err == nil {
		t.Fatal("decodeParentInput() succeeded for invalid JSON")
	}

	live := map[string]struct{}{"TASK-1": {}}
	for _, child := range []backend.IssueData{
		{ID: "TASK-1", Status: "open"},
		{ID: "TASK-2", Status: "closed"},
		{ID: "TASK-3", Status: "deferred"},
	} {
		if shouldEnsureChild(child, live) {
			t.Fatalf("shouldEnsureChild(%+v) = true, want false", child)
		}
	}
	if !shouldEnsureChild(backend.IssueData{ID: "TASK-4", Status: "open"}, live) {
		t.Fatal("shouldEnsureChild(open unseen child) = false, want true")
	}

	ensure := taskRunEnsure(&domain.WorkflowRun{WorkspaceKey: "WS", RunID: "RUN", WorkflowName: "wf"}, input, backend.IssueData{ID: "TASK-4", Title: "Build"})
	if ensure.IdempotencyKey != "child:TASK-4:role:task" || ensure.Metadata["parent_id"] != "EPIC" {
		t.Fatalf("taskRunEnsure() = %+v", ensure)
	}
}

func hasWorkflowEvent(events []*domain.RunEvent, typ string) bool {
	for _, event := range events {
		if event != nil && event.Type == typ {
			return true
		}
	}
	return false
}

func countWorkflowEvents(events []*domain.RunEvent, typ string) int {
	count := 0
	for _, event := range events {
		if event != nil && event.Type == typ {
			count++
		}
	}
	return count
}

func workflowEventDataByOperation(t *testing.T, events []*domain.RunEvent, operation string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event == nil || event.Type != "agent_session_operation" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("decode event data %s: %v", event.EventID, err)
		}
		if data["operation"] == operation {
			return data
		}
	}
	t.Fatalf("missing agent_session_operation event for %s in %+v", operation, events)
	return nil
}

func workflowEventDataByType(t *testing.T, events []*domain.RunEvent, typ string) map[string]any {
	t.Helper()
	for _, event := range events {
		if event == nil || event.Type != typ {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("decode event data %s: %v", event.EventID, err)
		}
		return data
	}
	t.Fatalf("missing workflow event %s in %+v", typ, events)
	return nil
}
