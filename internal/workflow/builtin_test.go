package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRunOncePersistsFailedWorkflowRunOnTypeScriptError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-fails.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-fails',
  async run(ctx) {
    ctx.log.info('about to fail');
    throw new Error('boom from workflow');
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSFAIL", Name: "TypeScript Failure"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSFAIL", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSFAIL", "context-fails", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	_, err = RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err == nil || !strings.Contains(err.Error(), "boom from workflow") {
		t.Fatalf("RunOnce() error = %v, want TypeScript workflow failure", err)
	}
	failed, err := st.WorkflowRuns().Get(ctx, "TSFAIL", run.RunID)
	if err != nil {
		t.Fatalf("get failed run: %v", err)
	}
	if failed.Status != domain.WorkflowRunFailed || failed.ErrorClass != "runner_error" ||
		!strings.Contains(failed.ErrorMessage, "boom from workflow") || failed.FinishedAt == nil {
		t.Fatalf("failed run = %+v, want durable failed state with runner error", failed)
	}
	events, err := st.RunEvents().List(ctx, "TSFAIL", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "workflow_ts_context_started") || !hasWorkflowEvent(events, "workflow_failed") {
		t.Fatalf("events = %+v, want context start and durable failure evidence", events)
	}
	failureEvent := workflowEventDataByType(t, events, "workflow_failed")
	errorMessage, _ := failureEvent["error_message"].(string)
	if failureEvent["error_class"] != "runner_error" ||
		!strings.Contains(errorMessage, "boom from workflow") ||
		failureEvent["source"] != "workflow_run_once" {
		t.Fatalf("failure event = %+v, want runner error evidence", failureEvent)
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

func TestCodeDefinedWorkflowContextGetsAndCommentsOnWorkItem(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-work-item.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-work-item',
  tools: ['workItems.get', 'workItems.comment'],
  async run(ctx) {
    const issue = await ctx.workItems.get('TSWI-2');
    if (!issue) throw new Error('missing work item');
    const comment = await ctx.workItems.comment({
      workItemId: issue.id,
      text: `+"`"+`reviewed ${issue.title}`+"`"+`,
      metadata: { source: 'workflow-context' },
    });
    return { title: issue.title, commentAccepted: comment.accepted };
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSWI", Name: "TypeScript Work Item Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSWI", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	ib := clitest.NewMockIssueBackend()
	ib.ListResult = []backend.IssueData{{ID: "TSWI-2", Title: "Review composer", Status: "closed", Parent: "TSWI-1"}}
	ib.AddCommentResult = &backend.CommentData{ID: 42, IssueID: "TSWI-2", Author: "atlas", Text: "reviewed Review composer"}

	run, err := CreateOrResumeRun(ctx, st, "TSWI", "context-work-item", json.RawMessage(`{"parentId":"TSWI-1"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, ib, run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed work item controller workflow", result)
	}
	if ib.CallCount("AddComment") != 1 {
		t.Fatalf("AddComment calls = %+v, want one work item comment", ib.Calls)
	}
	params, ok := ib.Calls[len(ib.Calls)-1].Args[0].(backend.CommentAddParams)
	if !ok || params.IssueID != "TSWI-2" || params.Author != "atlas" || params.Text != "reviewed Review composer" {
		t.Fatalf("AddComment params = %+v, want workflow-authored comment", ib.Calls[len(ib.Calls)-1].Args)
	}
	events, err := st.RunEvents().List(ctx, "TSWI", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "work_item_read") || !hasWorkflowEvent(events, "work_item_comment_added") || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want work item read/comment and completion evidence", events)
	}
	commentEvent := workflowEventDataByType(t, events, "work_item_comment_added")
	if commentEvent["work_item_id"] != "TSWI-2" || commentEvent["author"] != "atlas" || commentEvent["comment_id"] != float64(42) {
		t.Fatalf("comment event = %+v, want durable comment evidence", commentEvent)
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

func TestCodeDefinedWorkflowContextStagesFilesAndCompactsSession(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-staging.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-staging',
  async run(ctx) {
    const staged = await ctx.staging.writeText('reports/summary.md', '# Summary\nready', {
      summary: 'controller-staged report',
      metadata: { source: 'workflow-context' },
    });
    const reread = await ctx.files.readText('reports/summary.md');
    const session = await ctx.agents.session({
      agentId: 'worker-staging',
      taskId: String(ctx.input.taskId ?? ''),
      sessionName: 'phase-one',
    });
    const compacted = await session.compact({ reason: 'phase boundary' });
    return {
      stagedUri: staged.uri,
      reread,
      compactOperation: compacted.operation,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSTAGE", Name: "TypeScript Staging Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSSTAGE", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSSTAGE", "context-staging", json.RawMessage(`{"taskId":"TASK-STAGE"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed staging workflow", result)
	}
	artifacts, err := st.Artifacts().List(ctx, "TSSTAGE", store.ArtifactFilter{TaskID: "", Type: "staging"})
	if err != nil {
		t.Fatalf("list staged artifacts: %v", err)
	}
	if len(artifacts) != 1 || !strings.HasPrefix(artifacts[0].URI, "file://") ||
		artifacts[0].MIMEType != "text/plain; charset=utf-8" ||
		artifacts[0].SizeBytes != int64(len("# Summary\nready")) ||
		!strings.HasPrefix(artifacts[0].Checksum, "sha256:") ||
		artifacts[0].Metadata["path"] != "reports/summary.md" ||
		artifacts[0].Metadata["source"] != "workflow_context_staging" {
		t.Fatalf("artifacts = %+v, want controller-staged file artifact", artifacts)
	}
	events, err := st.RunEvents().List(ctx, "TSSTAGE", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "workflow_file_staged") || !hasWorkflowEvent(events, "workflow_file_read") ||
		!hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want staged/read file and completion evidence", events)
	}
	compactEvent := workflowEventDataByOperation(t, events, "compact")
	if compactEvent["status"] != "completed" || compactEvent["visibility"] != "controller" {
		t.Fatalf("compact event = %+v, want completed controller compaction evidence", compactEvent)
	}
}

func TestCodeDefinedWorkflowContextRecordsControllerShellRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-shell.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'context-shell',
  async run(ctx) {
    const setup = await ctx.shell.run('npm test', {
      cwd: 'packages/api',
      metadata: { phase: 'setup' },
      mockResult: { exitCode: 0, stdout: 'ok' },
    });
    const verify = await ctx.setup.shell.run({
      command: 'go test ./internal/workflow',
      mockResult: { exitCode: 0, stdout: 'workflow ok' },
    });
    return {
      setupExitCode: setup.exitCode,
      verifyStdout: verify.stdout,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSHELL", Name: "TypeScript Shell Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSSHELL", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSSHELL", "context-shell", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed shell workflow", result)
	}
	var output struct {
		SetupExitCode int    `json:"setupExitCode"`
		VerifyStdout  string `json:"verifyStdout"`
	}
	if err := json.Unmarshal(result.Run.Result, &output); err != nil {
		t.Fatalf("decode result %s: %v", result.Run.Result, err)
	}
	if output.SetupExitCode != 0 || output.VerifyStdout != "workflow ok" {
		t.Fatalf("output = %+v, want controller shell mock results", output)
	}
	events, err := st.RunEvents().List(ctx, "TSSHELL", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if countWorkflowEvents(events, "workflow_shell_run") != 2 || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want two controller shell events and completion evidence", events)
	}
	shellEvent := workflowEventDataByType(t, events, "workflow_shell_run")
	if shellEvent["command"] != "npm test" || shellEvent["cwd"] != "packages/api" ||
		shellEvent["visibility"] != "controller" || shellEvent["status"] != "completed" ||
		shellEvent["operationId"] == "" {
		t.Fatalf("shell event = %+v, want completed controller shell evidence", shellEvent)
	}
	resultData, ok := shellEvent["result"].(map[string]any)
	if !ok || resultData["exitCode"] != float64(0) || resultData["stdout"] != "ok" {
		t.Fatalf("shell event result = %+v, want captured mock result", shellEvent["result"])
	}
	if countWorkflowEvents(events, "agent_session_operation") != 0 {
		t.Fatalf("events = %+v, controller shell must not be model-visible session operation", events)
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
      result: { type: 'object', required: ['summary'] },
      providerModel: 'test/provider-model',
      usage: { inputTokens: 12, outputTokens: 5, totalTokens: 17, costUSD: 0.004 },
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
    return {
      agentId: session.agentId,
      sessionName: session.sessionName,
      summary: report.summary,
      promptOperationId: report.operationId,
      promptDataSummary: report.data?.summary,
      promptResultSummary: report.result?.summary,
      promptModel: report.model,
      promptProvider: report.provider,
      promptProviderModel: report.providerModel,
      promptUsageTotal: report.usage?.totalTokens,
      promptValidationStatus: report.validation?.status,
      promptText: report.text,
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
	var workflowOutput map[string]any
	if err := json.Unmarshal(result.Run.Result, &workflowOutput); err != nil {
		t.Fatalf("decode result %s: %v", result.Run.Result, err)
	}
	if workflowOutput["summary"] != "ready to continue" ||
		workflowOutput["promptDataSummary"] != "ready to continue" ||
		workflowOutput["promptResultSummary"] != "ready to continue" ||
		workflowOutput["promptModel"] != "test/model" ||
		workflowOutput["promptProvider"] != "codex" ||
		workflowOutput["promptProviderModel"] != "test/provider-model" ||
		workflowOutput["promptUsageTotal"] != float64(17) ||
		workflowOutput["promptValidationStatus"] != "not_validated" ||
		workflowOutput["promptText"] != "ready to continue" ||
		workflowOutput["promptOperationId"] == "" {
		t.Fatalf("workflow output = %+v, want session operation envelope fields", workflowOutput)
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
	if promptEvent["model"] != "test/model" || promptEvent["provider"] != "codex" || promptEvent["providerModel"] != "test/provider-model" {
		t.Fatalf("prompt event = %+v, want model/provider metadata", promptEvent)
	}
	promptUsage, ok := promptEvent["usage"].(map[string]any)
	if !ok || promptUsage["totalTokens"] != float64(17) || promptUsage["inputTokens"] != float64(12) {
		t.Fatalf("prompt event usage = %+v, want token usage metadata", promptEvent["usage"])
	}
	promptResult, ok := promptEvent["result"].(map[string]any)
	if !ok || promptResult["summary"] != "ready to continue" || promptResult["needsFix"] != false {
		t.Fatalf("prompt result = %+v, want captured structured mock result", promptEvent["result"])
	}
	if promptResult["operationId"] != promptEvent["operationId"] ||
		promptResult["status"] != "completed" ||
		promptResult["model"] != "test/model" ||
		promptResult["provider"] != "codex" ||
		promptResult["providerModel"] != "test/provider-model" ||
		promptResult["text"] != "ready to continue" {
		t.Fatalf("prompt result = %+v, want result envelope metadata", promptResult)
	}
	promptData, ok := promptResult["data"].(map[string]any)
	if !ok || promptData["summary"] != "ready to continue" || promptData["needsFix"] != false {
		t.Fatalf("prompt result data = %+v, want structured data payload", promptResult["data"])
	}
	promptEnvelopeUsage, ok := promptResult["usage"].(map[string]any)
	if !ok || promptEnvelopeUsage["totalTokens"] != float64(17) {
		t.Fatalf("prompt result usage = %+v, want usage envelope", promptResult["usage"])
	}
	promptValidation, ok := promptResult["validation"].(map[string]any)
	if !ok || promptValidation["requested"] != true || promptValidation["status"] != "not_validated" {
		t.Fatalf("prompt validation = %+v, want schema request evidence", promptResult["validation"])
	}
	if _, ok := promptEvent["durationMs"].(float64); !ok {
		t.Fatalf("prompt event = %+v, want durationMs", promptEvent)
	}
	shellEvent := workflowEventDataByOperation(t, events, "shell")
	shellResult, ok := shellEvent["result"].(map[string]any)
	if !ok || shellResult["exitCode"] != float64(0) {
		t.Fatalf("shell result = %+v, want captured shell mock result", shellEvent["result"])
	}
	shellData, ok := shellResult["data"].(map[string]any)
	if !ok || shellData["exitCode"] != float64(0) {
		t.Fatalf("shell data = %+v, want shell result envelope data", shellResult["data"])
	}
}

func TestCodeDefinedWorkflowContextProjectsAgentSessionToolCalls(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-session-tools.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { createAgent, defineWorkflow } from '@loom/runtime';

const worker = createAgent(() => ({
  name: 'tool-worker',
  model: 'test/model',
  backend: 'codex',
  instructions: 'Exercise session-level tool call projection.',
}));

export default defineWorkflow({
  name: 'context-session-tools',
  async run(ctx) {
    const harness = await ctx.init(worker, { name: 'tool-harness' });
    const session = await harness.session('tool-review', {
      taskId: 'TASK-TOOLS',
      phase: 'execution',
    });
    const report = await session.prompt({
      instruction: 'Use the reviewed lookup tool.',
      providerModel: 'test/provider-model',
      usage: { inputTokens: 7, outputTokens: 3, totalTokens: 10 },
      toolCalls: [{
        callId: 'call-lookup-1',
        providerCallId: 'provider-call-lookup-1',
        name: 'lookup_order_status',
        status: 'completed',
        authorizationStatus: 'authorized',
        idempotencyKey: 'idem-lookup-1',
        toolVersion: 'v1',
        sourceHash: 'sha256:lookup',
        handler: 'typescript',
        runtime: 'local',
        timeout: '2s',
        cancellable: true,
        readOnly: true,
        redacted: false,
        args: { orderId: 'order_1042' },
        result: { status: 'packed' },
      }],
      mockResult: { summary: 'tool call recorded' },
    });
    return {
      summary: report.summary,
      toolCallCount: report.toolCalls?.length ?? 0,
      toolCallName: report.toolCalls?.[0]?.name,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSESSIONTOOLS", Name: "TypeScript Session Tool Calls"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSSESSIONTOOLS", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSSESSIONTOOLS", "context-session-tools", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed session tool workflow", result)
	}
	var output map[string]any
	if err := json.Unmarshal(result.Run.Result, &output); err != nil {
		t.Fatalf("decode result %s: %v", result.Run.Result, err)
	}
	if output["summary"] != "tool call recorded" || output["toolCallCount"] != float64(1) || output["toolCallName"] != "lookup_order_status" {
		t.Fatalf("output = %+v, want tool-call receipt returned to TypeScript", output)
	}
	events, err := st.RunEvents().List(ctx, "TSSESSIONTOOLS", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if countWorkflowEvents(events, "agent_session_operation") != 1 || countWorkflowEvents(events, "agent_session_tool_call") != 1 {
		t.Fatalf("events = %+v, want one session operation and one session tool call event", events)
	}
	operationEvent := workflowEventDataByOperation(t, events, "prompt")
	toolEvent := workflowEventDataByType(t, events, "agent_session_tool_call")
	if toolEvent["workflow_run_id"] != run.RunID ||
		toolEvent["agent_id"] != "tool-worker" ||
		toolEvent["session_id"] == "" ||
		toolEvent["operation_id"] != operationEvent["operation_id"] ||
		toolEvent["call_id"] != "call-lookup-1" ||
		toolEvent["provider_call_id"] != "provider-call-lookup-1" ||
		toolEvent["name"] != "lookup_order_status" ||
		toolEvent["tool_name"] != "lookup_order_status" ||
		toolEvent["status"] != "completed" ||
		toolEvent["authorization_status"] != "authorized" ||
		toolEvent["idempotency_key"] != "idem-lookup-1" ||
		toolEvent["tool_version"] != "v1" ||
		toolEvent["source_hash"] != "sha256:lookup" ||
		toolEvent["handler"] != "typescript" ||
		toolEvent["runtime"] != "local" ||
		toolEvent["timeout"] != "2s" ||
		toolEvent["cancellable"] != true ||
		toolEvent["read_only"] != true ||
		toolEvent["redacted"] != false {
		t.Fatalf("tool event = %+v, want normalized session tool-call evidence", toolEvent)
	}
	args, ok := toolEvent["args"].(map[string]any)
	if !ok || args["orderId"] != "order_1042" {
		t.Fatalf("tool event args = %+v, want model-selected args", toolEvent["args"])
	}
	toolResult, ok := toolEvent["result"].(map[string]any)
	if !ok || toolResult["status"] != "packed" {
		t.Fatalf("tool event result = %+v, want tool result evidence", toolEvent["result"])
	}
}

func TestCodeDefinedWorkflowContextInitializesAgentProfileSession(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workflowPath := filepath.Join(root, ".loom", "workflows", "context-profile-session.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { createAgent, defineAgentProfile, defineWorkflow } from '@loom/runtime';

const reviewer = defineAgentProfile({
  name: 'review-specialist',
  model: 'test/review',
  backend: 'codex',
  instructions: 'Review implementation evidence.',
});

const worker = createAgent(reviewer, {
  name: 'review-worker',
  model: 'test/worker',
});

export default defineWorkflow({
  name: 'context-profile-session',
  async run(ctx) {
    const harness = await ctx.init(worker, {
      agentId: 'review-service',
      name: 'profile-review',
    });
    const session = await harness.session('audit', {
      taskId: 'TASK-PROFILE',
      phase: 'review',
    });
    const report = await session.prompt({
      instruction: 'Review the profile-scoped task.',
      mockResult: { summary: 'profile preserved' },
    });
    return {
      agentId: session.agentId,
      profileName: harness.profileName,
      summary: report.summary,
      operationProfileName: report.profileName,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSPROFILECTX", Name: "TypeScript Profile Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSPROFILECTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSPROFILECTX", "context-profile-session", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed profile session workflow", result)
	}
	var output map[string]any
	if err := json.Unmarshal(result.Run.Result, &output); err != nil {
		t.Fatalf("decode result %s: %v", result.Run.Result, err)
	}
	if output["agentId"] != "review-service" || output["profileName"] != "review-specialist" ||
		output["operationProfileName"] != "review-specialist" || output["summary"] != "profile preserved" {
		t.Fatalf("output = %+v, want reusable profile identity preserved", output)
	}
	sessions, err := st.AgentSessions().List(ctx, "TSPROFILECTX", store.AgentSessionFilter{AgentID: "review-service", TaskID: "TASK-PROFILE"})
	if err != nil {
		t.Fatalf("list profile sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Metadata["profile_name"] != "review-specialist" ||
		sessions[0].Metadata["source_agent_profile"] != "review-specialist" ||
		sessions[0].Metadata["source_agent_name"] != "review-worker" {
		t.Fatalf("sessions = %+v, want durable profile identity metadata", sessions)
	}
	events, err := st.RunEvents().List(ctx, "TSPROFILECTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	initEvent := workflowEventDataByType(t, events, "agent_session_initialized")
	if initEvent["profile_name"] != "review-specialist" || initEvent["source_agent_profile"] != "review-specialist" {
		t.Fatalf("init event = %+v, want profile identity evidence", initEvent)
	}
	promptEvent := workflowEventDataByOperation(t, events, "prompt")
	if promptEvent["profileName"] != "review-specialist" {
		t.Fatalf("prompt event = %+v, want operation profile identity", promptEvent)
	}
}

func TestCodeDefinedWorkflowContextReadsRuntimeProfile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runtimePath := filepath.Join(root, ".loom", "runtimes", "local-node.ts")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.WriteFile(runtimePath, []byte(`import { runtime } from '@loom/runtime';

export default runtime.local({
  name: 'local-node',
  image: 'node:22',
  workspace: {
    providerWorkspaceId: 'local-dev-workspace',
    owner: 'loom',
    cleanup: { mode: 'after_ttl', ttl: '24h' },
    filesystem: { persistence: 'durable', retention: '7d' },
  },
  capabilities: {
    filesystem: { read: true, write: true, artifactURI: true },
    shell: { enabled: true, commands: ['node', 'npm'] },
    network: { enabled: false, policy: 'disabled' },
    lifecycle: { materialize: true, cleanup: true, release: true, cancellation: true, defaultTimeout: '30m' },
  },
  repos: ['slack-src'],
  env: ['NODE_ENV'],
});
`), 0o644); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	workflowPath := filepath.Join(root, ".loom", "workflows", "runtime-context.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'runtime-context',
  runtimeProfile: 'local-node',
  async run(ctx) {
    const profile = await ctx.runtime.profile();
    return {
      name: profile?.name,
      provider: profile?.provider,
      repos: profile?.repos ?? [],
      env: profile?.env ?? [],
      providerWorkspaceId: profile?.workspace?.providerWorkspaceId,
      owner: profile?.workspace?.owner,
      cleanupMode: profile?.workspace?.cleanup?.mode,
      cleanupTTL: profile?.workspace?.cleanup?.ttl,
      filesystemPersistence: profile?.workspace?.filesystem?.persistence,
      filesystemRetention: profile?.workspace?.filesystem?.retention,
      filesystemRead: profile?.capabilities?.filesystem?.read,
      filesystemWrite: profile?.capabilities?.filesystem?.write,
      shellEnabled: profile?.capabilities?.shell?.enabled,
      shellCommands: profile?.capabilities?.shell?.commands ?? [],
      networkEnabled: profile?.capabilities?.network?.enabled,
      networkPolicy: profile?.capabilities?.network?.policy,
      lifecycleTimeout: profile?.capabilities?.lifecycle?.default_timeout,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSRUNTIMECTX", Name: "TypeScript Runtime Context"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSRUNTIMECTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSRUNTIMECTX", "runtime-context", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed runtime context workflow", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Run.Result, &data); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if data["name"] != "local-node" || data["provider"] != "local" {
		t.Fatalf("result data = %+v, want runtime profile identity", data)
	}
	if data["providerWorkspaceId"] != "local-dev-workspace" || data["owner"] != "loom" ||
		data["cleanupMode"] != "after_ttl" || data["cleanupTTL"] != "24h" ||
		data["filesystemPersistence"] != "durable" || data["filesystemRetention"] != "7d" {
		t.Fatalf("result data = %+v, want runtime workspace lifecycle metadata", data)
	}
	if data["filesystemRead"] != true || data["filesystemWrite"] != true ||
		data["shellEnabled"] != true || len(stringSliceFromAny(data["shellCommands"])) != 2 ||
		data["networkEnabled"] != false || data["networkPolicy"] != "disabled" ||
		data["lifecycleTimeout"] != "30m" {
		t.Fatalf("result data = %+v, want runtime capability metadata", data)
	}
	events, err := st.RunEvents().List(ctx, "TSRUNTIMECTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "runtime_profile_read") || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want runtime profile read and completion evidence", events)
	}
	runtimeEvent := workflowEventDataByType(t, events, "runtime_profile_read")
	if runtimeEvent["name"] != "local-node" || runtimeEvent["provider"] != "local" || runtimeEvent["found"] != true {
		t.Fatalf("runtime event = %+v, want profile read evidence", runtimeEvent)
	}
	workspaceEvent, ok := runtimeEvent["workspace"].(map[string]any)
	if !ok || workspaceEvent["providerWorkspaceId"] != "local-dev-workspace" || workspaceEvent["owner"] != "loom" {
		t.Fatalf("runtime event workspace = %+v, want lifecycle metadata evidence", runtimeEvent["workspace"])
	}
	capabilitiesEvent, ok := runtimeEvent["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("runtime event capabilities = %+v, want capability metadata evidence", runtimeEvent["capabilities"])
	}
	shellEventCaps, ok := capabilitiesEvent["shell"].(map[string]any)
	if !ok || shellEventCaps["enabled"] != true || len(stringSliceFromAny(shellEventCaps["commands"])) != 2 {
		t.Fatalf("runtime event shell capabilities = %+v, want shell capability evidence", capabilitiesEvent["shell"])
	}
	networkEventCaps, ok := capabilitiesEvent["network"].(map[string]any)
	if !ok || networkEventCaps["enabled"] != false || networkEventCaps["policy"] != "disabled" {
		t.Fatalf("runtime event network capabilities = %+v, want network capability evidence", capabilitiesEvent["network"])
	}
}

func TestCodeDefinedWorkflowContextReadsRuntimeWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runtimePath := filepath.Join(root, ".loom", "runtimes", "local-node.ts")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.WriteFile(runtimePath, []byte(`import { runtime } from '@loom/runtime';

export default runtime.local({
  name: 'local-node',
  image: 'node:22',
  workspace: {
    providerWorkspaceId: 'local-runtime-workspace',
    owner: 'external',
    cleanup: { mode: 'provider_default' },
    filesystem: { persistence: 'session', retention: '1d' },
  },
  repos: ['slack-src'],
  env: ['NODE_ENV'],
});
`), 0o644); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	workflowPath := filepath.Join(root, ".loom", "workflows", "runtime-workspace.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'runtime-workspace',
  runtimeProfile: 'local-node',
  env: ['WORKFLOW_FLAG'],
  async run(ctx) {
    const workspace = await ctx.runtime.workspace();
    return {
      key: workspace.key,
      directKey: ctx.workspace.key,
      workspaceName: workspace.name,
      workflowName: workspace.workflow?.name,
      profile: workspace.runtime?.profileName,
      provider: workspace.runtime?.provider,
      repoNames: (workspace.repos ?? []).map((repo) => repo.name),
      repoFound: (workspace.repos ?? []).map((repo) => repo.found),
      selectedRepos: workspace.selectedRepos ?? [],
      workflowEnv: workspace.env ?? [],
      runtimeEnv: workspace.runtime?.env ?? [],
      providerWorkspaceId: workspace.runtime?.providerWorkspaceId,
      owner: workspace.runtime?.owner,
      cleanupMode: workspace.runtime?.cleanup?.mode,
      filesystemPersistence: workspace.runtime?.filesystem?.persistence,
      filesystemRetention: workspace.runtime?.filesystem?.retention,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSWORKSPACECTX", Name: "Runtime Workspace", DefaultBranch: "main"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "TSWORKSPACECTX", Name: "slack-src", SourceRepoID: "slack-service", DefaultBranch: "main", Groups: []string{"app"}}); err != nil {
		t.Fatalf("create selected repo: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "TSWORKSPACECTX", Name: "docs", SourceRepoID: "docs"}); err != nil {
		t.Fatalf("create unselected repo: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSWORKSPACECTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSWORKSPACECTX", "runtime-workspace", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed runtime workspace workflow", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Run.Result, &data); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if data["key"] != "TSWORKSPACECTX" || data["directKey"] != "TSWORKSPACECTX" || data["workspaceName"] != "Runtime Workspace" {
		t.Fatalf("result data = %+v, want workspace identity", data)
	}
	if data["workflowName"] != "runtime-workspace" || data["profile"] != "local-node" || data["provider"] != "local" {
		t.Fatalf("result data = %+v, want workflow/runtime identity", data)
	}
	if got := stringSliceFromAny(data["repoNames"]); len(got) != 1 || got[0] != "slack-src" {
		t.Fatalf("repoNames = %+v, want selected runtime repo only", got)
	}
	if got := boolSliceFromAny(data["repoFound"]); len(got) != 1 || !got[0] {
		t.Fatalf("repoFound = %+v, want selected repo found", got)
	}
	if got := stringSliceFromAny(data["selectedRepos"]); len(got) != 1 || got[0] != "slack-src" {
		t.Fatalf("selectedRepos = %+v, want runtime profile selected repo", got)
	}
	if got := stringSliceFromAny(data["workflowEnv"]); len(got) != 1 || got[0] != "WORKFLOW_FLAG" {
		t.Fatalf("workflowEnv = %+v, want workflow env names without values", got)
	}
	if got := stringSliceFromAny(data["runtimeEnv"]); len(got) != 1 || got[0] != "NODE_ENV" {
		t.Fatalf("runtimeEnv = %+v, want runtime profile env names", got)
	}
	if data["providerWorkspaceId"] != "local-runtime-workspace" || data["owner"] != "external" ||
		data["cleanupMode"] != "provider_default" || data["filesystemPersistence"] != "session" ||
		data["filesystemRetention"] != "1d" {
		t.Fatalf("result data = %+v, want workspace runtime lifecycle metadata", data)
	}
	events, err := st.RunEvents().List(ctx, "TSWORKSPACECTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "runtime_workspace_read") || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want runtime workspace read and completion evidence", events)
	}
	workspaceEvent := workflowEventDataByType(t, events, "runtime_workspace_read")
	if workspaceEvent["key"] != "TSWORKSPACECTX" || workspaceEvent["runtimeProfileName"] != "local-node" || workspaceEvent["repoCount"] != float64(1) {
		t.Fatalf("workspace event = %+v, want workspace read evidence", workspaceEvent)
	}
	if workspaceEvent["providerWorkspaceId"] != "local-runtime-workspace" || workspaceEvent["owner"] != "external" {
		t.Fatalf("workspace event = %+v, want runtime lifecycle metadata evidence", workspaceEvent)
	}
}

func TestCodeDefinedWorkflowContextAdmitsRuntimeWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runtimePath := filepath.Join(root, ".loom", "runtimes", "local-node.ts")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.WriteFile(runtimePath, []byte(`import { runtime } from '@loom/runtime';

export default runtime.local({
  name: 'local-node',
  cwd: '.',
  workspace: {
    providerWorkspaceId: 'local-lifecycle-workspace',
    owner: 'loom',
    cleanup: { mode: 'after_ttl', ttl: '24h' },
    filesystem: { persistence: 'session', retention: '1d' },
  },
  repos: ['slack-src'],
  env: [],
});
`), 0o644); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	workflowPath := filepath.Join(root, ".loom", "workflows", "runtime-lifecycle.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'runtime-lifecycle',
  runtimeProfile: 'local-node',
  async run(ctx) {
    const materialized = await ctx.runtime.materializeWorkspace({
      reason: 'prepare workflow sandbox',
      metadata: { phase: 'setup' },
    });
    const written = await ctx.runtime.files.writeText('state/summary.txt', 'runtime workspace ready', {
      summary: 'Runtime workspace state',
    });
    const reread = await ctx.runtime.files.readText('state/summary.txt');
    const cleaned = await ctx.runtime.cleanupWorkspace({
      reason: 'workflow finished',
      cleanup: { mode: 'after_ttl', ttl: '1h' },
      metadata: { phase: 'teardown' },
    });
    return {
      materializeAccepted: materialized.accepted,
      cleanupAccepted: cleaned.accepted,
      runtimeProfileName: materialized.runtimeProfileName,
      providerWorkspaceId: materialized.providerWorkspaceId,
      cleanupTTL: cleaned.cleanup?.ttl,
      cleanupEnforced: cleaned.cleanupEnforced,
      cleanedFiles: cleaned.cleanedFiles,
      runtimeFileURI: written.uri,
      runtimeFileRead: reread,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSLIFECYCLECTX", Name: "Runtime Lifecycle"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSLIFECYCLECTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSLIFECYCLECTX", "runtime-lifecycle", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed runtime lifecycle workflow", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Run.Result, &data); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if data["materializeAccepted"] != true || data["cleanupAccepted"] != true ||
		data["runtimeProfileName"] != "local-node" || data["providerWorkspaceId"] != "local-lifecycle-workspace" ||
		data["cleanupTTL"] != "1h" || data["cleanupEnforced"] != true || data["cleanedFiles"] != float64(1) ||
		data["runtimeFileRead"] != "runtime workspace ready" {
		t.Fatalf("result data = %+v, want admitted lifecycle receipts", data)
	}
	if uri, ok := data["runtimeFileURI"].(string); !ok || !strings.HasPrefix(uri, "runtime-workspace://local-lifecycle-workspace/state/summary.txt") {
		t.Fatalf("runtimeFileURI = %#v, want runtime workspace URI without host path", data["runtimeFileURI"])
	}
	if _, err := os.Stat(filepath.Join(root, "state", "summary.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime workspace file exists after cleanup or stat failed with %v, want cleaned local runtime file", err)
	}
	events, err := st.RunEvents().List(ctx, "TSLIFECYCLECTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "runtime_workspace_materialize_requested") ||
		!hasWorkflowEvent(events, "runtime_workspace_cleanup_requested") ||
		!hasWorkflowEvent(events, "runtime_workspace_file_written") ||
		!hasWorkflowEvent(events, "runtime_workspace_file_read") ||
		!hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want lifecycle request and completion evidence", events)
	}
	materializeEvent := workflowEventDataByType(t, events, "runtime_workspace_materialize_requested")
	if materializeEvent["runtime_profile_name"] != "local-node" ||
		materializeEvent["providerWorkspaceId"] != "local-lifecycle-workspace" ||
		materializeEvent["status"] != "admitted" || materializeEvent["source"] != "workflow_context" {
		t.Fatalf("materialize event = %+v, want admitted lifecycle evidence", materializeEvent)
	}
	cleanupEvent := workflowEventDataByType(t, events, "runtime_workspace_cleanup_requested")
	cleanup, ok := cleanupEvent["cleanup"].(map[string]any)
	if !ok || cleanup["mode"] != "after_ttl" || cleanup["ttl"] != "1h" {
		t.Fatalf("cleanup event = %+v, want cleanup policy override evidence", cleanupEvent)
	}
	if cleanupEvent["cleanupEnforced"] != true || cleanupEvent["cleanupScope"] != "current_run_runtime_files" ||
		cleanupEvent["cleanedFiles"] != float64(1) {
		t.Fatalf("cleanup event = %+v, want enforced local cleanup evidence", cleanupEvent)
	}
	fileEvent := workflowEventDataByType(t, events, "runtime_workspace_file_written")
	if fileEvent["providerWorkspaceId"] != "local-lifecycle-workspace" || fileEvent["visibility"] != "runtime_workspace" ||
		fileEvent["source"] != "workflow_context" || fileEvent["providerBacked"] != true {
		t.Fatalf("file event = %+v, want runtime workspace filesystem evidence", fileEvent)
	}
	if uri, ok := fileEvent["uri"].(string); !ok || strings.HasPrefix(uri, "file://") {
		t.Fatalf("file event uri = %#v, want provider-scoped runtime workspace URI", fileEvent["uri"])
	}
	artifacts, err := st.Artifacts().List(ctx, "TSLIFECYCLECTX", store.ArtifactFilter{Type: "runtime_workspace_file"})
	if err != nil {
		t.Fatalf("list runtime workspace artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Metadata["source"] != "runtime_workspace_filesystem" ||
		artifacts[0].URI == "" || strings.HasPrefix(artifacts[0].URI, "file://") {
		t.Fatalf("artifacts = %+v, want runtime workspace file artifact without host URI", artifacts)
	}
}

func TestCodeDefinedWorkflowContextExecutesRemoteRuntimeWorkspaceAdapter(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runtimePath := filepath.Join(root, ".loom", "runtimes", "remote-e2b.ts")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.WriteFile(runtimePath, []byte(`import { runtime } from '@loom/runtime';

export default runtime.remote({
  name: 'remote-e2b',
  provider: 'e2b',
  cwd: '.',
  workspace: {
    providerWorkspaceId: 'e2b-workspace-1',
    owner: 'loom',
    cleanup: { mode: 'after_ttl', ttl: '24h' },
    filesystem: { persistence: 'session', durability: 'provider', retention: '2d' },
  },
  filesystem: { read: true, write: true, artifactURI: true, policy: 'provider_adapter' },
  lifecycle: { materialize: true, cleanup: true, release: true, policy: 'provider_adapter' },
  env: [],
});
`), 0o644); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	workflowPath := filepath.Join(root, ".loom", "workflows", "remote-runtime-lifecycle.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'remote-runtime-lifecycle',
  runtimeProfile: 'remote-e2b',
  async run(ctx) {
    const materialized = await ctx.runtime.materializeWorkspace({
      reason: 'prepare remote provider workspace',
      metadata: { phase: 'setup' },
    });
    const written = await ctx.runtime.files.writeJSON('state/result.json', { ok: true }, {
      summary: 'Remote runtime workspace state',
    });
    const reread = await ctx.runtime.files.readJSON('state/result.json');
    const cleaned = await ctx.runtime.cleanupWorkspace({
      reason: 'workflow finished',
      metadata: { phase: 'teardown' },
    });
    return {
      provider: written.provider,
      providerBacked: written.providerBacked,
      materialized: materialized.materialized,
      materializeProviderBacked: materialized.providerBacked,
      cleanupEnforced: cleaned.cleanupEnforced,
      cleanupProviderBacked: cleaned.providerBacked,
      cleanupScope: cleaned.cleanupScope,
      cleanedFiles: cleaned.cleanedFiles,
      runtimeFileURI: written.uri,
      readOK: reread.ok,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSREMOTECTX", Name: "Remote Runtime"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSREMOTECTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSREMOTECTX", "remote-runtime-lifecycle", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed remote runtime lifecycle workflow", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Run.Result, &data); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if data["provider"] != "e2b" || data["providerBacked"] != true ||
		data["materialized"] != true || data["materializeProviderBacked"] != true ||
		data["cleanupEnforced"] != true || data["cleanupProviderBacked"] != true ||
		data["cleanupScope"] != "current_run_runtime_files" ||
		data["cleanedFiles"] != float64(1) || data["readOK"] != true {
		t.Fatalf("result data = %+v, want provider-backed remote lifecycle execution", data)
	}
	if uri, ok := data["runtimeFileURI"].(string); !ok ||
		!strings.HasPrefix(uri, "runtime-workspace://e2b-workspace-1/state/result.json") ||
		strings.HasPrefix(uri, "file://") {
		t.Fatalf("runtimeFileURI = %#v, want provider-scoped runtime workspace URI", data["runtimeFileURI"])
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSREMOTECTX", "remote-runtime-lifecycle")
	if err != nil {
		t.Fatalf("get workflow definition: %v", err)
	}
	adapterRoot := tsContextRuntimeWorkspaceRoot(tsContextRuntimeProfileForDefinition(ctx, st, def))
	if adapterRoot == "" {
		t.Fatalf("remote runtime adapter root is empty")
	}
	if _, err := os.Stat(filepath.Join(adapterRoot, "state", "result.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote adapter file exists after cleanup or stat failed with %v, want cleaned provider-backed file", err)
	}
	events, err := st.RunEvents().List(ctx, "TSREMOTECTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "runtime_workspace_materialize_requested") ||
		!hasWorkflowEvent(events, "runtime_workspace_cleanup_requested") ||
		!hasWorkflowEvent(events, "runtime_workspace_file_written") ||
		!hasWorkflowEvent(events, "runtime_workspace_file_read") ||
		!hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want remote lifecycle request and completion evidence", events)
	}
	materializeEvent := workflowEventDataByType(t, events, "runtime_workspace_materialize_requested")
	if materializeEvent["provider"] != "e2b" || materializeEvent["providerWorkspaceId"] != "e2b-workspace-1" ||
		materializeEvent["providerBacked"] != true || materializeEvent["materialized"] != true {
		t.Fatalf("materialize event = %+v, want provider-backed remote materialization evidence", materializeEvent)
	}
	cleanupEvent := workflowEventDataByType(t, events, "runtime_workspace_cleanup_requested")
	if cleanupEvent["providerBacked"] != true || cleanupEvent["cleanupEnforced"] != true ||
		cleanupEvent["cleanupScope"] != "current_run_runtime_files" || cleanupEvent["cleanedFiles"] != float64(1) {
		t.Fatalf("cleanup event = %+v, want provider-backed remote cleanup evidence", cleanupEvent)
	}
	fileEvent := workflowEventDataByType(t, events, "runtime_workspace_file_written")
	if fileEvent["provider"] != "e2b" || fileEvent["providerBacked"] != true ||
		fileEvent["providerWorkspaceId"] != "e2b-workspace-1" ||
		fileEvent["visibility"] != "runtime_workspace" ||
		fileEvent["source"] != "workflow_context" {
		t.Fatalf("file event = %+v, want remote runtime workspace filesystem evidence", fileEvent)
	}
	artifacts, err := st.Artifacts().List(ctx, "TSREMOTECTX", store.ArtifactFilter{Type: "runtime_workspace_file"})
	if err != nil {
		t.Fatalf("list runtime workspace artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Metadata["provider_backed"] != "true" ||
		artifacts[0].URI == "" || strings.HasPrefix(artifacts[0].URI, "file://") {
		t.Fatalf("artifacts = %+v, want remote runtime workspace artifact without host URI", artifacts)
	}
}

func TestCodeDefinedWorkflowContextRemoteRuntimeWorkspacePersistsAcrossRunsAndReconcilesCleanup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	providerWorkspaceID := "e2b-persist-" + safeRuntimeWorkspacePart(filepath.Base(filepath.Dir(root)))
	runtimePath := filepath.Join(root, ".loom", "runtimes", "remote-e2b-persist.ts")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.WriteFile(runtimePath, []byte(`import { runtime } from '@loom/runtime';

export default runtime.remote({
  name: 'remote-e2b-persist',
  provider: 'e2b',
  cwd: '.',
  workspace: {
    providerWorkspaceId: '`+providerWorkspaceID+`',
    owner: 'loom',
    cleanup: { mode: 'manual', ttl: '7d' },
    filesystem: { persistence: 'durable', durability: 'provider', retention: '7d' },
  },
  filesystem: { read: true, write: true, artifactURI: true, policy: 'provider_adapter' },
  lifecycle: { materialize: true, cleanup: true, release: true, policy: 'provider_adapter' },
  env: [],
});
`), 0o644); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	workflowPath := filepath.Join(root, ".loom", "workflows", "remote-runtime-persist.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'remote-runtime-persist',
  runtimeProfile: 'remote-e2b-persist',
  async run(ctx) {
    if (ctx.input.mode === 'write') {
      await ctx.runtime.materializeWorkspace({
        reason: 'prepare durable provider workspace',
        metadata: { phase: 'write' },
      });
      const written = await ctx.runtime.files.writeJSON('state/persisted.json', {
        token: 'from-first-run',
        run: 'write',
      }, {
        summary: 'Cross-run provider workspace state',
      });
      return {
        mode: 'write',
        provider: written.provider,
        providerBacked: written.providerBacked,
        uri: written.uri,
      };
    }

    const persisted = await ctx.runtime.files.readJSON('state/persisted.json');
    const cleaned = await ctx.runtime.cleanupWorkspace({
      reason: 'reconcile durable provider workspace',
      reconcile: true,
      metadata: { phase: 'reconcile' },
    });
    return {
      mode: 'reconcile',
      token: persisted.token,
      cleanupScope: cleaned.cleanupScope,
      cleanedFiles: cleaned.cleanedFiles,
      reconcileRequested: cleaned.reconcileRequested,
      reconciled: cleaned.reconciled,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSREMOTEPU", Name: "Remote Runtime Persistence"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSREMOTEPU", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSREMOTEPU", "remote-runtime-persist")
	if err != nil {
		t.Fatalf("get workflow definition: %v", err)
	}
	adapterRoot := tsContextRuntimeWorkspaceRoot(tsContextRuntimeProfileForDefinition(ctx, st, def))
	if adapterRoot == "" {
		t.Fatalf("remote runtime adapter root is empty")
	}
	_ = os.RemoveAll(adapterRoot)
	t.Cleanup(func() { _ = os.RemoveAll(adapterRoot) })

	writeRun, err := CreateOrResumeRun(ctx, st, "TSREMOTEPU", "remote-runtime-persist", json.RawMessage(`{"mode":"write"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun(write) error = %v", err)
	}
	writeResult, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), writeRun)
	if err != nil {
		t.Fatalf("RunOnce(write) error = %v", err)
	}
	if writeResult.Run == nil || writeResult.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("write result = %+v, want completed provider-backed write run", writeResult)
	}
	if _, err := os.Stat(filepath.Join(adapterRoot, "state", "persisted.json")); err != nil {
		t.Fatalf("provider workspace file after write stat error = %v, want persisted file", err)
	}

	reconcileRun, err := CreateOrResumeRun(ctx, st, "TSREMOTEPU", "remote-runtime-persist", json.RawMessage(`{"mode":"reconcile"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun(reconcile) error = %v", err)
	}
	reconcileResult, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), reconcileRun)
	if err != nil {
		t.Fatalf("RunOnce(reconcile) error = %v", err)
	}
	if reconcileResult.Run == nil || reconcileResult.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("reconcile result = %+v, want completed provider-backed cleanup run", reconcileResult)
	}
	var data map[string]any
	if err := json.Unmarshal(reconcileResult.Run.Result, &data); err != nil {
		t.Fatalf("decode reconcile result: %v", err)
	}
	if data["token"] != "from-first-run" || data["cleanupScope"] != "runtime_workspace_reconcile" ||
		data["cleanedFiles"] != float64(1) || data["reconcileRequested"] != true || data["reconciled"] != true {
		t.Fatalf("reconcile result data = %+v, want cross-run read and provider workspace cleanup reconciliation", data)
	}
	if _, err := os.Stat(filepath.Join(adapterRoot, "state", "persisted.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider workspace file exists after reconcile cleanup or stat failed with %v, want removed", err)
	}
	events, err := st.RunEvents().List(ctx, "TSREMOTEPU", store.RunEventFilter{WorkflowRunID: reconcileRun.RunID})
	if err != nil {
		t.Fatalf("list reconcile run events: %v", err)
	}
	if !hasWorkflowEvent(events, "runtime_workspace_file_read") ||
		!hasWorkflowEvent(events, "runtime_workspace_cleanup_requested") ||
		!hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want cross-run read, cleanup, and completion evidence", events)
	}
	cleanupEvent := workflowEventDataByType(t, events, "runtime_workspace_cleanup_requested")
	if cleanupEvent["cleanupScope"] != "runtime_workspace_reconcile" ||
		cleanupEvent["cleanedFiles"] != float64(1) ||
		cleanupEvent["reconcileRequested"] != true ||
		cleanupEvent["reconciled"] != true {
		t.Fatalf("cleanup event = %+v, want provider workspace reconciliation evidence", cleanupEvent)
	}
}

func TestCodeDefinedWorkflowContextDiscoversRuntimeWorkspaceSkills(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	skillDir := filepath.Join(root, ".agents", "skills", "review-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: review-pr
description: Review pull request changes.
compatibility: codex
owner: quality
---

Review the diff, risks, and verification evidence.
`), 0o644); err != nil {
		t.Fatalf("write review skill: %v", err)
	}
	skillDir = filepath.Join(root, ".agents", "skills", "release-note")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir second skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: release-note
description: Draft release notes from merged changes.
---

Summarize user-visible changes.
`), 0o644); err != nil {
		t.Fatalf("write release skill: %v", err)
	}
	runtimePath := filepath.Join(root, ".loom", "runtimes", "local-node.ts")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.WriteFile(runtimePath, []byte(`import { runtime } from '@loom/runtime';

export default runtime.local({
  name: 'local-node',
  cwd: '.',
  workspaceSkillDirs: ['.agents/skills'],
  repos: ['slack-src'],
  env: ['NODE_ENV'],
});
`), 0o644); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	workflowPath := filepath.Join(root, ".loom", "workflows", "runtime-skills.ts")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(workflowPath, []byte(`import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'runtime-skills',
  runtimeProfile: 'local-node',
  async run(ctx) {
    const all = await ctx.skills.list();
    const review = await ctx.skills.get('review-pr');
    const codex = await ctx.skills.list({ compatibility: 'codex' });
    const runtimeSkills = await ctx.runtime.skills();
    return {
      names: all.map((skill) => skill.name),
      sources: all.map((skill) => skill.source),
      directNames: (ctx.workspace.skills ?? []).map((skill) => skill.name),
      reviewDescription: review?.description,
      reviewOwner: review?.metadata?.owner,
      codexCount: codex.length,
      runtimeCount: runtimeSkills.length,
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
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSKILLSCTX", Name: "Runtime Skills"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "TSSKILLSCTX", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "TSSKILLSCTX", "runtime-skills", json.RawMessage(`{}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed runtime skills workflow", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Run.Result, &data); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if got := stringSliceFromAny(data["names"]); len(got) != 2 || got[0] != "release-note" || got[1] != "review-pr" {
		t.Fatalf("names = %+v, want discovered runtime workspace skills", got)
	}
	if got := stringSliceFromAny(data["sources"]); len(got) != 2 || got[0] != "runtime_workspace" || got[1] != "runtime_workspace" {
		t.Fatalf("sources = %+v, want runtime workspace source", got)
	}
	if got := stringSliceFromAny(data["directNames"]); len(got) != 2 || got[0] != "release-note" || got[1] != "review-pr" {
		t.Fatalf("directNames = %+v, want skills projected on ctx.workspace", got)
	}
	if data["reviewDescription"] != "Review pull request changes." || data["reviewOwner"] != "quality" {
		t.Fatalf("result data = %+v, want frontmatter metadata without skill body", data)
	}
	if data["codexCount"] != float64(1) || data["runtimeCount"] != float64(2) {
		t.Fatalf("result data = %+v, want filtered and runtime skill counts", data)
	}
	events, err := st.RunEvents().List(ctx, "TSSKILLSCTX", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if countWorkflowEvents(events, "runtime_workspace_skills_read") != 4 || !hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want runtime workspace skill read evidence and completion", events)
	}
	skillsEvent := workflowEventDataByType(t, events, "runtime_workspace_skills_read")
	if skillsEvent["action"] != "list" || skillsEvent["count"] != float64(2) {
		t.Fatalf("skills event = %+v, want list read evidence", skillsEvent)
	}
	if got := stringSliceFromAny(skillsEvent["names"]); len(got) != 2 || got[0] != "release-note" || got[1] != "review-pr" {
		t.Fatalf("skills event names = %+v, want discovered skill names", got)
	}
}

func TestRuntimeWorkspaceSkillRootsStayInsideProject(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, ".loom", "runtimes", "local-node.ts")
	inside := runtimeWorkspaceSkillRoots(&tsContextRuntimeProfile{
		SourcePath:         sourcePath,
		CWD:                ".",
		WorkspaceSkillDirs: []string{".agents/skills"},
	})
	if len(inside) != 1 || inside[0] != filepath.Join(root, ".agents", "skills") {
		t.Fatalf("inside roots = %+v, want project-local .agents skills root", inside)
	}
	escapedCWD := runtimeWorkspaceSkillRoots(&tsContextRuntimeProfile{
		SourcePath:         sourcePath,
		CWD:                "..",
		WorkspaceSkillDirs: []string{".agents/skills"},
	})
	if len(escapedCWD) != 0 {
		t.Fatalf("escaped cwd roots = %+v, want ignored", escapedCWD)
	}
	absoluteDir := runtimeWorkspaceSkillRoots(&tsContextRuntimeProfile{
		SourcePath:         sourcePath,
		CWD:                ".",
		WorkspaceSkillDirs: []string{filepath.Join(root, ".agents", "skills")},
	})
	if len(absoluteDir) != 0 {
		t.Fatalf("absolute skill roots = %+v, want ignored", absoluteDir)
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
      idempotencyKey: 'claim-dispatch:CLAIM-1',
      providerModel: 'test/dispatch-model',
      metadata: { source: 'claim-projection' },
      message: 'continue from claim projection',
    });
    return {
      claims: claims.length,
      observed: observed.length,
      actor: claim?.claim_actor ?? '',
      dispatched: dispatch.accepted === true,
      dispatchId: dispatch.dispatchId,
      operationId: dispatch.operationId,
      sessionId: dispatch.sessionId,
      taskRunId: dispatch.taskRunId,
      workItemId: dispatch.workItemId,
      status: dispatch.status,
      providerModel: dispatch.providerModel,
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
		Claims        int    `json:"claims"`
		Observed      int    `json:"observed"`
		Actor         string `json:"actor"`
		Dispatched    bool   `json:"dispatched"`
		DispatchID    string `json:"dispatchId"`
		OperationID   string `json:"operationId"`
		SessionID     string `json:"sessionId"`
		TaskRunID     string `json:"taskRunId"`
		WorkItemID    string `json:"workItemId"`
		Status        string `json:"status"`
		ProviderModel string `json:"providerModel"`
	}
	if err := json.Unmarshal(result.Run.Result, &output); err != nil {
		t.Fatalf("decode result %s: %v", result.Run.Result, err)
	}
	if output.Claims != 1 || output.Observed != 1 || output.Actor != "worker-one" || !output.Dispatched ||
		output.DispatchID == "" || output.OperationID != "op:"+output.DispatchID ||
		output.SessionID != "session-one" || output.TaskRunID == "" || output.WorkItemID != "CLAIM-1" ||
		output.Status != "admitted" || output.ProviderModel != "test/dispatch-model" {
		t.Fatalf("output = %+v, want task claim projection and correlated dispatch admission", output)
	}
	events, err := st.RunEvents().List(ctx, "TSCLAIM", store.RunEventFilter{WorkflowRunID: run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasWorkflowEvent(events, "task_claims_observed") || !hasWorkflowEvent(events, "agent_dispatch_admitted") ||
		!hasWorkflowEvent(events, "workflow_completed") {
		t.Fatalf("events = %+v, want task claim observation, dispatch, and completion evidence", events)
	}
	dispatchEvent := workflowEventDataByType(t, events, "agent_dispatch_admitted")
	if dispatchEvent["agent_id"] != "worker-one" ||
		dispatchEvent["dispatchId"] == "" ||
		dispatchEvent["dispatch_id"] != dispatchEvent["dispatchId"] ||
		dispatchEvent["operationId"] != "op:"+dispatchEvent["dispatchId"].(string) ||
		dispatchEvent["session_id"] != "session-one" ||
		dispatchEvent["task_run_id"] == "" ||
		dispatchEvent["work_item_id"] != "CLAIM-1" ||
		dispatchEvent["status"] != "admitted" ||
		dispatchEvent["source"] != "workflow_context" ||
		dispatchEvent["providerModel"] != "test/dispatch-model" {
		t.Fatalf("dispatch event = %+v, want correlated dispatch admission evidence", dispatchEvent)
	}
	dispatchInput, ok := dispatchEvent["input"].(map[string]any)
	if !ok || dispatchInput["idempotencyKey"] != "claim-dispatch:CLAIM-1" ||
		dispatchInput["message"] != "continue from claim projection" {
		t.Fatalf("dispatch input = %+v, want original dispatch payload", dispatchEvent["input"])
	}
	dispatchCorrelation, ok := dispatchEvent["correlation"].(map[string]any)
	if !ok || dispatchCorrelation["workflowRunId"] != run.RunID ||
		dispatchCorrelation["dispatchId"] != dispatchEvent["dispatchId"] ||
		dispatchCorrelation["operationId"] != dispatchEvent["operationId"] ||
		dispatchCorrelation["sessionId"] != "session-one" ||
		dispatchCorrelation["workItemId"] != "CLAIM-1" {
		t.Fatalf("dispatch correlation = %+v, want workflow/session/task linkage", dispatchEvent["correlation"])
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

func stringSliceFromAny(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func boolSliceFromAny(value any) []bool {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]bool, 0, len(raw))
	for _, item := range raw {
		if b, ok := item.(bool); ok {
			out = append(out, b)
		}
	}
	return out
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
