package defs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestInitTypeScriptProjectWritesStartContract(t *testing.T) {
	root := t.TempDir()
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("InitTypeScriptProject() error = %v", err)
	}
	path := filepath.Join(root, ".loom", "start.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read start contract: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"Create a TypeScript-First Loom Agent",
		".loom/agents/<name>.ts",
		"loom connect <name> local",
		"loom run <name> --payload '{}'",
		"loom defs apply --start",
		"never invent or write secret values",
		"Loom/FleetDB durable records remain",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("start contract missing %q:\n%s", want, text)
		}
	}

	custom := []byte("# Custom onboarding\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatalf("write custom start contract: %v", err)
	}
	if err := InitTypeScriptProject(root); err != nil {
		t.Fatalf("second InitTypeScriptProject() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read custom start contract: %v", err)
	}
	if string(got) != string(custom) {
		t.Fatalf("start contract overwritten = %q, want custom preserved", got)
	}
}

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
	if workflow.Runner != "workflow-context-v1" || workflow.Builtin != "" {
		t.Fatalf("workflow runner = %q builtin=%q, want constrained WorkflowContext runner", workflow.Runner, workflow.Builtin)
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
	if runner["context"] != "workflow-context-v1" {
		t.Fatalf("runner capability = %#v, want constrained WorkflowContext runner", runner)
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
	if manifest["runner"] != "workflow-context-v1" {
		t.Fatalf("manifest runner = %v, want workflow-context-v1", manifest["runner"])
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

func TestApplyRejectsWorkflowRouteCollision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/workflows/slack-a.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-a',
  path: '/workflows/slack/run',
  auth: 'workspace',
  builtin: 'run-parent-work-items',
});`)
	writeDefFile(t, root, ".loom/workflows/slack-b.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-b',
  path: '/workflows/slack/run',
  auth: 'workspace',
  builtin: 'run-parent-work-items',
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSCOLLIDE", Name: "TypeScript Collisions"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	err = Apply(ctx, st, "TSCOLLIDE", "test", plan)
	if err == nil || !strings.Contains(err.Error(), "workflow route collision POST /workflows/slack/run") {
		t.Fatalf("Apply() error = %v, want workflow route collision", err)
	}
}

func TestApplyRejectsWorkflowTriggerCollision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/workflows/slack-a.ts", `import { defineWorkflow, trigger } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-a',
  triggers: [trigger.issueLabelAdded({ label: 'ready', type: 'epic' })],
  builtin: 'run-parent-work-items',
});`)
	writeDefFile(t, root, ".loom/workflows/slack-b.ts", `import { defineWorkflow, trigger } from '@loom/runtime';

