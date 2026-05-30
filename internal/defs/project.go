package defs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

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
	if err := writeIfMissing(filepath.Join(root, ".loom", "runtime.d.ts"), []byte(runtimeTypes)); err != nil {
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

const defaultConfig = `import { defineConfig } from '@loom/runtime';

export default defineConfig({
  sourceRoot: '.loom',
});
`

func agentTemplate(name string) string {
	return fmt.Sprintf(`import { createAgent, runtime } from '@loom/runtime';

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
	return fmt.Sprintf(`import { defineWorkflow, trigger } from '@loom/runtime';

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

const runtimeTypes = `declare module '@loom/runtime' {
  export type RuntimeProfile = {
    provider: string;
    name?: string;
    image?: string;
    repos?: string[];
    env?: string[];
    cpu?: string;
    memory?: string;
  };

  export type AgentDefinition = {
    name: string;
    description?: string;
    backend?: string;
    model?: string;
    runtime?: RuntimeProfile;
    instructions?: string;
    skills?: Array<string | SkillDefinition>;
    tools?: Array<string | unknown>;
    allowedCommands?: string[];
    deniedCommands?: string[];
    repos?: string[];
    env?: string[];
    maxConcurrency?: number;
    maxBudgetUSD?: number;
    readOnly?: boolean;
    policy?: {
      allowedCommands?: string[];
      deniedCommands?: string[];
      maxConcurrency?: number;
      maxBudgetUSD?: number;
      readOnly?: boolean;
    };
  };

  export type SkillDefinition = {
    name: string;
    description?: string;
    version?: string;
    source_path?: string;
    source_hash?: string;
    instructions?: string;
    resources?: string[];
  };

  export type ToolSchema = Record<string, unknown>;

  export type ToolDefinition = {
    name: string;
    description: string;
    parameters: ToolSchema;
    handler?: string;
    runtime?: string;
    repos?: string[];
    env?: string[];
    readOnly?: boolean;
    execute?: (args: Record<string, unknown>, signal?: AbortSignal) => string | Promise<string>;
  };

  export type WorkflowDefinition = {
    name: string;
    description?: string;
    builtin?: string;
    runner?: string;
    singleton?: string | ((input: Record<string, unknown>) => string);
    path?: string;
    auth?: string;
    routePath?: string;
    routeAuth?: string;
    expose?: {
      http?: {
        path?: string;
        auth?: string;
      };
    };
    triggerEvent?: string;
    triggerFilter?: Record<string, string>;
    issueLabelAdded?: Record<string, string>;
    triggers?: Array<{ event: string; filter?: Record<string, string> }>;
    tools?: Array<string | unknown>;
    repos?: string[];
    env?: string[];
    run?: (ctx: WorkflowContext) => unknown | Promise<unknown>;
  };

  export type WorkflowRequestContext = {
    workspaceKey: string;
    workflowName: string;
    workflowVersion: string;
    actor?: string;
  };

  export type WorkflowContext = {
    id: string;
    input: Record<string, unknown>;
    payload: Record<string, unknown>;
    env: Record<string, string | undefined>;
    req: WorkflowRequestContext;
    request: WorkflowRequestContext;
    log: {
      info(message: string, attributes?: Record<string, unknown>): void;
      warn(message: string, attributes?: Record<string, unknown>): void;
      error(message: string, attributes?: Record<string, unknown>): void;
    };
    workItems: {
      readyChildren(parentId: string, options?: Record<string, unknown>): Promise<Array<Record<string, unknown>>>;
      blockedChildren(parentId: string, options?: Record<string, unknown>): Promise<Array<Record<string, unknown>>>;
      listChildren(parentId: string, options?: Record<string, unknown>): Promise<Array<Record<string, unknown>>>;
    };
    taskRuns: {
      ensure(input: {
        workItemId?: string;
        work_item_id?: string;
        role?: string;
        roleName?: string;
        reason?: string;
        metadata?: Record<string, string>;
      }): Promise<Record<string, unknown>>;
    };
  };

  export function defineConfig<T extends object>(config: T): T;
  export function defineAgent<T extends AgentDefinition>(agent: T): T;
  export function createAgent<T extends AgentDefinition>(agent: T): T;
  export function defineAgentProfile<T extends AgentDefinition>(profile: T): T;
  export function defineSkill<T extends SkillDefinition>(skill: T): T;
  export function defineTool<T extends ToolDefinition>(tool: T): T;
  export function defineWorkflow<T extends WorkflowDefinition>(workflow: T): T;

  export const schema: {
    [kind: string]: (...args: unknown[]) => ToolSchema;
  };

  export const Type: {
    [kind: string]: (...args: unknown[]) => ToolSchema;
  };

  export const runtime: {
    local(config: Omit<RuntimeProfile, 'provider'>): RuntimeProfile;
    podman(config: Omit<RuntimeProfile, 'provider'>): RuntimeProfile;
    remote(config: Omit<RuntimeProfile, 'provider'> & { provider?: string }): RuntimeProfile;
  };

  export const trigger: {
    issueLabelAdded(config?: Record<string, string>): { event: 'issue.label_added'; filter: Record<string, string> };
  };
}

declare module '*.md' {
  const skill: import('@loom/runtime').SkillDefinition;
  export default skill;
}
`
