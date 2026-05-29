package defs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var definitionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

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

export default defineWorkflow({
  name: '%s',
  description: 'Ensure one live task run per ready child under a parent work item.',
  builtin: 'run-parent-work-items',
  singleton: (input) => `+"`"+`parent:${input.parentId}`+"`"+`,
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
    skills?: string[];
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

  export type WorkflowDefinition = {
    name: string;
    description?: string;
    builtin?: string;
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
  };

  export function defineConfig<T extends object>(config: T): T;
  export function defineAgent<T extends AgentDefinition>(agent: T): T;
  export function createAgent<T extends AgentDefinition>(agent: T): T;
  export function defineAgentProfile<T extends AgentDefinition>(profile: T): T;
  export function defineWorkflow<T extends WorkflowDefinition>(workflow: T): T;

  export const runtime: {
    local(config: Omit<RuntimeProfile, 'provider'>): RuntimeProfile;
    podman(config: Omit<RuntimeProfile, 'provider'>): RuntimeProfile;
    remote(config: Omit<RuntimeProfile, 'provider'> & { provider?: string }): RuntimeProfile;
  };

  export const trigger: {
    issueLabelAdded(config?: Record<string, string>): { event: 'issue.label_added'; filter: Record<string, string> };
  };
}
`