export default defineWorkflow({
  name: 'slack-b',
  triggers: [trigger.issueLabelAdded({ type: 'epic', label: 'ready' })],
  builtin: 'run-parent-work-items',
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSTRIGGER", Name: "TypeScript Trigger Collisions"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	err = Apply(ctx, st, "TSTRIGGER", "test", plan)
	if err == nil || !strings.Contains(err.Error(), `workflow trigger collision issue.label_added {"label":"ready","type":"epic"}`) {
		t.Fatalf("Apply() error = %v, want workflow trigger collision", err)
	}
}

func TestTypeScriptFirstSkillImportBundlesMetadata(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	skillPath, err := ScaffoldSkill(root, "triage")
	if err != nil {
		t.Fatalf("ScaffoldSkill() error = %v", err)
	}
	writeDefFile(t, root, ".loom/skills/triage/references/checklist.md", "Check reproduction, root cause, and validation evidence.\n")
	writeDefFile(t, root, ".loom/agents/triage-agent.ts", `import { createAgent, runtime } from '@loom/runtime';
import triageSkill from '../skills/triage/SKILL.md' with { type: 'skill' };

export default createAgent({
  name: 'triage-agent',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({ repos: ['.'], env: [] }),
  instructions: 'Use imported skills when they match the requested work.',
  skills: [triageSkill],
  tools: [],
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=1 workflows=0 runtimes=0 skills=1" {
		t.Fatalf("Summary() = %q, want agent plus skill bundle", got)
	}
	if len(plan.Skills) != 1 {
		t.Fatalf("skills = %+v, want one imported skill", plan.Skills)
	}
	skill := plan.Skills[0]
	if skill.Name != "triage" || skill.SourcePath != skillPath {
		t.Fatalf("skill = %+v, want triage from %s", skill, skillPath)
	}
	if !strings.Contains(skill.Instructions, "Before finishing") {
		t.Fatalf("skill instructions = %q, want scaffolded instructions", skill.Instructions)
	}
	if len(skill.Resources) != 1 || skill.Resources[0] != "references/checklist.md" {
		t.Fatalf("skill resources = %+v, want packaged checklist", skill.Resources)
	}
	agent, ok := FindAgent(plan, "triage-agent")
	if !ok {
		t.Fatalf("FindAgent() did not find triage-agent")
	}
	if len(agent.Skills) != 1 || agent.Skills[0] != "triage" {
		t.Fatalf("agent skills = %+v, want imported skill name", agent.Skills)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSSKILL", Name: "TypeScript Skills"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSSKILL", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := st.DefinitionVersions().Get(ctx, "TSSKILL", domain.DefinitionTypeSkill, "triage", skill.Version); err != nil {
		t.Fatalf("skill definition version not created: %v", err)
	}
	role, err := st.Roles().Get(ctx, "TSSKILL", "triage-agent")
	if err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if len(role.Skills) != 1 || role.Skills[0] != "triage" {
		t.Fatalf("role skills = %+v, want imported skill name", role.Skills)
	}
	version, err := st.DefinitionVersions().Get(ctx, "TSSKILL", domain.DefinitionTypeAgent, "triage-agent", agent.Version)
	if err != nil {
		t.Fatalf("agent definition version not created: %v", err)
	}
	capability := jsonMap(t, version.CapabilityManifest)
	model := capability["model"].(map[string]any)
	if model["prompt_bundle_hash"] == "" {
		t.Fatalf("model capability = %#v, want prompt bundle hash", model)
	}
	bundles := model["skill_bundles"].([]any)
	if len(bundles) != 1 {
		t.Fatalf("skill_bundles = %#v, want one bundle", bundles)
	}
	bundle := bundles[0].(map[string]any)
	if bundle["name"] != "triage" || bundle["source_hash"] != skill.SourceHash {
		t.Fatalf("skill bundle = %#v, want triage metadata", bundle)
	}
}

func TestTypeScriptFirstToolDefinitionsBecomeDurableCapabilities(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/tools/create-channel.ts", `import { Type, defineTool } from '@loom/runtime';

export default defineTool({
  name: 'create_channel',
  description: 'Create one channel after workspace policy checks pass.',
  parameters: Type.Object({
    name: Type.String({ description: 'Lowercase channel name' }),
    private: Type.Optional(Type.Boolean()),
  }),
  handler: 'workflow',
  repos: ['slack-src'],
  env: ['SLACK_TOKEN'],
  execute: async ({ name }) => `+"`"+`created ${name}`+"`"+`,
});`)
	writeDefFile(t, root, ".loom/agents/slack-agent.ts", `import { createAgent, runtime } from '@loom/runtime';
import createChannel from '../tools/create-channel';

export default createAgent({
  name: 'slack-agent',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({ repos: ['slack-src'], env: ['SLACK_TOKEN'] }),
  instructions: 'Use approved tools for Slack workspace actions.',
  tools: [createChannel],
  policy: {
    allowedCommands: ['npm test'],
  },
});`)
	writeDefFile(t, root, ".loom/workflows/provision-channel.ts", `import { defineWorkflow } from '@loom/runtime';
import createChannel from '../tools/create-channel';

export default defineWorkflow({
  name: 'provision-channel',
  description: 'Provision a Slack channel for an approved request.',
  builtin: 'run-parent-work-items',
  tools: [createChannel],
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=1 workflows=1 runtimes=0 tools=1" {
		t.Fatalf("Summary() = %q, want tool in TypeScript plan", got)
	}
	if len(plan.Tools) != 1 {
		t.Fatalf("tools = %+v, want one typed tool definition", plan.Tools)
	}
	tool := plan.Tools[0]
	if tool.Name != "create_channel" || tool.Handler != "workflow" {
		t.Fatalf("tool = %+v, want create_channel with workflow handler", tool)
	}
	if len(tool.Parameters) == 0 || len(tool.Env) != 1 || tool.Env[0] != "SLACK_TOKEN" {
		t.Fatalf("tool metadata = %+v, want parameters and env policy", tool)
	}
	agent, ok := FindAgent(plan, "slack-agent")
	if !ok {
		t.Fatalf("FindAgent() did not find slack-agent")
	}
	if len(agent.Tools) != 1 || agent.Tools[0] != "create_channel" {
		t.Fatalf("agent tools = %+v, want imported typed tool name", agent.Tools)
	}
	workflow, ok := FindWorkflow(plan, "provision-channel")
	if !ok {
		t.Fatalf("FindWorkflow() did not find provision-channel")
	}
	if len(workflow.Tools) != 1 || workflow.Tools[0] != "create_channel" {
		t.Fatalf("workflow tools = %+v, want imported typed tool name", workflow.Tools)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSTOOL", Name: "TypeScript Tools"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSTOOL", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	toolVersion, err := st.DefinitionVersions().Get(ctx, "TSTOOL", domain.DefinitionTypeTool, "create_channel", tool.Version)
	if err != nil {
		t.Fatalf("tool definition version not created: %v", err)
	}
	toolCapability := jsonMap(t, toolVersion.CapabilityManifest)
	execution := toolCapability["execution"].(map[string]any)
	if execution["handler"] != "workflow" {
		t.Fatalf("tool execution capability = %#v, want workflow handler", execution)
	}
	role, err := st.Roles().Get(ctx, "TSTOOL", "slack-agent")
	if err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if strings.Contains(fmtStringSlice(role.AllowedTools), "create_channel") {
		t.Fatalf("role allowed tools = %+v, want typed tool kept out of sandbox command policy", role.AllowedTools)
	}
	if !strings.Contains(fmtStringSlice(role.AllowedTools), "npm test") {
		t.Fatalf("role allowed tools = %+v, want sandbox command policy preserved", role.AllowedTools)
	}
	agentVersion, err := st.DefinitionVersions().Get(ctx, "TSTOOL", domain.DefinitionTypeAgent, "slack-agent", agent.Version)
	if err != nil {
		t.Fatalf("agent definition version not created: %v", err)
	}
	agentCapability := jsonMap(t, agentVersion.CapabilityManifest)
	model := agentCapability["model"].(map[string]any)
	toolDefs := model["tool_definitions"].([]any)
	if len(toolDefs) != 1 || toolDefs[0].(map[string]any)["name"] != "create_channel" {
		t.Fatalf("agent tool_definitions = %#v, want create_channel metadata", toolDefs)
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSTOOL", "provision-channel")
	if err != nil {
		t.Fatalf("workflow definition not created: %v", err)
	}
	workflowCapability := jsonMap(t, def.CapabilityManifest)
	workflowCaps := workflowCapability["workflow"].(map[string]any)
	workflowToolDefs := workflowCaps["tool_definitions"].([]any)
	if len(workflowToolDefs) != 1 || workflowToolDefs[0].(map[string]any)["handler"] != "workflow" {
		t.Fatalf("workflow tool_definitions = %#v, want workflow handler metadata", workflowToolDefs)
	}
}

func TestTypeScriptFirstWorkflowBindsRuntimeProfile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeDefFile(t, root, ".loom/runtimes/local-node.ts", `import { runtime } from '@loom/runtime';

export default runtime.local({
  name: 'local-node',
  image: 'node:22',
  repos: ['slack-src'],
  env: ['NODE_ENV'],
  cpu: '2',
  memory: '4Gi',
});`)
	writeDefFile(t, root, ".loom/workflows/runtime-bound.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'runtime-bound',
  builtin: 'run-parent-work-items',
  runtimeProfile: 'local-node',
  env: ['WORKFLOW_ONLY'],
  repos: ['workflow-src'],
});`)

	plan, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Summary(plan); got != "agents=0 workflows=1 runtimes=1" {
		t.Fatalf("Summary() = %q, want workflow plus runtime profile", got)
	}
	workflow, ok := FindWorkflow(plan, "runtime-bound")
	if !ok {
		t.Fatalf("FindWorkflow() did not find runtime-bound")
	}
	if workflow.RuntimeProfileName != "local-node" {
		t.Fatalf("workflow runtime profile = %q, want local-node", workflow.RuntimeProfileName)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TSRUNTIME", Name: "TypeScript Runtime"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := Apply(ctx, st, "TSRUNTIME", "test", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	def, err := st.WorkflowDefinitions().Get(ctx, "TSRUNTIME", "runtime-bound")
	if err != nil {
		t.Fatalf("workflow definition not created: %v", err)
	}
	if def.RuntimeProfileName != "local-node" {
		t.Fatalf("durable workflow runtime profile = %q, want local-node", def.RuntimeProfileName)
	}
	capability := jsonMap(t, def.CapabilityManifest)
	runtimeCaps := capability["runtime"].(map[string]any)
	if runtimeCaps["profile"] != "local-node" {
		t.Fatalf("runtime capability = %#v, want profile binding", runtimeCaps)
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

func TestLoadRejectsMissingTypedAgentToolDefinition(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/agents/slack-agent.ts", `import { createAgent } from '@loom/runtime';

export default createAgent({
  name: 'slack-agent',
  backend: 'echo',
  model: 'local/echo',
  tools: ['create_channel'],
});`)

	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), `agent model tool "create_channel" must reference a declared typed tool definition`) {
		t.Fatalf("Load() error = %v, want missing typed agent tool validation", err)
	}
}

func TestLoadRejectsMissingTypedWorkflowToolDefinition(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/workflows/provision-channel.ts", `import { defineWorkflow } from '@loom/runtime';

export default defineWorkflow({
  name: 'provision-channel',
  builtin: 'run-parent-work-items',
  tools: ['create_channel'],
});`)

	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), `workflow tool "create_channel" must reference a declared typed tool definition`) {
		t.Fatalf("Load() error = %v, want missing typed workflow tool validation", err)
	}
}

func TestLoadSupportsTypeScriptSyntax(t *testing.T) {
	root := t.TempDir()
	writeDefFile(t, root, ".loom/workflows/typed-epic.ts", `import { defineWorkflow, trigger } from '@loom/runtime';

type Input = { parentId: string };
type ToolName = string;

const workflowTools = ['workItems.readyChildren', 'taskRuns.ensure', 'shell.run'] satisfies ToolName[];
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
	if len(workflow.Tools) != 3 || workflow.Tools[1] != "taskRuns.ensure" || workflow.Tools[2] != "shell.run" {
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
