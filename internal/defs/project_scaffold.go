package defs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type Plan struct {
	Root                   string                        `json:"root"`
	ModelPolicy            *ModelPolicy                  `json:"model_policy,omitempty"`
	Agents                 []AgentModule                 `json:"agents,omitempty"`
	AgentInstances         []AgentInstanceModule         `json:"agent_instances,omitempty"`
	Nodes                  []NodeModule                  `json:"nodes,omitempty"`
	AgentSessions          []AgentSessionModule          `json:"agent_sessions,omitempty"`
	AgentSessionOperations []AgentSessionOperationModule `json:"agent_session_operations,omitempty"`
	AgentSessionToolCalls  []AgentSessionToolCallModule  `json:"agent_session_tool_calls,omitempty"`
	AgentLeases            []AgentLeaseModule            `json:"agent_leases,omitempty"`
	AgentOwnershipLeases   []AgentOwnershipLeaseModule   `json:"agent_ownership_leases,omitempty"`
	AgentCommands          []AgentCommandModule          `json:"agent_commands,omitempty"`
	TerminalSessions       []TerminalSessionModule       `json:"terminal_sessions,omitempty"`
	Artifacts              []ArtifactModule              `json:"artifacts,omitempty"`
	Workflows              []WorkflowModule              `json:"workflows,omitempty"`
	WorkflowRuns           []WorkflowRunModule           `json:"workflow_runs,omitempty"`
	TaskRuns               []TaskRunModule               `json:"task_runs,omitempty"`
	RunEvents              []RunEventModule              `json:"run_events,omitempty"`
	Runtimes               []RuntimeModule               `json:"runtimes,omitempty"`
	Skills                 []SkillModule                 `json:"skills,omitempty"`
	Tools                  []ToolModule                  `json:"tools,omitempty"`
}

type ModelPolicy struct {
	AllowedModels    []string `json:"allowed_models,omitempty"`
	AllowedProviders []string `json:"allowed_providers,omitempty"`
	AllowUnknown     bool     `json:"allow_unknown,omitempty"`
}

type AgentModule struct {
	Name               string                 `json:"name"`
	Description        string                 `json:"description,omitempty"`
	Backend            string                 `json:"backend,omitempty"`
	Model              string                 `json:"model,omitempty"`
	ProfileName        string                 `json:"profile_name,omitempty"`
	SourcePath         string                 `json:"source_path"`
	SourceHash         string                 `json:"source_hash"`
	Version            string                 `json:"version"`
	Instructions       string                 `json:"instructions,omitempty"`
	Skills             []string               `json:"skills,omitempty"`
	Tools              []string               `json:"tools,omitempty"`
	AllowedCommands    []string               `json:"allowed_commands,omitempty"`
	DeniedCommands     []string               `json:"denied_commands,omitempty"`
	Repos              []string               `json:"repos,omitempty"`
	Env                []string               `json:"env,omitempty"`
	MaxConcurrency     int                    `json:"max_concurrency,omitempty"`
	MaxBudgetUSD       *float64               `json:"max_budget_usd,omitempty"`
	ReadOnly           bool                   `json:"read_only,omitempty"`
	RuntimeProvider    domain.RuntimeProvider `json:"runtime_provider,omitempty"`
	RuntimeProfileName string                 `json:"runtime_profile_name,omitempty"`
	RuntimeCWD         string                 `json:"runtime_cwd,omitempty"`
	RuntimeDaytona     map[string]any         `json:"runtime_daytona,omitempty"`
}

type WorkflowModule struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	SourcePath         string            `json:"source_path"`
	SourceHash         string            `json:"source_hash"`
	Version            string            `json:"version"`
	SingletonPolicy    string            `json:"singleton_policy,omitempty"`
	RuntimeProfileName string            `json:"runtime_profile_name,omitempty"`
	Builtin            string            `json:"builtin,omitempty"`
	Runner             string            `json:"runner,omitempty"`
	RoutePath          string            `json:"route_path,omitempty"`
	RouteAuth          string            `json:"route_auth,omitempty"`
	TriggerEvent       string            `json:"trigger_event,omitempty"`
	TriggerFilter      map[string]string `json:"trigger_filter,omitempty"`
	Tools              []string          `json:"tools,omitempty"`
	Env                []string          `json:"env,omitempty"`
	Repos              []string          `json:"repos,omitempty"`
}

