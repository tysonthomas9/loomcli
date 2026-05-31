package tsfirst

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	backendcaps "github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestRunLocalConnectEchoPersistsTranscript(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=test\n# ignored\nNODE_ENV=test\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "default",
		EnvFile:  envPath,
		Message:  "say hello",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.Agent != "hello-world" || result.Instance != "local" || result.Session != "default" {
		t.Fatalf("result identity = %+v, want hello-world/local/default", result)
	}
	if result.Response != "echo: say hello" {
		t.Fatalf("response = %q, want echo response", result.Response)
	}
	if result.Operation == nil || result.Operation.ID != result.OperationID ||
		result.Operation.Kind != "local_connect_prompt" ||
		result.Operation.Status != "completed" ||
		result.Operation.Text != "echo: say hello" ||
		result.Operation.TranscriptPath != result.TranscriptPath ||
		result.Operation.EventCorrelation["session"] != "default" {
		t.Fatalf("operation = %+v, want local connect result envelope", result.Operation)
	}
	if !strings.Contains(strings.Join(result.Env, ","), "ANTHROPIC_API_KEY") || !strings.Contains(strings.Join(result.Env, ","), "NODE_ENV") {
		t.Fatalf("env allowlist = %+v, want env file keys", result.Env)
	}
	if result.TranscriptPath == "" {
		t.Fatalf("transcript path is empty")
	}
	data, err := os.ReadFile(result.TranscriptPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("transcript lines = %d, want 1: %s", len(lines), data)
	}
	var turn localTurn
	if err := json.Unmarshal([]byte(lines[0]), &turn); err != nil {
		t.Fatalf("parse transcript turn: %v", err)
	}
	if turn.Message != "say hello" || turn.Response != "echo: say hello" || turn.DefinitionVersion == "" || turn.PromptHash == "" {
		t.Fatalf("turn = %+v, want persisted local connect turn", turn)
	}
	if turn.Operation == nil || turn.Operation.ID != turn.OperationID || turn.Operation.PromptHash != turn.PromptHash {
		t.Fatalf("turn operation = %+v promptHash=%q, want persisted operation envelope", turn.Operation, turn.PromptHash)
	}
}

func TestRunLocalConnectIncludesPriorSessionHistory(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}

	if _, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "support",
		Message:  "first",
	}); err != nil {
		t.Fatalf("first runLocalConnect() error = %v", err)
	}
	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "support",
		Message:  "second",
	})
	if err != nil {
		t.Fatalf("second runLocalConnect() error = %v", err)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 2 || turns[0].Message != "first" || turns[1].Message != "second" {
		t.Fatalf("turns = %+v, want two persisted turns in order", turns)
	}
}

func TestRunLocalConnectPersistsFailedOperation(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(failingConnectBackend{})
	root := t.TempDir()
	writeAgent(t, root, "fail-agent", "fail-connect")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "fail-agent",
		Instance: "local",
		Session:  "failure",
		Message:  "please fail",
	})
	if err == nil || !strings.Contains(err.Error(), "backend exploded") {
		t.Fatalf("runLocalConnect() error = %v, want backend failure", err)
	}
	if result.Operation == nil || result.Operation.Status != "failed" ||
		result.Operation.ErrorClass != "backend_error" ||
		!strings.Contains(result.Operation.ErrorMessage, "backend exploded") ||
		result.Operation.TranscriptPath != result.TranscriptPath {
		t.Fatalf("operation = %+v, want failed local connect operation envelope", result.Operation)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].Operation == nil || turns[0].Operation.Status != "failed" ||
		turns[0].ErrorClass != "backend_error" ||
		!strings.Contains(turns[0].ErrorMessage, "backend exploded") ||
		turns[0].PromptHash == "" {
		t.Fatalf("turns = %+v, want persisted failed operation turn", turns)
	}
}

func TestRunLocalConnectLoadsEnvFileForBackend(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(envCheckBackend{})
	root := t.TempDir()
	writeAgent(t, root, "env-agent", "envcheck")
	envPath := filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("LOCAL_CONNECT_TOKEN=\"loaded-from-file\"\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	t.Setenv("LOCAL_CONNECT_TOKEN", "")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "env-agent",
		Instance: "local",
		Session:  "default",
		EnvFile:  envPath,
		Message:  "check env",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.Backend != "envcheck" {
		t.Fatalf("backend = %q, want envcheck", result.Backend)
	}
	if got := os.Getenv("LOCAL_CONNECT_TOKEN"); got != "" {
		t.Fatalf("LOCAL_CONNECT_TOKEN after run = %q, want restored empty value", got)
	}
}

func TestRunInteractiveConnectProcessesPromptLines(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}
	var out bytes.Buffer
	if err := runInteractiveConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "interactive",
	}, strings.NewReader("first\n\nsecond\n/quit\nignored\n"), &out); err != nil {
		t.Fatalf("runInteractiveConnect() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Enter one prompt per line") || !strings.Contains(got, "hello-world: echo: first") || !strings.Contains(got, "hello-world: echo: second") {
		t.Fatalf("interactive output = %q, want prompt loop and two echo responses", got)
	}
	turns, err := readLocalTurns(localTranscriptPath(root, "hello-world", "local", "interactive"))
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 2 || turns[0].Message != "first" || turns[1].Message != "second" {
		t.Fatalf("turns = %+v, want two persisted interactive turns before /quit", turns)
	}
}

func TestScaffoldAndCheckCommands(t *testing.T) {
	root := t.TempDir()
	withTSFirstGlobals(t, func() {
		addDir = root
		addJSON = false
		checkDir = root
		checkJSON = false

		if err := runAddAgent(nil, []string{"helper-agent"}); err != nil {
			t.Fatalf("runAddAgent() error = %v", err)
		}
		if err := runAddWorkflow(nil, []string{"helper-flow"}); err != nil {
			t.Fatalf("runAddWorkflow() error = %v", err)
		}
		if err := runAddSkill(nil, []string{"code-review"}); err != nil {
			t.Fatalf("runAddSkill() error = %v", err)
		}
		if err := runCheck(nil, nil); err != nil {
			t.Fatalf("runCheck() error = %v", err)
		}
	})
	for _, path := range []string{
		filepath.Join(root, ".loom", "agents", "helper-agent.ts"),
		filepath.Join(root, ".loom", "workflows", "helper-flow.ts"),
		filepath.Join(root, ".loom", "skills", "code-review", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected scaffolded file %s: %v", path, err)
		}
	}
}

func TestConnectCommandWrappers(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}
	withTSFirstGlobals(t, func() {
		connectJSON = false
		if err := runOneConnectMessage(connectOptions{
			Dir:      root,
			Agent:    "hello-world",
			Instance: "local",
			Session:  "single",
			Message:  "one",
		}); err != nil {
			t.Fatalf("runOneConnectMessage() error = %v", err)
		}
		if err := runConnectMessages(connectOptions{
			Dir:      root,
			Agent:    "hello-world",
			Instance: "local",
			Session:  "batch",
		}, []string{"first", "second"}); err != nil {
			t.Fatalf("runConnectMessages() error = %v", err)
		}
		if err := runConnectReady(connectOptions{
			Dir:      root,
			Agent:    "hello-world",
			Instance: "local",
			Session:  "ready",
		}, false); err != nil {
			t.Fatalf("runConnectReady() error = %v", err)
		}
	})
	for session, want := range map[string]int{"single": 1, "batch": 2} {
		turns, err := readLocalTurns(localTranscriptPath(root, "hello-world", "local", session))
		if err != nil {
			t.Fatalf("readLocalTurns(%s) error = %v", session, err)
		}
		if len(turns) != want {
			t.Fatalf("session %s turns = %d, want %d", session, len(turns), want)
		}
	}
}

func TestTypeScriptCommandValidationBeforeStoreAccess(t *testing.T) {
	root := t.TempDir()
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	withTSFirstGlobals(t, func() {
		applyDir = root
		runDir = root
		runInput = `{"parentId":`

		if err := runApply(nil, []string{"missing-agent"}); err == nil || !strings.Contains(err.Error(), "missing-agent") {
			t.Fatalf("runApply() error = %v, want missing-agent", err)
		}
		if err := runApplyWorkflow(nil, []string{"missing-flow"}); err == nil || !strings.Contains(err.Error(), "missing-flow") {
			t.Fatalf("runApplyWorkflow() error = %v, want missing-flow", err)
		}
		if err := runTypeScriptWorkflowCommand(nil, []string{"missing-flow"}); err == nil || !strings.Contains(err.Error(), "missing-flow") {
			t.Fatalf("runTypeScriptWorkflowCommand() error = %v, want missing-flow", err)
		}
		if err := runTypeScriptWorkflowCommand(nil, []string{"epic-runner"}); err == nil || !strings.Contains(err.Error(), "--input must be valid JSON") {
			t.Fatalf("runTypeScriptWorkflowCommand() invalid input error = %v", err)
		}
	})
}

