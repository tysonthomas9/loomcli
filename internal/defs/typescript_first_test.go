package defs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTypeScriptFirstAgentApplyCreatesUIVisibleInstance(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	path, err := ScaffoldAgent(root, "hello-world")
	if err != nil {
		t.Fatalf("ScaffoldAgent() error = %v", err)
	}

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	agent, ok := FindAgent(plan, "hello-world")
	if !ok {
		t.Fatalf("FindAgent() did not find hello-world in %+v", plan.Agents)
	}
	if agent.SourcePath != path || agent.Backend != "echo" || agent.Model != "local/echo" {
		t.Fatalf("agent = %+v, want scaffolded echo/local agent from %s", agent, path)
	}
	if agent.MaxConcurrency != 1 {
		t.Fatalf("MaxConcurrency = %d, want policy maxConcurrency from TypeScript", agent.MaxConcurrency)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSFIRST", Name: "TypeScript First"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSFIRST", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	version, err := st.DefinitionVersions().Get(ctx, "TSFIRST", domain.DefinitionTypeAgent, "hello-world", agent.Version)
	if err != nil {
		t.Fatalf("definition version not created: %v", err)
	}
	capability := jsonMap(t, version.CapabilityManifest)
	if capability["manifest_version"] != "loom.capabilities.v1" {
		t.Fatalf("capability manifest = %#v, want v1 scoped manifest", capability)
	}
	sandbox := capability["sandbox"].(map[string]any)
	if !strings.Contains(fmtStringSlice(sandbox["allowed_commands"]), "git status") {
		t.Fatalf("sandbox capability = %#v, want allowed git status command", sandbox)
	}
	runtime := capability["runtime"].(map[string]any)
	if !strings.Contains(fmtStringSlice(runtime["repos"]), ".") {
		t.Fatalf("runtime capability = %#v, want local repo grant preserved", runtime)
	}
	role, err := st.Roles().Get(ctx, "TSFIRST", "hello-world")
	if err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if role.Backend != "echo" || role.Model != "local/echo" {
		t.Fatalf("role = %+v, want TypeScript backend/model", role)
	}

	instance, err := ApplyAgentInstance(ctx, st, "TSFIRST", agent, "local", true)
	if err != nil {
		t.Fatalf("ApplyAgentInstance() error = %v", err)
	}
	if instance.Name != "local" || instance.RoleName != "hello-world" {
		t.Fatalf("instance = %+v, want local instance for hello-world role", instance)
	}
	if instance.State != domain.AgentStateActive || instance.DesiredState != domain.AgentDesiredRunning {
		t.Fatalf("instance state = %s/%s, want active/running", instance.State, instance.DesiredState)
	}
	cmds, err := st.AgentCommands().List(ctx, "TSFIRST", store.AgentCommandFilter{TargetAgentID: "local"})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Type != "start" || cmds[0].Payload["source"] != "typescript-first" {
		t.Fatalf("commands = %+v, want one queued typescript-first start command", cmds)
	}
}

func TestTypeScriptFirstWorkflowApplyCreatesDurableBindings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	path, err := ScaffoldWorkflow(root, "epic-runner")
	if err != nil {
		t.Fatalf("ScaffoldWorkflow() error = %v", err)
	}
	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=0 workflows=1 runtimes=0" {
		t.Fatalf("Summary() = %q, want one workflow", got)
	}
	workflow := plan.Workflows[0]
	if workflow.SourcePath != path || workflow.Name != "epic-runner" {
		t.Fatalf("workflow = %+v, want scaffolded epic-runner from %s", workflow, path)
	}
	if workflow.Builtin != "run-parent-work-items" {
		t.Fatalf("Builtin = %q, want constrained built-in runner", workflow.Builtin)
	}
	if workflow.RoutePath != "/workflows/epic-runner/run" || workflow.RouteAuth != "workspace" {
		t.Fatalf("route = %q auth=%q, want workspace HTTP route", workflow.RoutePath, workflow.RouteAuth)
	}
	if workflow.TriggerEvent != "issue.label_added" || workflow.TriggerFilter["type"] != "epic" {
		t.Fatalf("trigger = %q filter=%v, want issue label epic trigger", workflow.TriggerEvent, workflow.TriggerFilter)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSWF", Name: "TypeScript Workflow"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSWF", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSWF", "epic-runner")
	if err != nil {
		t.Fatalf("workflow definition not created: %v", err)
	}
	if def.Version != workflow.Version || def.SourceRef != workflow.SourcePath {
		t.Fatalf("definition = %+v, want TypeScript source/version", def)
	}
	capability := jsonMap(t, def.CapabilityManifest)
	if capability["manifest_version"] != "loom.capabilities.v1" {
		t.Fatalf("capability manifest = %#v, want v1 scoped manifest", capability)
	}
	workflowCaps := capability["workflow"].(map[string]any)
	if !strings.Contains(fmtStringSlice(workflowCaps["tools"]), "taskRuns.ensure") {
		t.Fatalf("workflow capabilities = %#v, want taskRuns.ensure grant", workflowCaps)
	}
	runner := capability["runner"].(map[string]any)
	if runner["builtin"] != "run-parent-work-items" {
		t.Fatalf("runner capability = %#v, want constrained builtin runner", runner)
	}
	ingress := capability["ingress"].(map[string]any)
	route := ingress["route"].(map[string]any)
	if route["path"] != "/workflows/epic-runner/run" || route["auth"] != "workspace" {
		t.Fatalf("route capability = %#v, want workspace route", route)
	}
	var manifest map[string]any
	if err := json.Unmarshal(def.Manifest, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if manifest["builtin"] != "run-parent-work-items" {
		t.Fatalf("manifest builtin = %v, want run-parent-work-items", manifest["builtin"])
	}
	routes, err := st.RouteBindings().List(ctx, "TSWF", store.RouteBindingFilter{DefinitionName: "epic-runner"})
	if err != nil {
		t.Fatalf("list route bindings: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/workflows/epic-runner/run" || routes[0].AuthPolicy != "workspace" {
		t.Fatalf("routes = %+v, want one workspace HTTP binding", routes)
	}
	triggers, err := st.TriggerBindings().List(ctx, "TSWF", store.TriggerBindingFilter{WorkflowName: "epic-runner"})
	if err != nil {
		t.Fatalf("list trigger bindings: %v", err)
	}
	if len(triggers) != 1 || triggers[0].EventType != "issue.label_added" || !strings.Contains(string(triggers[0].Filter), `"epic"`) {
		t.Fatalf("triggers = %+v, want one issue label trigger", triggers)
	}
}

func TestLoadRejectsUnauthenticatedWorkflowRoute(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/workflows/unsafe.ts", `export default defineWorkflow({
  name: "unsafe-route",
  builtin: "run-parent-work-items",
  path: "/workflows/unsafe/run",
});`)

	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "must declare an auth policy") {
		t.Fatalf("Load() error = %v, want route auth policy validation", err)
	}
}

func TestLoadSupportsTypeScriptSyntax(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/workflows/typed-epic.ts", `import { defineWorkflow, trigger } from '@loom/runtime';

type Input = { parentId: string };
type ToolName = string;

const workflowTools = ['workItems.readyChildren', 'taskRuns.ensure'] satisfies ToolName[];
const route = {
  path: '/workflows/typed-epic/run',
  auth: 'workspace',
} as const;

export default defineWorkflow({
  name: 'typed-epic',
  builtin: 'run-parent-work-items',
  singleton: (input: Input): string => `+"`"+`parent:${input.parentId}`+"`"+`,
  expose: { http: route },
  triggers: [
    trigger.issueLabelAdded({ label: 'typed-epic', type: 'epic' } as const),
  ],
  tools: workflowTools,
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(plan.Workflows) != 1 {
		t.Fatalf("workflows = %+v, want one typed workflow", plan.Workflows)
	}
	workflow := plan.Workflows[0]
	if workflow.Name != "typed-epic" || workflow.RoutePath != "/workflows/typed-epic/run" {
		t.Fatalf("workflow = %+v, want typed workflow route", workflow)
	}
	if len(workflow.Tools) != 2 || workflow.Tools[1] != "taskRuns.ensure" {
		t.Fatalf("tools = %+v, want typed workflow tools", workflow.Tools)
	}
}

func jsonMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode JSON map: %v\n%s", err, raw)
	}
	return out
}

func fmtStringSlice(value any) string {
	return strings.Trim(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(fmtAny(value)), "\n", " "), "  ", " "), "[]")
}

func fmtAny(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