type RuntimeModule struct {
	Name               string                 `json:"name"`
	Version            string                 `json:"version"`
	SourcePath         string                 `json:"source_path"`
	SourceHash         string                 `json:"source_hash"`
	Provider           domain.RuntimeProvider `json:"provider"`
	Image              string                 `json:"image,omitempty"`
	Daytona            map[string]any         `json:"daytona,omitempty"`
	Repos              []string               `json:"repos,omitempty"`
	Env                []string               `json:"env,omitempty"`
	CPU                string                 `json:"cpu,omitempty"`
	Memory             string                 `json:"memory,omitempty"`
	CWD                string                 `json:"cwd,omitempty"`
	WorkspaceSkillDirs []string               `json:"workspace_skill_dirs,omitempty"`
	Workspace          *RuntimeWorkspace      `json:"workspace,omitempty"`
	Capabilities       *RuntimeCapabilities   `json:"capabilities,omitempty"`
}

type RuntimeWorkspace struct {
	ProviderWorkspaceID string                 `json:"provider_workspace_id,omitempty"`
	Owner               string                 `json:"owner,omitempty"`
	Cleanup             *RuntimeCleanupPolicy  `json:"cleanup,omitempty"`
	Filesystem          *RuntimeFilesystemSpec `json:"filesystem,omitempty"`
}

type RuntimeCleanupPolicy struct {
	Mode      string `json:"mode,omitempty"`
	TTL       string `json:"ttl,omitempty"`
	Retention string `json:"retention,omitempty"`
}

type RuntimeFilesystemSpec struct {
	Persistence string `json:"persistence,omitempty"`
	Durability  string `json:"durability,omitempty"`
	Retention   string `json:"retention,omitempty"`
}

type RuntimeCapabilities struct {
	Filesystem *RuntimeFilesystemCapabilities `json:"filesystem,omitempty"`
	Shell      *RuntimeShellCapabilities      `json:"shell,omitempty"`
	Network    *RuntimeNetworkCapabilities    `json:"network,omitempty"`
	Env        *RuntimeEnvCapabilities        `json:"env,omitempty"`
	Workspace  *RuntimeWorkspaceCapabilities  `json:"workspace,omitempty"`
	Lifecycle  *RuntimeLifecycleCapabilities  `json:"lifecycle,omitempty"`
}

type RuntimeFilesystemCapabilities struct {
	Read        *bool  `json:"read,omitempty"`
	Write       *bool  `json:"write,omitempty"`
	ArtifactURI *bool  `json:"artifact_uri,omitempty"`
	Policy      string `json:"policy,omitempty"`
	Persistence string `json:"persistence,omitempty"`
	Durability  string `json:"durability,omitempty"`
	Retention   string `json:"retention,omitempty"`
}

type RuntimeShellCapabilities struct {
	Enabled  *bool    `json:"enabled,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Policy   string   `json:"policy,omitempty"`
}

type RuntimeNetworkCapabilities struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Policy  string `json:"policy,omitempty"`
}

type RuntimeEnvCapabilities struct {
	Forwarded []string `json:"forwarded,omitempty"`
	Policy    string   `json:"policy,omitempty"`
}

type RuntimeWorkspaceCapabilities struct {
	ProviderWorkspaceID string   `json:"provider_workspace_id,omitempty"`
	Owner               string   `json:"owner,omitempty"`
	CWD                 string   `json:"cwd,omitempty"`
	Repos               []string `json:"repos,omitempty"`
	SkillDirs           []string `json:"skill_dirs,omitempty"`
}

type RuntimeLifecycleCapabilities struct {
	Materialize    *bool  `json:"materialize,omitempty"`
	Cleanup        *bool  `json:"cleanup,omitempty"`
	Release        *bool  `json:"release,omitempty"`
	Cancellation   *bool  `json:"cancellation,omitempty"`
	DefaultTimeout string `json:"default_timeout,omitempty"`
	Policy         string `json:"policy,omitempty"`
}

type SkillModule struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Version      string   `json:"version"`
	SourcePath   string   `json:"source_path"`
	SourceHash   string   `json:"source_hash"`
	Instructions string   `json:"instructions,omitempty"`
	Resources    []string `json:"resources,omitempty"`
}

type ToolModule struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     string         `json:"version"`
	SourcePath  string         `json:"source_path"`
	SourceHash  string         `json:"source_hash"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Handler     string         `json:"handler,omitempty"`
	Runtime     string         `json:"runtime,omitempty"`
	Timeout     string         `json:"timeout,omitempty"`
	Cancellable bool           `json:"cancellable,omitempty"`
	Repos       []string       `json:"repos,omitempty"`
	Env         []string       `json:"env,omitempty"`
	ReadOnly    bool           `json:"read_only,omitempty"`
}