func TestParseWorkflowPayloadAlias(t *testing.T) {
	got, err := parseWorkflowPayload("{}", `{"parentId":"EPIC-1"}`)
	if err != nil {
		t.Fatalf("parseWorkflowPayload() error = %v", err)
	}
	if string(got) != `{"parentId":"EPIC-1"}` {
		t.Fatalf("payload = %s, want payload alias value", got)
	}
	if _, err := parseWorkflowPayload("{}", `{"broken":`); err == nil || !strings.Contains(err.Error(), "--payload must be valid JSON") {
		t.Fatalf("invalid payload error = %v, want --payload validation", err)
	}
	if _, err := parseWorkflowPayload(`{"input":true}`, `{"payload":true}`); err == nil || !strings.Contains(err.Error(), "cannot both be set") {
		t.Fatalf("conflicting input/payload error = %v, want conflict", err)
	}
	if runCmd.Flags().Lookup("payload") == nil {
		t.Fatal("loom run missing --payload flag")
	}
}

func TestRunTypeScriptWorkflowAppliesAndRunsWorkflowContext(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path, err := defspkg.ScaffoldWorkflow(root, "epic-runner")
	if err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	workflow, ok := defspkg.FindWorkflow(plan, "epic-runner")
	if !ok {
		t.Fatalf("FindWorkflow() did not find epic-runner in %+v", plan.Workflows)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSRUN", Name: "TypeScript Run"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	ready := backend.IssueData{ID: "TASK-2", Title: "Build composer", Status: "open"}
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{ready}
	ib.BlockedResult = nil
	ib.ListResult = []backend.IssueData{ready}

	input := json.RawMessage(`{"parentId":"EPIC-1","role":"task","maxConcurrency":1}`)
	result, err := runTypeScriptWorkflow(ctx, st, ib, "TSRUN", "test", plan, workflow, input, true, false)
	if err != nil {
		t.Fatalf("runTypeScriptWorkflow() error = %v", err)
	}
	if result.Workflow != "epic-runner" || result.Version != workflow.Version {
		t.Fatalf("result = %+v, want epic-runner@%s", result, workflow.Version)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunWaiting {
		t.Fatalf("run = %+v, want waiting workflow run", result.Run)
	}
	if result.Operation == nil || result.Operation.ID != result.Run.RunID ||
		result.Operation.Kind != "workflow_run" ||
		result.Operation.Status != domain.WorkflowRunWaiting ||
		result.Operation.WorkflowRunID != result.Run.RunID ||
		result.Operation.EventCount == 0 {
		t.Fatalf("operation = %+v, want workflow run result envelope", result.Operation)
	}
	if result.Builtin == nil || len(result.Builtin.TaskRuns) != 1 {
		t.Fatalf("execution = %+v, want one ensured task run", result.Builtin)
	}
	if result.Builtin.DispatchedCount != 1 {
		t.Fatalf("DispatchedCount = %d, want one daemon command dispatch", result.Builtin.DispatchedCount)
	}

	def, err := st.WorkflowDefinitions().Get(ctx, "TSRUN", "epic-runner")
	if err != nil {
		t.Fatalf("workflow definition not applied: %v", err)
	}
	if def.Version != workflow.Version || def.SourceRef != path {
		t.Fatalf("workflow definition = %+v, want TypeScript source %s@%s", def, path, workflow.Version)
	}
	taskRuns, err := st.TaskRuns().List(ctx, "TSRUN", store.TaskRunFilter{WorkflowRunID: result.Run.RunID, WorkItemID: ready.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 || taskRuns[0].Status != domain.TaskRunStarting || taskRuns[0].RoleName != "task" || taskRuns[0].AgentID == "" || taskRuns[0].CommandID == "" {
		t.Fatalf("taskRuns = %+v, want one dispatched starting task role run", taskRuns)
	}
	cmds, err := st.AgentCommands().List(ctx, "TSRUN", store.AgentCommandFilter{TargetAgentID: taskRuns[0].AgentID})
	if err != nil {
		t.Fatalf("list agent commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Payload["workflow_run_id"] != result.Run.RunID || cmds[0].Payload["task_run_id"] != taskRuns[0].TaskRunID {
		t.Fatalf("commands = %+v, want one start command linked to workflow/task run", cmds)
	}
	events, err := st.RunEvents().List(ctx, "TSRUN", store.RunEventFilter{WorkflowRunID: result.Run.RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasRunEvent(events, "workflow_ts_context_started") || !hasRunEvent(events, "workflow_log") || !hasRunEvent(events, "task_run_ensured") || !hasRunEvent(events, "task_run_dispatched") {
		t.Fatalf("events = %+v, want TypeScript WorkflowContext, log, ensure, and dispatch evidence", events)
	}
}

func TestTypeScriptFirstEndToEndPatternConvergesOnDurableGraph(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := defspkg.InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	if _, err := defspkg.ScaffoldAgent(root, "hello-world"); err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	withTSFirstGlobals(t, func() {
		checkDir = root
		checkJSON = false
		if err := runCheck(nil, nil); err != nil {
			t.Fatalf("runCheck() error = %v", err)
		}
	})
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := defspkg.Summary(plan); got != "agents=1 workflows=1 runtimes=0" {
		t.Fatalf("Summary() = %q, want source agent and workflow", got)
	}
	agent, ok := defspkg.FindAgent(plan, "hello-world")
	if !ok {
		t.Fatalf("FindAgent() did not find hello-world")
	}
	workflow, ok := defspkg.FindWorkflow(plan, "epic-runner")
	if !ok {
		t.Fatalf("FindWorkflow() did not find epic-runner")
	}

	connectResult, err := runLocalConnect(ctx, connectOptions{
		Dir:      root,
		Agent:    "hello-world",
		Instance: "local",
		Session:  "e2e",
		Message:  "prove local agent first",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if connectResult.Response != "echo: prove local agent first" || connectResult.TranscriptPath == "" {
		t.Fatalf("connect result = %+v, want local echo proof and transcript", connectResult)
	}
	turns, err := readLocalTurns(connectResult.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].OperationID == "" || turns[0].PromptHash == "" {
		t.Fatalf("turns = %+v, want local proof evidence with operation and prompt hash", turns)
	}

	st := memstore.New()
	const workspace = "TSE2E"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "TypeScript E2E"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, workspace, "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	instance, err := defspkg.ApplyAgentInstance(ctx, st, workspace, agent, "local", true)
	if err != nil {
		t.Fatalf("ApplyAgentInstance() error = %v", err)
	}
	if instance.Name != "local" || instance.RoleName != "hello-world" ||
		instance.State != domain.AgentStateActive || instance.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("instance = %+v, want active/running TypeScript-first agent instance", instance)
	}

	ready := backend.IssueData{ID: "TASK-1", Title: "Build first task", Status: "open"}
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{ready}
	ib.BlockedResult = nil
	ib.ListResult = []backend.IssueData{ready}
	runResult, err := runTypeScriptWorkflow(ctx, st, ib, workspace, "test", plan, workflow, json.RawMessage(`{"parentId":"EPIC-1","role":"task","maxConcurrency":1}`), true, false)
	if err != nil {
		t.Fatalf("runTypeScriptWorkflow() error = %v", err)
	}
	if runResult.Run == nil || runResult.Run.Status != domain.WorkflowRunWaiting {
		t.Fatalf("run result = %+v, want waiting workflow run", runResult)
	}
	if runResult.Builtin == nil || runResult.Builtin.DispatchedCount != 1 {
		t.Fatalf("builtin result = %+v, want one dispatched child task", runResult.Builtin)
	}

	exported, err := defspkg.PlanFromWorkspace(ctx, st, workspace)
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	if exported.Root != "workspace:"+workspace {
		t.Fatalf("exported root = %q, want workspace root", exported.Root)
	}
	if exportedAgent, ok := defspkg.FindAgent(exported, "hello-world"); !ok || exportedAgent.Version != agent.Version {
		t.Fatalf("exported agents = %+v, want hello-world@%s", exported.Agents, agent.Version)
	}
	if exportedWorkflow, ok := defspkg.FindWorkflow(exported, "epic-runner"); !ok || exportedWorkflow.Version != workflow.Version {
		t.Fatalf("exported workflows = %+v, want epic-runner@%s", exported.Workflows, workflow.Version)
	}
	localInstance, ok := findExportedAgentInstance(exported.AgentInstances, "local")
	if !ok || localInstance.RoleName != "hello-world" || localInstance.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("exported agent instances = %+v, want started local instance", exported.AgentInstances)
	}
	if len(exported.WorkflowRuns) != 1 || exported.WorkflowRuns[0].RunID != runResult.Run.RunID {
		t.Fatalf("exported workflow runs = %+v, want run %s", exported.WorkflowRuns, runResult.Run.RunID)
	}
	if len(exported.TaskRuns) != 1 || exported.TaskRuns[0].WorkItemID != ready.ID ||
		exported.TaskRuns[0].WorkflowRunID != runResult.Run.RunID ||
		exported.TaskRuns[0].AgentID == "" || exported.TaskRuns[0].CommandID == "" {
		t.Fatalf("exported task runs = %+v, want dispatched child task run", exported.TaskRuns)
	}
	workerInstance, ok := findExportedAgentInstance(exported.AgentInstances, exported.TaskRuns[0].AgentID)
	if !ok || workerInstance.RoleName != "task" || workerInstance.Parent != "EPIC-1" ||
		workerInstance.Mode != domain.AgentModeEphemeral ||
		workerInstance.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("exported worker instance = %+v present=%t, want dispatched ephemeral worker for task run %+v", workerInstance, ok, exported.TaskRuns[0])
	}
	if len(exported.AgentCommands) < 2 {
		t.Fatalf("exported agent commands = %+v, want start plus task dispatch commands", exported.AgentCommands)
	}
	if !hasExportedRunEvent(exported.RunEvents, "workflow_ts_context_started") ||
		!hasExportedRunEvent(exported.RunEvents, "task_run_ensured") ||
		!hasExportedRunEvent(exported.RunEvents, "task_run_dispatched") {
		t.Fatalf("exported run events = %+v, want workflow context and task dispatch evidence", exported.RunEvents)
	}
}

func TestTypeScriptFirstCommandSurfaceProofLoopConvergesOnDurableGraph(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	const workspace = "TSCMD"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: "TypeScript Command Surface"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	ready := backend.IssueData{ID: "TASK-CMD-1", Title: "Build command path", Status: "open"}
	ib := clitest.NewMockIssueBackend()
	ib.ReadyResult = []backend.IssueData{ready}
	ib.BlockedResult = nil
	ib.ListResult = []backend.IssueData{ready}
	cli.SetDefaultIssueBackend(ib)
	t.Cleanup(cli.ResetDefaultIssueBackend)

	withTSFirstStore(t, st, workspace)
	withTSFirstGlobals(t, func() {
		addDir = root
		checkDir = root
		connectDir = root
		applyDir = root
		runDir = root

		if err := runAddAgent(nil, []string{"hello-world"}); err != nil {
			t.Fatalf("runAddAgent() error = %v", err)
		}
		if err := runAddWorkflow(nil, []string{"epic-runner"}); err != nil {
			t.Fatalf("runAddWorkflow() error = %v", err)
		}
		if err := runCheck(nil, nil); err != nil {
			t.Fatalf("runCheck() error = %v", err)
		}

		connectSession = "command"
		connectMessage = "prove the command surface"
		if err := runConnect(nil, []string{"hello-world", "local"}); err != nil {
			t.Fatalf("runConnect() error = %v", err)
		}

		applyInstance = "local"
		applyStart = true
		if err := runApply(nil, []string{"hello-world"}); err != nil {
			t.Fatalf("runApply() error = %v", err)
		}
		if err := runApplyWorkflow(nil, []string{"epic-runner"}); err != nil {
			t.Fatalf("runApplyWorkflow() error = %v", err)
		}

		runInput = "{}"
		runPayload = `{"parentId":"EPIC-CMD-1","role":"task","maxConcurrency":1}`
		runOnce = true
		runWait = false
		if err := runTypeScriptWorkflowCommand(nil, []string{"epic-runner"}); err != nil {
			t.Fatalf("runTypeScriptWorkflowCommand() error = %v", err)
		}
	})

	turns, err := readLocalTurns(localTranscriptPath(root, "hello-world", "local", "command"))
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].Operation == nil ||
		turns[0].Operation.Status != "completed" ||
		turns[0].Operation.EventCorrelation["agent"] != "hello-world" ||
		turns[0].PromptHash == "" {
		t.Fatalf("turns = %+v, want command connect proof with operation evidence", turns)
	}

	instance, err := st.Agents().Get(ctx, workspace, "local")
	if err != nil {
		t.Fatalf("get applied agent instance: %v", err)
	}
	if instance.RoleName != "hello-world" || instance.State != domain.AgentStateActive || instance.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("instance = %+v, want UI-visible active/running hello-world instance", instance)
	}
	if _, err := st.WorkflowDefinitions().Get(ctx, workspace, "epic-runner"); err != nil {
		t.Fatalf("workflow definition not applied through command surface: %v", err)
	}
	runs, err := st.WorkflowRuns().List(ctx, workspace, store.WorkflowRunFilter{WorkflowName: "epic-runner"})
	if err != nil {
		t.Fatalf("list workflow runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != domain.WorkflowRunWaiting {
		t.Fatalf("runs = %+v, want one waiting command-surface workflow run", runs)
	}
	taskRuns, err := st.TaskRuns().List(ctx, workspace, store.TaskRunFilter{WorkflowRunID: runs[0].RunID, WorkItemID: ready.ID})
	if err != nil {
		t.Fatalf("list task runs: %v", err)
	}
	if len(taskRuns) != 1 || taskRuns[0].AgentID == "" || taskRuns[0].CommandID == "" {
		t.Fatalf("taskRuns = %+v, want command-surface dispatched child task", taskRuns)
	}
	cmds, err := st.AgentCommands().List(ctx, workspace, store.AgentCommandFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) < 2 {
		t.Fatalf("commands = %+v, want local start command and task dispatch command", cmds)
	}
	events, err := st.RunEvents().List(ctx, workspace, store.RunEventFilter{WorkflowRunID: runs[0].RunID})
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if !hasRunEvent(events, "workflow_ts_context_started") ||
		!hasRunEvent(events, "task_run_ensured") ||
		!hasRunEvent(events, "task_run_dispatched") {
		t.Fatalf("events = %+v, want command-surface workflow context and task dispatch evidence", events)
	}
	exported, err := defspkg.PlanFromWorkspace(ctx, st, workspace)
	if err != nil {
		t.Fatalf("PlanFromWorkspace() error = %v", err)
	}
	if _, ok := defspkg.FindAgent(exported, "hello-world"); !ok {
		t.Fatalf("exported agents = %+v, want command-surface hello-world source intent", exported.Agents)
	}
	if _, ok := defspkg.FindWorkflow(exported, "epic-runner"); !ok {
		t.Fatalf("exported workflows = %+v, want command-surface epic-runner source intent", exported.Workflows)
	}
	if len(exported.WorkflowRuns) != 1 || exported.WorkflowRuns[0].RunID != runs[0].RunID {
		t.Fatalf("exported runs = %+v, want command-surface workflow run %s", exported.WorkflowRuns, runs[0].RunID)
	}
}

func TestRunTypeScriptWorkflowCanCreateRunWithoutReconcile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	workflow, ok := defspkg.FindWorkflow(plan, "epic-runner")
	if !ok {
		t.Fatalf("FindWorkflow() did not find epic-runner")
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "NOWAIT", Name: "No Wait"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	input := json.RawMessage(`{"parentId":"EPIC-1"}`)
	result, err := runTypeScriptWorkflow(ctx, st, nil, "NOWAIT", "test", plan, workflow, input, false, false)
	if err != nil {
		t.Fatalf("runTypeScriptWorkflow() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunQueued || result.Builtin != nil {
		t.Fatalf("result = %+v, want queued run without builtin reconcile", result)
	}
	completed := domain.WorkflowRunCompleted
	if _, err := st.WorkflowRuns().Update(ctx, "NOWAIT", result.Run.RunID, store.WorkflowRunUpdate{Status: &completed}); err != nil {
		t.Fatalf("complete workflow run: %v", err)
	}
	waited, err := waitTypeScriptWorkflow(ctx, st, "NOWAIT", result.Run.RunID)
	if err != nil {
		t.Fatalf("waitTypeScriptWorkflow() error = %v", err)
	}
	if waited.RunID != result.Run.RunID {
		t.Fatalf("waited run = %+v, want %s", waited, result.Run.RunID)
	}
}

func TestRunTypeScriptWorkflowRequiresIssueBackendWhenReconciling(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if _, err := defspkg.ScaffoldWorkflow(root, "epic-runner"); err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	workflow, ok := defspkg.FindWorkflow(plan, "epic-runner")
	if !ok {
		t.Fatalf("FindWorkflow() did not find epic-runner")
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "NOIB", Name: "No Issue Backend"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	_, err = runTypeScriptWorkflow(ctx, st, nil, "NOIB", "test", plan, workflow, json.RawMessage(`{"parentId":"EPIC-1"}`), true, false)
	if err == nil || !strings.Contains(err.Error(), "no issue backend") {
		t.Fatalf("runTypeScriptWorkflow() error = %v, want no issue backend", err)
	}
}

func TestParseWorkflowInputAndHelpers(t *testing.T) {
	for _, input := range []string{"", "  ", `{"parentId":"EPIC-1"}`} {
		got, err := parseWorkflowInput(input)
		if err != nil {
			t.Fatalf("parseWorkflowInput(%q) error = %v", input, err)
		}
		if len(got) == 0 {
			t.Fatalf("parseWorkflowInput(%q) returned empty JSON", input)
		}
	}
	if _, err := parseWorkflowInput(`{"broken":`); err == nil {
		t.Fatal("parseWorkflowInput() succeeded for invalid JSON")
	}

	t.Setenv("LOOM_ACTOR", " loom-user ")
	if got := actorName(); got != "loom-user" {
		t.Fatalf("actorName() = %q, want loom-user", got)
	}
	t.Setenv("LOOM_ACTOR", "")
	t.Setenv("USER", "")
	if got := actorName(); got != "loom" {
		t.Fatalf("actorName() = %q, want loom", got)
	}

	if got := importName("code-review-helper"); got != "codeReviewHelperSkill" {
		t.Fatalf("importName() = %q", got)
	}
	if got := safePathSegment(" a/b:c "); got != "a-b-c" {
		t.Fatalf("safePathSegment() = %q", got)
	}
	if got := localWorkDir("/root", defspkg.AgentModule{Repos: []string{"", "repo"}}); got != filepath.Join("/root", "repo") {
		t.Fatalf("localWorkDir(relative) = %q", got)
	}
	if got := localWorkDir("/root", defspkg.AgentModule{Repos: []string{"/tmp/work"}}); got != "/tmp/work" {
		t.Fatalf("localWorkDir(abs) = %q", got)
	}
}

func TestInvokeLocalAgentStreamingBackend(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(streamingBackend{})
	root := t.TempDir()
	writeAgent(t, root, "stream-agent", "streamtest")
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := defspkg.FindAgent(plan, "stream-agent")
	if !ok {
		t.Fatalf("FindAgent() did not find stream-agent")
	}
	got, err := invokeLocalAgent(context.Background(), plan, agent, "prompt", "hello stream", nil, "")
	if err != nil {
		t.Fatalf("invokeLocalAgent() error = %v", err)
	}
	if got.Response != "streamed response" {
		t.Fatalf("invokeLocalAgent() = %+v", got)
	}
}

func TestInvokeLocalAgentStreamsAndCapturesResponse(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(streamingBackend{})
	root := t.TempDir()
	writeAgent(t, root, "stream-agent", "streamtest")
	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := defspkg.FindAgent(plan, "stream-agent")
	if !ok {
		t.Fatalf("FindAgent() did not find stream-agent")
	}

	var streamed bytes.Buffer
	got, err := invokeLocalAgent(context.Background(), plan, agent, "prompt", "hello stream", &streamed, "")
	if err != nil {
		t.Fatalf("invokeLocalAgent() error = %v", err)
	}
	if got.Response != "streamed response" {
		t.Fatalf("invokeLocalAgent() = %+v", got)
	}
	if streamed.String() != "streamed response\n" {
		t.Fatalf("streamed output = %q", streamed.String())
	}
}

func TestRunLocalConnectCapturesProviderSessionAndUsage(t *testing.T) {
	cli.TestingResetBackendState(t)
	backend := &providerSessionBackend{}
	cli.RegisterBackend(backend)
	root := t.TempDir()
	writeAgent(t, root, "provider-agent", "provider-session")

	first, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "provider-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "first",
	})
	if err != nil {
		t.Fatalf("first runLocalConnect() error = %v", err)
	}
	if first.Response != "hello from provider" || first.ProviderSessionID != "provider-session-1" || first.ProviderModel != "provider/model" {
		t.Fatalf("first result = %+v, want provider metadata", first)
	}
	if first.OperationID == "" || first.DurationMS < 0 {
		t.Fatalf("first result operation fields = %+v", first)
	}
	if first.Usage == nil || first.Usage.TotalTokens != 15 {
		t.Fatalf("first usage = %+v, want total 15", first.Usage)
	}

	second, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "provider-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "second",
	})
	if err != nil {
		t.Fatalf("second runLocalConnect() error = %v", err)
	}
	if got := backend.ResumeIDs(); len(got) != 1 || got[0] != "provider-session-1" {
		t.Fatalf("resume IDs = %+v, want provider-session-1", got)
	}
	if second.ProviderSessionID != "provider-session-2" {
		t.Fatalf("second provider session = %q", second.ProviderSessionID)
	}
	if second.Resume == nil || second.Resume.Status != connectResumeApplied ||
		second.Resume.Method != connectResumeMethodSetter ||
		second.Resume.PriorProviderSessionID != "provider-session-1" {
		t.Fatalf("second resume = %+v, want setter-applied provider-session-1", second.Resume)
	}
	turns, err := readLocalTurns(second.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 2 || turns[0].ProviderSessionID != "provider-session-1" || turns[1].ProviderSessionID != "provider-session-2" {
		t.Fatalf("turns = %+v, want provider session metadata persisted", turns)
	}
	if turns[1].Resume == nil || turns[1].Resume.Status != connectResumeApplied || turns[1].Resume.Method != connectResumeMethodSetter {
		t.Fatalf("turn[1] resume = %+v, want setter-applied resume evidence", turns[1].Resume)
	}
	if turns[0].OperationID == "" || turns[0].Usage == nil || turns[0].Usage.TotalTokens != 15 {
		t.Fatalf("turn[0] metadata = %+v, want operation and usage metadata", turns[0])
	}
}

func TestRunLocalConnectCapturesBackendSessionMetadataFallback(t *testing.T) {
	cli.TestingResetBackendState(t)
	backend := &sessionMetadataBackend{}
	cli.RegisterBackend(backend)
	root := t.TempDir()
	writeAgent(t, root, "session-agent", "session-metadata")

	first, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "session-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "first",
	})
	if err != nil {
		t.Fatalf("first runLocalConnect() error = %v", err)
	}
	if first.ProviderSessionID != "metadata-session-1" {
		t.Fatalf("first provider session = %q, want metadata-session-1", first.ProviderSessionID)
	}

	second, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "session-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "second",
	})
	if err != nil {
		t.Fatalf("second runLocalConnect() error = %v", err)
	}
	if second.ProviderSessionID != "metadata-session-2" {
		t.Fatalf("second provider session = %q, want metadata-session-2", second.ProviderSessionID)
	}
	if second.ProviderModel != "provider/session-metadata" {
		t.Fatalf("second provider model = %q, want backend-reported provider model", second.ProviderModel)
	}
	if second.ProviderMetadata == nil {
		t.Fatalf("provider metadata is nil, want backend-reported provider metadata")
	}
	backendReported, ok := second.ProviderMetadata["backend_reported"].(map[string]any)
	if !ok || backendReported["provider"] != "session-metadata" || backendReported["provider_session_id"] != "metadata-session-2" {
		t.Fatalf("provider metadata = %#v, want backend-reported provider metadata", second.ProviderMetadata)
	}
	if second.Resume == nil || second.Resume.Status != connectResumeUnsupported ||
		second.Resume.Method != connectResumeMethodNone ||
		second.Resume.PriorProviderSessionID != "metadata-session-1" {
		t.Fatalf("second resume = %+v, want unsupported resume evidence", second.Resume)
	}
	turns, err := readLocalTurns(second.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 2 || turns[0].ProviderSessionID != "metadata-session-1" || turns[1].ProviderSessionID != "metadata-session-2" {
		t.Fatalf("turns = %+v, want backend session metadata persisted", turns)
	}
	if turns[1].Resume == nil || turns[1].Resume.Status != connectResumeUnsupported {
		t.Fatalf("turn[1] resume = %+v, want unsupported resume evidence persisted", turns[1].Resume)
	}
}

func TestRunLocalConnectUsesNativeResumeWithoutSetter(t *testing.T) {
	cli.TestingResetBackendState(t)
	backend := &nativeResumeBackend{}
	cli.RegisterBackend(backend)
	root := t.TempDir()
	writeAgent(t, root, "native-agent", "native-resume")

	first, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "native-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "first",
	})
	if err != nil {
		t.Fatalf("first runLocalConnect() error = %v", err)
	}
	if first.Resume != nil {
		t.Fatalf("first resume = %+v, want nil for first turn", first.Resume)
	}
	if first.ProviderSessionID != "native-session-1" {
		t.Fatalf("first provider session = %q, want native-session-1", first.ProviderSessionID)
	}

	second, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "native-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "second",
	})
	if err != nil {
		t.Fatalf("second runLocalConnect() error = %v", err)
	}
	if got := backend.ResumeIDs(); len(got) != 1 || got[0] != "native-session-1" {
		t.Fatalf("native resume IDs = %+v, want native-session-1", got)
	}
	if second.ProviderSessionID != "native-session-2" {
		t.Fatalf("second provider session = %q, want native-session-2", second.ProviderSessionID)
	}
	if second.Resume == nil || second.Resume.Status != connectResumeApplied ||
		second.Resume.Method != connectResumeMethodNonInteractiveResumed ||
		second.Resume.PriorProviderSessionID != "native-session-1" {
		t.Fatalf("second resume = %+v, want native noninteractive resume evidence", second.Resume)
	}
	turns, err := readLocalTurns(second.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 2 || turns[1].Resume == nil ||
		turns[1].Resume.Method != connectResumeMethodNonInteractiveResumed ||
		turns[1].Resume.PriorProviderSessionID != "native-session-1" {
		t.Fatalf("turns = %+v, want native resume evidence persisted", turns)
	}
}

