package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestLiveDaytonaWorkflowSmoke(t *testing.T) {
	if os.Getenv("LOOM_DAYTONA_LIVE") != "1" {
		t.Skip("set LOOM_DAYTONA_LIVE=1 to run the live Daytona smoke test")
	}
	if os.Getenv("DAYTONA_API_KEY") == "" {
		t.Fatal("DAYTONA_API_KEY is required for live Daytona smoke test")
	}
	sdkRoot := os.Getenv("LOOM_DAYTONA_SDK_ROOT")
	if sdkRoot == "" {
		t.Fatal("LOOM_DAYTONA_SDK_ROOT must point at a directory containing node_modules/@daytona/sdk")
	}
	if _, err := os.Stat(filepath.Join(sdkRoot, "node_modules", "@daytona", "sdk", "package.json")); err != nil {
		t.Fatalf("missing @daytona/sdk under LOOM_DAYTONA_SDK_ROOT: %v", err)
	}

	ctx := context.Background()
	root, err := os.MkdirTemp(sdkRoot, "loom-live-workflow-*")
	if err != nil {
		t.Fatalf("mkdir live workflow root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	writeLiveDaytonaFile(t, root, ".flue/connectors/daytona.ts", `import { daytona as loomDaytona } from '@flue/runtime';

export function daytona(sandbox, options = {}) {
  return loomDaytona(sandbox, options);
}
`)
	writeLiveDaytonaFile(t, root, ".flue/workflows/code.ts", `import {
  createAgent,
  type FlueContext,
  type WorkflowRouteHandler,
} from '@flue/runtime';
import { Daytona } from '@daytona/sdk';
import { daytona } from '../connectors/daytona';

export const route: WorkflowRouteHandler = async (_c, next) => next();

export async function run({ init, payload, env, taskRuns }: FlueContext) {
  const client = new Daytona({ apiKey: env.DAYTONA_API_KEY });
  const sandbox = await client.create({
    language: 'typescript',
    envVars: { LOOM_DAYTONA_LIVE_SMOKE: '1' },
    autoStopInterval: 5,
  }, { timeout: 120 });
  try {
    const setupAgent = createAgent(() => ({
      sandbox: daytona(sandbox),
      model: 'openai/gpt-5.5',
    }));
    const setup = await (await init(setupAgent, { name: 'setup' })).session();
    const setupResult = await setup.shell("git clone --depth 1 " + payload.repo + " /home/daytona/project && mkdir -p /home/daytona/bin && printf '%s\n' '#!/bin/sh' 'printf \"loom-live-prompt:%s\\n\" \"$*\"' '/bin/pwd' > /home/daytona/bin/codex && chmod +x /home/daytona/bin/codex && printf 'loom-live-setup\n' && node --version && git --version");
    const [taskRun] = await taskRuns.list({ workItemId: payload.taskId });
    if (!taskRun) throw new Error("expected live seeded task run for " + payload.taskId);

    const projectAgent = createAgent(() => ({
      sandbox: daytona(sandbox, { cwd: '/home/daytona/project' }),
      model: 'openai/gpt-5.5',
    }));
    const project = await (await init(projectAgent, { name: 'project' })).session('pickup', {
      taskId: payload.taskId,
      taskRunId: taskRun.task_run_id,
    });
    const promptResult = await project.prompt(payload.prompt, {
      cwd: '/home/daytona/project',
      env: {
        PATH: '/home/daytona/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
      },
    });
    return {
      setupStatus: setupResult.status,
      promptStatus: promptResult.status,
      promptText: promptResult.text,
      runtimeProvider: promptResult.runtimeProvider,
      taskRunId: taskRun.task_run_id,
      sandboxId: promptResult.providerExecution?.sandboxId,
    };
  } finally {
    if (typeof client.delete === 'function') {
      await client.delete(sandbox, 60);
    }
  }
}
`)

	plan, err := defspkg.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	workflow, ok := defspkg.FindWorkflow(plan, "code")
	if !ok || !containsWorkflowString(workflow.Env, "DAYTONA_API_KEY") {
		t.Fatalf("workflow = %+v, want .flue Daytona workflow with API key env grant", workflow)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "LIVE-DAYTONA", Name: "Live Daytona Smoke"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := defspkg.Apply(ctx, st, "LIVE-DAYTONA", "atlas", plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	run, err := CreateOrResumeRun(ctx, st, "LIVE-DAYTONA", "code", json.RawMessage(`{"repo":"https://github.com/octocat/Hello-World.git","prompt":"inspect live sandbox","taskId":"LIVE-DAYTONA-1"}`), "atlas")
	if err != nil {
		t.Fatalf("CreateOrResumeRun() error = %v", err)
	}
	taskRun, err := st.TaskRuns().Ensure(ctx, store.TaskRunEnsure{
		WorkspaceKey:   "LIVE-DAYTONA",
		WorkflowRunID:  run.RunID,
		WorkItemID:     "LIVE-DAYTONA-1",
		RoleName:       "task",
		IdempotencyKey: "live:LIVE-DAYTONA-1",
		Status:         domain.TaskRunQueued,
	})
	if err != nil {
		t.Fatalf("seed live task run: %v", err)
	}
	result, err := RunOnce(ctx, st, clitest.NewMockIssueBackend(), run)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Run == nil || result.Run.Status != domain.WorkflowRunCompleted {
		t.Fatalf("result = %+v, want completed live Daytona workflow", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Run.Result, &data); err != nil {
		t.Fatalf("decode workflow result: %v", err)
	}
	if data["setupStatus"] != "completed" || data["promptStatus"] != "completed" ||
		data["runtimeProvider"] != "daytona" || data["taskRunId"] != taskRun.TaskRunID || data["sandboxId"] == "" {
		t.Fatalf("result data = %+v, want live Daytona setup and prompt completion", data)
	}
	if text, ok := data["promptText"].(string); !ok ||
		!strings.Contains(text, "loom-live-prompt:exec --json") ||
		!strings.Contains(text, "inspect live sandbox") ||
		!strings.Contains(text, "/home/daytona/project") {
		t.Fatalf("prompt text = %q, want live Daytona prompt command output", data["promptText"])
	}
	updated, err := st.TaskRuns().Get(ctx, "LIVE-DAYTONA", taskRun.TaskRunID)
	if err != nil {
		t.Fatalf("get live task run: %v", err)
	}
	if updated.Status != domain.TaskRunPassed || updated.AgentID == "" || updated.SessionID == "" ||
		updated.ClaimActor != updated.AgentID || updated.Metadata["runtime_provider"] != "daytona" ||
		updated.Metadata["daytona_sandbox_id"] == "" {
		t.Fatalf("live task run = %+v, want passed task picked up by Daytona session", updated)
	}
}

func writeLiveDaytonaFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