type NodeModule struct {
	NodeID          string                 `json:"node_id"`
	SourcePath      string                 `json:"source_path,omitempty"`
	OwnerActor      string                 `json:"owner_actor,omitempty"`
	RuntimeProvider domain.RuntimeProvider `json:"runtime_provider,omitempty"`
	Labels          []string               `json:"labels,omitempty"`
	Capabilities    []string               `json:"capabilities,omitempty"`
	ToolInventory   []string               `json:"tool_inventory,omitempty"`
	Version         string                 `json:"version,omitempty"`
	Capacity        int                    `json:"capacity,omitempty"`
	DrainState      domain.NodeDrainState  `json:"drain_state,omitempty"`
	LastHeartbeat   *time.Time             `json:"last_heartbeat,omitempty"`
	ExpiresAt       *time.Time             `json:"expires_at,omitempty"`
}

var definitionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

var reservedModelToolNames = map[string]bool{
	"bash":  true,
	"edit":  true,
	"glob":  true,
	"grep":  true,
	"read":  true,
	"task":  true,
	"write": true,
}

func InitTypeScriptProject(root string) error {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	dirs := []string{
		filepath.Join(root, ".loom", "agents"),
		filepath.Join(root, ".loom", "connectors"),
		filepath.Join(root, ".loom", "workflows"),
		filepath.Join(root, ".loom", "runtimes"),
		filepath.Join(root, ".loom", "tools"),
		filepath.Join(root, ".loom", "skills"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := writeIfMissing(filepath.Join(root, "loom.config.ts"), []byte(defaultConfig)); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(root, ".loom", "start.md"), []byte(startContract)); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(root, ".loom", "runtime.d.ts"), []byte(runtimeTypes)); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(root, ".loom", "connectors", "daytona.ts"), []byte(daytonaConnector)); err != nil {
		return err
	}
	return nil
}

func ScaffoldAgent(root, name string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	name = strings.TrimSpace(name)
	if !definitionNamePattern.MatchString(name) {
		return "", fmt.Errorf("agent name %q must be lower-kebab-case", name)
	}
	if err := InitTypeScriptProject(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, ".loom", "agents", name+".ts")
	if err := writeIfMissing(path, []byte(agentTemplate(name))); err != nil {
		return "", err
	}
	return path, nil
}

func ScaffoldWorkflow(root, name string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	name = strings.TrimSpace(name)
	if !definitionNamePattern.MatchString(name) {
		return "", fmt.Errorf("workflow name %q must be lower-kebab-case", name)
	}
	if err := InitTypeScriptProject(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, ".loom", "workflows", name+".ts")
	if err := writeIfMissing(path, []byte(workflowTemplate(name))); err != nil {
		return "", err
	}
	return path, nil
}

func ScaffoldSkill(root, name string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	name = strings.TrimSpace(name)
	if !definitionNamePattern.MatchString(name) {
		return "", fmt.Errorf("skill name %q must be lower-kebab-case", name)
	}
	if err := InitTypeScriptProject(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, ".loom", "skills", name, "SKILL.md")
	if err := writeIfMissing(path, []byte(skillTemplate(name))); err != nil {
		return "", err
	}
	return path, nil
}

func writeIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func skillTemplate(name string) string {
	return fmt.Sprintf(`---
name: %s
description: Reusable guidance for %s work.
---

Use this skill when the agent needs to perform %s work consistently.

Before finishing, summarize what changed, what evidence was checked, and any follow-up work.
`, name, name, name)
}

const defaultConfig = `import { defineConfig } from '@loom/sdk';

export default defineConfig({
  sourceRoot: '.loom',
});
`

const daytonaConnector = `import { daytona as loomDaytona } from '@loom/runtime';

export function daytona(sandbox, options = {}) {
  return loomDaytona(sandbox, options);
}

export default daytona;
`