func TestRunLocalConnectUsesNativeStreamingResumeWithoutSetter(t *testing.T) {
	cli.TestingResetBackendState(t)
	backend := &nativeStreamingResumeBackend{}
	cli.RegisterBackend(backend)
	root := t.TempDir()
	writeAgent(t, root, "native-stream-agent", "native-stream-resume")

	first, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "native-stream-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "first",
	})
	if err != nil {
		t.Fatalf("first runLocalConnect() error = %v", err)
	}
	if first.ProviderSessionID != "native-stream-session-1" {
		t.Fatalf("first provider session = %q, want native-stream-session-1", first.ProviderSessionID)
	}

	second, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "native-stream-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "second",
	})
	if err != nil {
		t.Fatalf("second runLocalConnect() error = %v", err)
	}
	if got := backend.ResumeIDs(); len(got) != 1 || got[0] != "native-stream-session-1" {
		t.Fatalf("native streaming resume IDs = %+v, want native-stream-session-1", got)
	}
	if second.ProviderSessionID != "native-stream-session-2" {
		t.Fatalf("second provider session = %q, want native-stream-session-2", second.ProviderSessionID)
	}
	if second.Resume == nil || second.Resume.Status != connectResumeApplied ||
		second.Resume.Method != connectResumeMethodStreamingResumed ||
		second.Resume.PriorProviderSessionID != "native-stream-session-1" {
		t.Fatalf("second resume = %+v, want native streaming resume evidence", second.Resume)
	}
}

func TestRunLocalConnectPrefersNativeStreamingResumeOverSetter(t *testing.T) {
	cli.TestingResetBackendState(t)
	backend := &dualResumeStreamingBackend{}
	cli.RegisterBackend(backend)
	root := t.TempDir()
	writeAgent(t, root, "dual-stream-agent", "dual-stream-resume")

	if _, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "dual-stream-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "first",
	}); err != nil {
		t.Fatalf("first runLocalConnect() error = %v", err)
	}
	second, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "dual-stream-agent",
		Instance: "local",
		Session:  "provider",
		Message:  "second",
	})
	if err != nil {
		t.Fatalf("second runLocalConnect() error = %v", err)
	}
	if len(backend.SetterIDs()) != 0 {
		t.Fatalf("setter IDs = %+v, want native resume path without setter state", backend.SetterIDs())
	}
	if got := backend.ResumeIDs(); len(got) != 1 || got[0] != "dual-stream-session-1" {
		t.Fatalf("native resume IDs = %+v, want dual-stream-session-1", got)
	}
	if second.Resume == nil || second.Resume.Method != connectResumeMethodStreamingResumed {
		t.Fatalf("second resume = %+v, want native streaming resume method", second.Resume)
	}
}

func TestRunLocalConnectCapturesNonStreamingBackendOutputAndUsage(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(nonStreamingCaptureBackend{})
	root := t.TempDir()
	writeAgent(t, root, "nonstream-agent", "nonstream-capture")

	var streamed bytes.Buffer
	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "nonstream-agent",
		Instance: "local",
		Session:  "capture",
		Message:  "capture stdout",
		Stream:   &streamed,
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if !strings.Contains(result.Response, "final captured answer") || !strings.Contains(result.Response, "assistant progress") {
		t.Fatalf("response = %q, want captured non-streaming backend stdout", result.Response)
	}
	if !strings.Contains(streamed.String(), "final captured answer") {
		t.Fatalf("streamed output = %q, want tee of non-streaming stdout", streamed.String())
	}
	if result.Usage == nil || result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 7 ||
		result.Usage.CacheReadInputTokens != 3 || result.Usage.CacheCreationInputTokens != 2 ||
		result.Usage.TotalTokens != 23 {
		t.Fatalf("usage = %+v, want captured collector totals", result.Usage)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].Response != result.Response || turns[0].Usage == nil || turns[0].Usage.TotalTokens != 23 {
		t.Fatalf("turns = %+v, want persisted non-streaming response and usage", turns)
	}
}