const startContract = `# Create a TypeScript-First Loom Agent

You are helping the user create or update a TypeScript-first Loom agent or
workflow in this repository.

## Contract

Before changing files, inspect the current repository and treat the existing
layout as authoritative. Import authored helpers from @loom/sdk and use the
.loom source layout:

.loom/
  agents/
  connectors/
  workflows/
  runtimes/
  tools/
  skills/

Ask only for choices that cannot be inferred safely:

1. Agent or workflow purpose.
2. Whether this is a continuing agent session or a finite workflow run.
3. Runtime target and model/provider.
4. Required environment variable names.

## First Agent Path

For a continuing agent, create .loom/agents/<name>.ts with createAgent(...).
Keep the first version small and locally provable. The first success moment is
a private local connect session:

loom add agent <name>
loom check
loom connect <name> local --message "hello"
loom defs plan
loom defs apply --start

Source files alone do not make an agent visible in the Loom UI. UI/runtime
visibility starts after durable apply and explicit start creates or updates the
workspace agent instance.

## First Workflow Path

For a finite orchestration, create .loom/workflows/<name>.ts with
defineWorkflow(...). The first success moment is a bounded workflow run:

loom add workflow <name>
loom check
loom run <name> --payload '{}'
loom defs plan
loom defs apply

Do not turn an agent request into a workflow-only artifact unless the user asks
for a bounded job.

## Runtime And Capabilities

Keep runtime authority explicit:

- Runtime profiles live in .loom/runtimes/*.ts.
- Provider adapters, including the Daytona sandbox adapter, live in .loom/connectors/*.ts.
- Typed tools live in .loom/tools/*.ts and need trusted handlers.
- Skills live in .loom/skills/<name>/SKILL.md when source-owned.
- Environment bindings list names only; never invent or write secret values.
- Route and trigger exposure must be reviewed in loom defs plan.

## Control Plane Fit

TypeScript is the reviewed authoring layer. Loom/FleetDB durable records remain
the runtime source of truth. Direct control-plane changes are valid for
operators and should converge with TypeScript through plan/apply or
from-workspace export.

## Finish Criteria

Before finishing:

- Run loom check or explain why it cannot run.
- Show the files changed.
- Show the plan/apply command the user should run next.
- State whether the agent/workflow is only local source or has been durably
  applied and started.
`

func agentTemplate(name string) string {
	return fmt.Sprintf(`import { createAgent, runtime } from '@loom/sdk';

export default createAgent({
  name: '%s',
  description: 'A TypeScript-defined Loom agent.',
  backend: 'echo',
  model: 'local/echo',
  runtime: runtime.local({
    repos: ['.'],
    env: [],
  }),
  instructions: `+"`"+`
    You are a helpful engineering agent.
    Inspect the workspace before making changes and keep edits scoped.
  `+"`"+`,
  tools: [],
  policy: {
    allowedCommands: ['git status', 'go test ./...'],
    deniedCommands: ['git reset --hard'],
    maxConcurrency: 1,
  },
});
`, name)
}

func workflowTemplate(name string) string {
	return fmt.Sprintf(`import { defineWorkflow, trigger } from '@loom/sdk';

type Input = {
  parentId: string;
  role?: string;
  maxConcurrency?: number;
};

export default defineWorkflow({
  name: '%s',
  description: 'Ensure one live task run per ready child under a parent work item.',
  singleton: (input: Input) => `+"`"+`parent:${input.parentId}`+"`"+`,
  expose: {
    http: {
      path: '/workflows/%s/run',
      auth: 'workspace',
    },
  },
  triggers: [
    trigger.issueLabelAdded({ label: '%s', type: 'epic' }),
  ],
  tools: ['workItems.readyChildren', 'taskRuns.ensure'],
  repos: ['.'],
  env: [],

  async run(ctx) {
    const parentId = String(ctx.input.parentId ?? ctx.input.parent_id ?? '');
    if (!parentId) throw new Error('parentId is required');

    const role = String(ctx.input.role ?? 'task');
    const maxConcurrency = Number(ctx.input.maxConcurrency ?? ctx.input.max_concurrency ?? 4);
    ctx.log.info('checking ready child work', { parentId, role, maxConcurrency });

    const ready = await ctx.workItems.readyChildren(parentId, { limit: maxConcurrency });
    for (const child of ready.slice(0, maxConcurrency)) {
      await ctx.taskRuns.ensure({
        workItemId: String(child.id),
        role,
        reason: String(child.title ?? ''),
      });
    }

    return { parentId, ensured: ready.length };
  },
});
`, name, name, name)
}