func TestRunLocalConnectCapturesProviderJSONMetadata(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(jsonMetadataBackend{})
	root := t.TempDir()
	writeAgent(t, root, "json-agent", "json-metadata")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "json-agent",
		Instance: "local",
		Session:  "metadata",
		Message:  "capture json",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.ProviderSessionID != "json-session-1" {
		t.Fatalf("provider session = %q, want json-session-1", result.ProviderSessionID)
	}
	if result.ProviderModel != "provider/model-json" {
		t.Fatalf("provider model = %q, want provider/model-json", result.ProviderModel)
	}
	if result.ProviderMetadata == nil {
		t.Fatalf("provider metadata is nil")
	}
	if result.ProviderMetadata["json_event_count"] != 2 {
		t.Fatalf("provider metadata = %#v, want two JSON events", result.ProviderMetadata)
	}
	eventTypes, ok := result.ProviderMetadata["event_types"].([]string)
	if !ok || len(eventTypes) != 2 || eventTypes[0] != "session.created" || eventTypes[1] != "turn.completed" {
		t.Fatalf("event_types = %#v, want session.created and turn.completed", result.ProviderMetadata["event_types"])
	}
	ids, ok := result.ProviderMetadata["ids"].(map[string]string)
	if !ok || ids["session_id"] != "json-session-1" || ids["id"] != "turn-1" {
		t.Fatalf("ids = %#v, want session and turn IDs", result.ProviderMetadata["ids"])
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].ProviderMetadata == nil || turns[0].ProviderMetadata["json_event_count"] == nil {
		t.Fatalf("turns = %+v, want provider metadata persisted", turns)
	}
}

func TestRunLocalConnectEchoCapturesTypedToolRuntimePolicy(t *testing.T) {
	root := t.TempDir()
	writeTypedToolAgent(t, root, "slack-agent", "echo")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "slack-agent",
		Instance: "local",
		Session:  "tools",
		Message:  "create a channel",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.ToolRuntime == nil || result.ToolRuntime.Status != connectToolRuntimeEcho {
		t.Fatalf("tool runtime = %+v, want echo offline policy", result.ToolRuntime)
	}
	if len(result.ToolRuntime.TypedTools) != 1 || result.ToolRuntime.TypedTools[0].Name != "create_channel" || result.ToolRuntime.TypedTools[0].Handler != "workflow" {
		t.Fatalf("typed tools = %+v, want create_channel workflow handler", result.ToolRuntime.TypedTools)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].ToolRuntime == nil || turns[0].ToolRuntime.Status != connectToolRuntimeEcho {
		t.Fatalf("turns = %+v, want typed tool runtime policy persisted", turns)
	}
}

func TestRunLocalConnectRejectsTypedToolsWithoutTypedRuntime(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(nonStreamingCaptureBackend{})
	root := t.TempDir()
	writeTypedToolAgent(t, root, "slack-agent", "nonstream-capture")

	_, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "slack-agent",
		Instance: "local",
		Session:  "tools",
		Message:  "create a channel",
	})
	if err == nil || !strings.Contains(err.Error(), "TypeScript-defined model tools") || !strings.Contains(err.Error(), "create_channel") {
		t.Fatalf("runLocalConnect() error = %v, want typed tool runtime rejection", err)
	}
}

func TestRunLocalConnectPassesTypedToolsToRuntimeBackend(t *testing.T) {
	cli.TestingResetBackendState(t)
	typedBackend := &typedToolRuntimeBackend{toolCalls: []backendcaps.TypedToolCallEvent{{
		CallID:              "call-create-channel",
		Name:                "create_channel",
		Status:              "completed",
		Arguments:           map[string]any{"name": "triage"},
		Result:              map[string]any{"channel_id": "C123"},
		StartedAt:           "2026-05-30T12:00:00Z",
		CompletedAt:         "2026-05-30T12:00:01Z",
		DurationMS:          1000,
		IdempotencyKey:      "tool:create_channel:triage",
		AuthorizationStatus: "authorized",
		Redacted:            true,
	}}}
	cli.RegisterBackend(typedBackend)
	root := t.TempDir()
	writeTypedToolAgent(t, root, "slack-agent", "typed-runtime")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "slack-agent",
		Instance: "local",
		Session:  "tools",
		Message:  "create a channel",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.ToolRuntime == nil || result.ToolRuntime.Status != connectToolRuntimeBackend {
		t.Fatalf("tool runtime = %+v, want backend runtime policy", result.ToolRuntime)
	}
	if len(typedBackend.tools) != 1 ||
		typedBackend.tools[0].Name != "create_channel" ||
		typedBackend.tools[0].Handler != "workflow" ||
		typedBackend.tools[0].Timeout != "30s" ||
		!typedBackend.tools[0].Cancellable {
		t.Fatalf("backend tools = %+v, want create_channel workflow handler", typedBackend.tools)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "create_channel" ||
		result.ToolCalls[0].CallID != "call-create-channel" ||
		result.ToolCalls[0].Status != "completed" ||
		result.ToolCalls[0].IdempotencyKey != "tool:create_channel:triage" ||
		result.ToolCalls[0].AuthorizationStatus != "authorized" ||
		result.ToolCalls[0].Handler != "workflow" ||
		result.ToolCalls[0].Timeout != "30s" ||
		!result.ToolCalls[0].Cancellable ||
		!result.ToolCalls[0].Redacted {
		t.Fatalf("tool calls = %+v, want backend-reported model tool call evidence", result.ToolCalls)
	}
	if !strings.Contains(result.Response, "typed runtime answer") {
		t.Fatalf("response = %q, want typed backend response", result.Response)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || len(turns[0].ToolCalls) != 1 || turns[0].ToolCalls[0].Name != "create_channel" {
		t.Fatalf("turns = %+v, want typed tool call evidence persisted", turns)
	}
}

func TestRunLocalConnectExecutesTrustedTypeScriptToolHandler(t *testing.T) {
	cli.TestingResetBackendState(t)
	typedBackend := &executingTypedToolBackend{
		request: backendcaps.TypedToolExecutionRequest{
			CallID:    "call-create-channel",
			Name:      "create_channel",
			Arguments: map[string]any{"name": "triage"},
		},
	}
	cli.RegisterBackend(typedBackend)
	root := t.TempDir()
	writeTypedToolAgent(t, root, "slack-agent", "executing-typed-runtime")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "slack-agent",
		Instance: "local",
		Session:  "tools",
		Message:  "create a channel",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if result.ToolRuntime == nil ||
		result.ToolRuntime.Status != connectToolRuntimeBackend ||
		result.ToolRuntime.HandlerExecution != connectToolHandlerExecutionConfigured {
		t.Fatalf("tool runtime = %+v, want trusted handler executor configured", result.ToolRuntime)
	}
	if len(result.ToolCalls) != 1 ||
		result.ToolCalls[0].Name != "create_channel" ||
		result.ToolCalls[0].CallID != "call-create-channel" ||
		result.ToolCalls[0].Status != "completed" ||
		result.ToolCalls[0].AuthorizationStatus != "authorized" ||
		result.ToolCalls[0].IdempotencyKey != "local-connect:create_channel:call-create-channel" ||
		result.ToolCalls[0].Handler != "workflow" ||
		result.ToolCalls[0].Timeout != "30s" ||
		result.ToolCalls[0].Result != "created triage" {
		t.Fatalf("tool calls = %+v, want trusted TypeScript handler execution evidence", result.ToolCalls)
	}
	if !strings.Contains(result.Response, "executed typed tool: created triage") {
		t.Fatalf("response = %q, want backend response from trusted tool result", result.Response)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 ||
		turns[0].ToolRuntime == nil ||
		turns[0].ToolRuntime.HandlerExecution != connectToolHandlerExecutionConfigured ||
		len(turns[0].ToolCalls) != 1 ||
		turns[0].ToolCalls[0].Result != "created triage" {
		t.Fatalf("turns = %+v, want trusted handler execution persisted", turns)
	}
}

func TestRunLocalConnectAuthorizesBackendToolCallsAgainstManifest(t *testing.T) {
	cli.TestingResetBackendState(t)
	typedBackend := &typedToolRuntimeBackend{toolCalls: []backendcaps.TypedToolCallEvent{{
		CallID:    "call-create-channel",
		Name:      "create_channel",
		Arguments: map[string]any{"name": "triage"},
		Result:    map[string]any{"channel_id": "C123"},
	}}}
	cli.RegisterBackend(typedBackend)
	root := t.TempDir()
	writeTypedToolAgent(t, root, "slack-agent", "typed-runtime")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "slack-agent",
		Instance: "local",
		Session:  "tools",
		Message:  "create a channel",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if len(result.ToolCalls) != 1 ||
		result.ToolCalls[0].AuthorizationStatus != "authorized" ||
		result.ToolCalls[0].IdempotencyKey != "local-connect:create_channel:call-create-channel" ||
		result.ToolCalls[0].Handler != "workflow" ||
		result.ToolCalls[0].Timeout != "30s" ||
		!result.ToolCalls[0].Cancellable ||
		result.ToolCalls[0].ToolVersion == "" ||
		result.ToolCalls[0].SourceHash == "" {
		t.Fatalf("tool calls = %+v, want manifest-authorized call with deterministic evidence", result.ToolCalls)
	}
}

func TestRunLocalConnectMarksUndeclaredBackendToolCallsDenied(t *testing.T) {
	cli.TestingResetBackendState(t)
	typedBackend := &typedToolRuntimeBackend{toolCalls: []backendcaps.TypedToolCallEvent{{
		CallID: "call-delete-workspace",
		Name:   "delete_workspace",
		Status: "completed",
		Result: map[string]any{"deleted": true},
	}}}
	cli.RegisterBackend(typedBackend)
	root := t.TempDir()
	writeTypedToolAgent(t, root, "slack-agent", "typed-runtime")

	result, err := runLocalConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "slack-agent",
		Instance: "local",
		Session:  "tools",
		Message:  "delete the workspace",
	})
	if err != nil {
		t.Fatalf("runLocalConnect() error = %v", err)
	}
	if len(result.ToolCalls) != 1 ||
		result.ToolCalls[0].Name != "delete_workspace" ||
		result.ToolCalls[0].Status != "failed" ||
		result.ToolCalls[0].AuthorizationStatus != "denied" ||
		result.ToolCalls[0].IdempotencyKey != "local-connect:delete_workspace:call-delete-workspace" ||
		!result.ToolCalls[0].Redacted ||
		!strings.Contains(result.ToolCalls[0].Error, "not declared") {
		t.Fatalf("tool calls = %+v, want undeclared backend tool call denial evidence", result.ToolCalls)
	}
	turns, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || len(turns[0].ToolCalls) != 1 || turns[0].ToolCalls[0].AuthorizationStatus != "denied" {
		t.Fatalf("turns = %+v, want denied typed tool call evidence persisted", turns)
	}
}

func TestRunInteractiveConnectStreamsBackendOutput(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(streamingBackend{})
	root := t.TempDir()
	writeAgent(t, root, "stream-agent", "streamtest")

	var out bytes.Buffer
	if err := runInteractiveConnect(context.Background(), connectOptions{
		Dir:      root,
		Agent:    "stream-agent",
		Instance: "local",
		Session:  "interactive",
	}, strings.NewReader("hello stream\n/quit\n"), &out); err != nil {
		t.Fatalf("runInteractiveConnect() error = %v", err)
	}
	got := out.String()
	if strings.Count(got, "stream-agent: streamed response\n") != 1 {
		t.Fatalf("interactive output = %q, want one streamed response", got)
	}
	turns, err := readLocalTurns(localTranscriptPath(root, "stream-agent", "local", "interactive"))
	if err != nil {
		t.Fatalf("readLocalTurns() error = %v", err)
	}
	if len(turns) != 1 || turns[0].Message != "hello stream" || turns[0].Response != "streamed response" {
		t.Fatalf("turns = %+v, want captured streamed response", turns)
	}
}

type envCheckBackend struct{}

func (envCheckBackend) Name() string { return "envcheck" }

func (envCheckBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (envCheckBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	if got := os.Getenv("LOCAL_CONNECT_TOKEN"); got != "loaded-from-file" {
		return fmt.Errorf("LOCAL_CONNECT_TOKEN = %q, want loaded-from-file", got)
	}
	return nil
}

type failingConnectBackend struct{}

func (failingConnectBackend) Name() string { return "fail-connect" }

func (failingConnectBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (failingConnectBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return fmt.Errorf("backend exploded")
}

type nonStreamingCaptureBackend struct{}

func (nonStreamingCaptureBackend) Name() string { return "nonstream-capture" }

func (nonStreamingCaptureBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (nonStreamingCaptureBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, collector *usage.Collector) error {
	fmt.Println("assistant progress")
	fmt.Println("final captured answer")
	if collector != nil {
		collector.Accumulate("", 11, 7, 3, 2)
	}
	return nil
}

type jsonMetadataBackend struct{}

func (jsonMetadataBackend) Name() string { return "json-metadata" }

func (jsonMetadataBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (jsonMetadataBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, collector *usage.Collector) error {
	fmt.Println(`{"type":"session.created","session_id":"json-session-1","model":"provider/model-json"}`)
	fmt.Println(`{"type":"turn.completed","id":"turn-1","usage":{"input_tokens":13,"output_tokens":8}}`)
	if collector != nil {
		collector.Accumulate("", 13, 8, 0, 0)
	}
	return nil
}

type streamingBackend struct{}

func (streamingBackend) Name() string { return "streamtest" }

func (streamingBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (streamingBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return fmt.Errorf("InvokeNonInteractive should not be called for streaming backend")
}

func (streamingBackend) InvokeStreaming(context.Context, string, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("streamed response\n")), nil
}

type providerSessionBackend struct {
	resumeIDs []string
	count     int
}

func (b *providerSessionBackend) Name() string { return "provider-session" }

func (b *providerSessionBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (b *providerSessionBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return fmt.Errorf("InvokeNonInteractive should not be called for provider session backend")
}

func (b *providerSessionBackend) SetResumeSessionID(id string) {
	b.resumeIDs = append(b.resumeIDs, id)
}

func (b *providerSessionBackend) ResumeIDs() []string {
	out := make([]string, len(b.resumeIDs))
	copy(out, b.resumeIDs)
	return out
}

func (b *providerSessionBackend) InvokeStreaming(context.Context, string, string, string) (io.ReadCloser, error) {
	b.count++
	sessionID := fmt.Sprintf("provider-session-%d", b.count)
	payload := strings.Join([]string{
		fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q}`, sessionID),
		`{"type":"assistant","message":{"model":"provider/model","content":[{"type":"text","text":"hello from provider"}]}}`,
		`{"type":"message_delta","usage":{"input_tokens":10,"output_tokens":5}}`,
		`{"type":"result","result":"hello from provider"}`,
		"",
	}, "\n")
	return io.NopCloser(strings.NewReader(payload)), nil
}

type sessionMetadataBackend struct {
	count  int
	lastID string
}

func (b *sessionMetadataBackend) Name() string { return "session-metadata" }

func (b *sessionMetadataBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (b *sessionMetadataBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	b.count++
	b.lastID = fmt.Sprintf("metadata-session-%d", b.count)
	fmt.Printf("metadata response %d\n", b.count)
	return nil
}

func (b *sessionMetadataBackend) ContinueSession(_, _, _ string) error {
	return fmt.Errorf("ContinueSession should not be called by local one-shot connect")
}

func (b *sessionMetadataBackend) LastSessionID(_ string) string {
	return b.lastID
}

func (b *sessionMetadataBackend) LastProviderMetadata(_ string) map[string]any {
	if b.lastID == "" {
		return nil
	}
	return map[string]any{
		"provider":            "session-metadata",
		"provider_model":      "provider/session-metadata",
		"provider_session_id": b.lastID,
	}
}

type nativeResumeBackend struct {
	count     int
	lastID    string
	resumeIDs []string
}

func (b *nativeResumeBackend) Name() string { return "native-resume" }

func (b *nativeResumeBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (b *nativeResumeBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	b.count++
	b.lastID = fmt.Sprintf("native-session-%d", b.count)
	fmt.Printf(`{"type":"session.created","session_id":%q,"model":"provider/native"}`+"\n", b.lastID)
	fmt.Println("native cold answer")
	return nil
}

func (b *nativeResumeBackend) InvokeNonInteractiveResumed(_, _, _ string, providerSessionID string, _ <-chan struct{}, _ *usage.Collector) error {
	b.resumeIDs = append(b.resumeIDs, providerSessionID)
	b.count++
	b.lastID = fmt.Sprintf("native-session-%d", b.count)
	fmt.Printf(`{"type":"session.created","session_id":%q,"model":"provider/native"}`+"\n", b.lastID)
	fmt.Printf("native resumed from %s\n", providerSessionID)
	return nil
}

func (b *nativeResumeBackend) LastSessionID(_ string) string {
	return b.lastID
}

func (b *nativeResumeBackend) ResumeIDs() []string {
	out := make([]string, len(b.resumeIDs))
	copy(out, b.resumeIDs)
	return out
}

type nativeStreamingResumeBackend struct {
	count     int
	resumeIDs []string
}

func (b *nativeStreamingResumeBackend) Name() string { return "native-stream-resume" }

func (b *nativeStreamingResumeBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (b *nativeStreamingResumeBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return fmt.Errorf("InvokeNonInteractive should not be called for native streaming resume backend")
}

func (b *nativeStreamingResumeBackend) InvokeStreaming(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
	b.count++
	sessionID := fmt.Sprintf("native-stream-session-%d", b.count)
	return io.NopCloser(strings.NewReader(nativeStreamingResumePayload(sessionID, "native streaming cold answer"))), nil
}

func (b *nativeStreamingResumeBackend) InvokeStreamingResumed(_ context.Context, _, _, _, providerSessionID string) (io.ReadCloser, error) {
	b.resumeIDs = append(b.resumeIDs, providerSessionID)
	b.count++
	sessionID := fmt.Sprintf("native-stream-session-%d", b.count)
	return io.NopCloser(strings.NewReader(nativeStreamingResumePayload(sessionID, "native streaming resumed answer"))), nil
}

func (b *nativeStreamingResumeBackend) ResumeIDs() []string {
	out := make([]string, len(b.resumeIDs))
	copy(out, b.resumeIDs)
	return out
}

func nativeStreamingResumePayload(sessionID, answer string) string {
	return strings.Join([]string{
		fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q}`, sessionID),
		fmt.Sprintf(`{"type":"assistant","message":{"model":"provider/native-stream","content":[{"type":"text","text":%q}]}}`, answer),
		fmt.Sprintf(`{"type":"result","result":%q}`, answer),
		"",
	}, "\n")
}

type dualResumeStreamingBackend struct {
	count     int
	resumeIDs []string
	setterIDs []string
}

func (b *dualResumeStreamingBackend) Name() string { return "dual-stream-resume" }

func (b *dualResumeStreamingBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (b *dualResumeStreamingBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return fmt.Errorf("InvokeNonInteractive should not be called for dual streaming resume backend")
}

func (b *dualResumeStreamingBackend) SetResumeSessionID(providerSessionID string) {
	b.setterIDs = append(b.setterIDs, providerSessionID)
}

func (b *dualResumeStreamingBackend) InvokeStreaming(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
	b.count++
	sessionID := fmt.Sprintf("dual-stream-session-%d", b.count)
	return io.NopCloser(strings.NewReader(nativeStreamingResumePayload(sessionID, "dual cold answer"))), nil
}

func (b *dualResumeStreamingBackend) InvokeStreamingResumed(_ context.Context, _, _, _, providerSessionID string) (io.ReadCloser, error) {
	b.resumeIDs = append(b.resumeIDs, providerSessionID)
	b.count++
	sessionID := fmt.Sprintf("dual-stream-session-%d", b.count)
	return io.NopCloser(strings.NewReader(nativeStreamingResumePayload(sessionID, "dual resumed answer"))), nil
}

func (b *dualResumeStreamingBackend) ResumeIDs() []string {
	out := make([]string, len(b.resumeIDs))
	copy(out, b.resumeIDs)
	return out
}

func (b *dualResumeStreamingBackend) SetterIDs() []string {
	out := make([]string, len(b.setterIDs))
	copy(out, b.setterIDs)
	return out
}

type typedToolRuntimeBackend struct {
	tools     []backendcaps.TypedToolDefinition
	toolCalls []backendcaps.TypedToolCallEvent
}

func (b *typedToolRuntimeBackend) Name() string { return "typed-runtime" }

func (b *typedToolRuntimeBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (b *typedToolRuntimeBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	fmt.Println("typed runtime answer")
	return nil
}

func (b *typedToolRuntimeBackend) SetTypedTools(tools []backendcaps.TypedToolDefinition) error {
	b.tools = append([]backendcaps.TypedToolDefinition(nil), tools...)
	return nil
}

func (b *typedToolRuntimeBackend) TypedToolCalls(_ string) []backendcaps.TypedToolCallEvent {
	return append([]backendcaps.TypedToolCallEvent(nil), b.toolCalls...)
}

type executingTypedToolBackend struct {
	tools     []backendcaps.TypedToolDefinition
	executor  backendcaps.TypedToolExecutor
	request   backendcaps.TypedToolExecutionRequest
	toolCalls []backendcaps.TypedToolCallEvent
}

func (b *executingTypedToolBackend) Name() string { return "executing-typed-runtime" }

func (b *executingTypedToolBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (b *executingTypedToolBackend) InvokeNonInteractive(_ string, _ string, _ string, shutdown <-chan struct{}, _ *usage.Collector) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if shutdown != nil {
		go func() {
			<-shutdown
			cancel()
		}()
	}
	event, err := b.executor.ExecuteTypedTool(ctx, b.request)
	if err != nil {
		return err
	}
	b.toolCalls = []backendcaps.TypedToolCallEvent{event}
	fmt.Printf("executed typed tool: %v\n", event.Result)
	return nil
}

func (b *executingTypedToolBackend) SetTypedTools(tools []backendcaps.TypedToolDefinition) error {
	b.tools = append([]backendcaps.TypedToolDefinition(nil), tools...)
	return nil
}

func (b *executingTypedToolBackend) SetTypedToolExecutor(executor backendcaps.TypedToolExecutor) error {
	b.executor = executor
	return nil
}

func (b *executingTypedToolBackend) TypedToolCalls(_ string) []backendcaps.TypedToolCallEvent {
	return append([]backendcaps.TypedToolCallEvent(nil), b.toolCalls...)
}

func withTSFirstGlobals(t *testing.T, fn func()) {
	t.Helper()
	oldAddDir, oldAddJSON := addDir, addJSON
	oldCheckDir, oldCheckJSON := checkDir, checkJSON
	oldConnectDir, oldConnectEnvFile, oldConnectJSON, oldConnectMessage, oldConnectSession := connectDir, connectEnvFile, connectJSON, connectMessage, connectSession
	oldApplyDir, oldApplyInstance, oldApplyStart, oldApplyJSON := applyDir, applyInstance, applyStart, applyJSON
	oldRunDir, oldRunInput, oldRunPayload, oldRunWait, oldRunOnce, oldRunJSON := runDir, runInput, runPayload, runWait, runOnce, runJSON
	t.Cleanup(func() {
		addDir, addJSON = oldAddDir, oldAddJSON
		checkDir, checkJSON = oldCheckDir, oldCheckJSON
		connectDir, connectEnvFile, connectJSON, connectMessage, connectSession = oldConnectDir, oldConnectEnvFile, oldConnectJSON, oldConnectMessage, oldConnectSession
		applyDir, applyInstance, applyStart, applyJSON = oldApplyDir, oldApplyInstance, oldApplyStart, oldApplyJSON
		runDir, runInput, runPayload, runWait, runOnce, runJSON = oldRunDir, oldRunInput, oldRunPayload, oldRunWait, oldRunOnce, oldRunJSON
	})
	fn()
}

func withTSFirstStore(t *testing.T, st store.Store, workspace string) {
	t.Helper()
	old := tsfirstWithActiveWorkspace
	tsfirstWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, workspace)
	}
	t.Cleanup(func() { tsfirstWithActiveWorkspace = old })
}

func writeAgent(t *testing.T, root, name, backend string) {
	t.Helper()
	path := filepath.Join(root, ".loom", "agents", name+".ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	src := fmt.Sprintf(`import { createAgent, runtime } from '@loom/runtime';

export default createAgent({
  name: %q,
  backend: %q,
  model: 'local/test',
  runtime: runtime.local({ repos: ['.'], env: ['LOCAL_CONNECT_TOKEN'] }),
  instructions: 'Check local connect env.',
});
`, name, backend)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
}

func writeTypedToolAgent(t *testing.T, root, name, backend string) {
	t.Helper()
	toolPath := filepath.Join(root, ".loom", "tools", "create-channel.ts")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	toolSrc := `import { Type, defineTool } from '@loom/runtime';

export default defineTool({
  name: 'create_channel',
  description: 'Create one approved channel.',
  parameters: Type.Object({
    name: Type.String({ description: 'Channel name' }),
  }),
  handler: 'workflow',
  timeout: '30s',
  cancellable: true,
  execute: async ({ name }) => ` + "`" + `created ${name}` + "`" + `,
});
`
	if err := os.WriteFile(toolPath, []byte(toolSrc), 0o644); err != nil {
		t.Fatalf("write tool: %v", err)
	}
	agentPath := filepath.Join(root, ".loom", "agents", name+".ts")
	if err := os.MkdirAll(filepath.Dir(agentPath), 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	agentSrc := fmt.Sprintf(`import { createAgent, runtime } from '@loom/runtime';
import createChannel from '../tools/create-channel';

export default createAgent({
  name: %q,
  backend: %q,
  model: 'local/test',
  runtime: runtime.local({ repos: ['.'], env: ['LOCAL_CONNECT_TOKEN'] }),
  instructions: 'Use approved Slack workspace tools.',
  tools: [createChannel],
});
`, name, backend)
	if err := os.WriteFile(agentPath, []byte(agentSrc), 0o644); err != nil {
		t.Fatalf("write typed tool agent: %v", err)
	}
}

func hasRunEvent(events []*domain.RunEvent, typ string) bool {
	for _, event := range events {
		if event != nil && event.Type == typ {
			return true
		}
	}
	return false
}

func hasExportedRunEvent(events []defspkg.RunEventModule, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func findExportedAgentInstance(instances []defspkg.AgentInstanceModule, name string) (defspkg.AgentInstanceModule, bool) {
	for _, instance := range instances {
		if instance.Name == name {
			return instance, true
		}
	}
	return defspkg.AgentInstanceModule{}, false
}
