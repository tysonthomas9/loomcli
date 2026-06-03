import assert from "node:assert/strict";
import test from "node:test";

import {
  FetchLoomTransport,
  createLoomClient,
  daytona,
  defineAgent,
  defineRuntimeProfile,
  defineWorkflow,
  runtime,
  sourceToProjectDir,
  trigger,
} from "./index.js";

class FakeTransport {
  constructor(results = []) {
    this.calls = [];
    this.results = results;
  }

  async run(args, options) {
    this.calls.push({ args, options });
    const result = this.results.shift() || { ok: true, args };
    return {
      stdout: JSON.stringify(result),
      stderr: "",
      status: 0,
    };
  }
}

class FakeRequestTransport {
  constructor(results = []) {
    this.workspace = "WS";
    this.calls = [];
    this.results = results;
  }

  async request(method, path, options) {
    this.calls.push({ method, path, options });
    return this.results.shift() ?? { ok: true, method, path };
  }
}

test("sourceToProjectDir normalizes .loom source roots to the project directory", () => {
  assert.equal(sourceToProjectDir(".loom"), ".");
  assert.equal(sourceToProjectDir("apps/site/.loom"), "apps/site");
  assert.equal(sourceToProjectDir("apps/site"), "apps/site");
});

test("defineRuntimeProfile is a first-class runtime authoring helper", () => {
  assert.deepEqual(defineRuntimeProfile({ name: "local-dev", provider: "local", cwd: "." }), {
    name: "local-dev",
    provider: "local",
    cwd: ".",
  });
  assert.deepEqual(runtime.remote({ name: "sandbox" }), {
    name: "sandbox",
    provider: "e2b",
  });
  assert.deepEqual(runtime.daytona({ name: "daytona-dev", target: "us", apiKeyEnv: "DAYTONA_API_KEY" }), {
    name: "daytona-dev",
    target: "us",
    apiKeyEnv: "DAYTONA_API_KEY",
    provider: "daytona",
  });
  assert.deepEqual(daytona({ id: "sandbox-1", cwd: "/workspace/project", snapshot: "snap", target: "us" }), {
    provider: "daytona",
    cwd: "/workspace/project",
    workspace: {
      providerWorkspaceId: "sandbox-1",
      provider_workspace_id: "sandbox-1",
      provider: "daytona",
    },
    daytona: {
      sandbox_id: "sandbox-1",
      sandboxId: "sandbox-1",
      snapshot: "snap",
      target: "us",
    },
  });
  assert.deepEqual(daytona({ id: "sandbox-3" }, {
    cwd: "/workspace/project",
    env: ["OPENAI_API_KEY", "GITHUB_TOKEN"],
    repos: ["app"],
    language: "typescript",
    image: { base: "debian-slim:3.12" },
    resources: { cpu: 2, memory: 4 },
    envVars: { NODE_ENV: "test" },
    autoStopInterval: 15,
    autoArchiveInterval: 60,
    autoDeleteInterval: 120,
    ephemeral: false,
    repoUrl: "https://github.com/acme/app.git",
    branch: "main",
    gitTokenEnv: "GITHUB_TOKEN",
    openaiApiKeyEnv: "OPENAI_API_KEY",
    setupCommands: ["npm ci"],
    createTimeout: 90,
    runTimeout: 600,
    buildLogs: "inherit",
  }), {
    provider: "daytona",
    cwd: "/workspace/project",
    env: ["OPENAI_API_KEY", "GITHUB_TOKEN"],
    repos: ["app"],
    workspace: {
      providerWorkspaceId: "sandbox-3",
      provider_workspace_id: "sandbox-3",
      provider: "daytona",
    },
    daytona: {
      sandbox_id: "sandbox-3",
      sandboxId: "sandbox-3",
      language: "typescript",
      image: { base: "debian-slim:3.12" },
      resources: { cpu: 2, memory: 4 },
      env_vars: { NODE_ENV: "test" },
      auto_stop_interval: 15,
      auto_archive_interval: 60,
      auto_delete_interval: 120,
      ephemeral: false,
      repo_url: "https://github.com/acme/app.git",
      branch: "main",
      git_token_env: "GITHUB_TOKEN",
      openai_api_key_env: "OPENAI_API_KEY",
      setup_commands: ["npm ci"],
      create_timeout: 90,
      run_timeout: 600,
      build_logs: "inherit",
    },
  });
  const instrumentedSandbox = {};
  Object.defineProperty(instrumentedSandbox, "__loomDaytona", {
    value: { sandbox_id: "sandbox-2", cwd: "/workspace/app", snapshot: "hidden-snap" },
  });
  assert.deepEqual(daytona(instrumentedSandbox, { name: "instrumented" }), {
    provider: "daytona",
    profileName: "instrumented",
    profile_name: "instrumented",
    name: "instrumented",
    cwd: "/workspace/app",
    workspace: {
      providerWorkspaceId: "sandbox-2",
      provider_workspace_id: "sandbox-2",
      provider: "daytona",
    },
    daytona: {
      sandbox_id: "sandbox-2",
      cwd: "/workspace/app",
      snapshot: "hidden-snap",
      sandboxId: "sandbox-2",
    },
  });
});

test("trigger helpers compile platform intent into stable event/filter objects", () => {
  assert.deepEqual(trigger.github("pull_request.closed", { action: "closed", merged: true, ignored: null }), {
    event: "github.pull_request.closed",
    filter: { action: "closed", merged: "true" },
  });
  assert.deepEqual(trigger.cron("0 * * * *", { timezone: "UTC" }), {
    event: "schedule.cron",
    filter: { timezone: "UTC", schedule: "0 * * * *" },
  });
});

test("loom.check uses the project root for a .loom source", async () => {
  const transport = new FakeTransport([{ root: "/repo", agents: [] }]);
  const loom = createLoomClient({ transport });

  const result = await loom.check({ source: ".loom" });

  assert.deepEqual(result, { root: "/repo", agents: [] });
  assert.deepEqual(transport.calls[0].args, ["check", "--json", "--dir", "."]);
});

test("loom.connect accepts source-defined agent objects and local session options", async () => {
  const transport = new FakeTransport([{ agent: "triage", instance: "local" }]);
  const loom = createLoomClient({ transport });
  const triage = defineAgent({ name: "triage" });

  await loom.connect(triage, {
    source: "apps/site/.loom",
    id: "local",
    session: "issue-123",
    message: "hello",
  });

  assert.deepEqual(transport.calls[0].args, [
    "connect",
    "triage",
    "local",
    "--json",
    "--dir",
    "apps/site",
    "--session",
    "issue-123",
    "--message",
    "hello",
  ]);
});

test("loom.run serializes object input for finite workflow runs", async () => {
  const transport = new FakeTransport([{ run: { workflow_name: "route-issue" } }]);
  const loom = createLoomClient({ transport });
  const workflow = defineWorkflow({ name: "route-issue" });

  await loom.run(workflow, { dir: "/repo", input: { issue_id: "ISS-1" }, wait: true });

  assert.deepEqual(transport.calls[0].args, [
    "run",
    "route-issue",
    "--json",
    "--dir",
    "/repo",
    "--input",
    "{\"issue_id\":\"ISS-1\"}",
    "--wait",
  ]);
});

test("loom.defs exposes plan, apply start, and source export operations", async () => {
  const transport = new FakeTransport([{ plan: true }, { applied: true }, [{ path: ".loom/agents/triage.ts" }]]);
  const loom = createLoomClient({ transport });

  await loom.defs.plan({ fromWorkspace: true });
  await loom.defs.apply({ source: ".loom", start: true });
  await loom.defs.exportSource({ dir: "/repo", force: true, includeState: true });

  assert.deepEqual(transport.calls[0].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[1].args, ["defs", "apply", "--json", "--dir", ".", "--start"]);
  assert.deepEqual(transport.calls[2].args, [
    "defs",
    "export-source",
    "--json",
    "--dir",
    "/repo",
    "--force",
    "--include-state",
  ]);
});

test("loom SDK exposes workflow and run inspection namespaces", async () => {
  const transport = new FakeTransport([
    [{ name: "route-issue" }],
    [{ binding_id: "route-1" }],
    { binding_id: "route-1" },
    { binding_id: "route-1", status: "disabled" },
    [{ binding_id: "trigger-1" }],
    { binding_id: "trigger-1" },
    { binding_id: "trigger-1", status: "disabled" },
    { run_id: "wrun-1" },
    [{ type: "workflow_started" }],
    [{ task_run_id: "trun-1" }],
    [{ session_id: "session-1" }],
    [{ operation_id: "op-1" }],
    [{ call_id: "call-1" }],
    [{ artifact_id: "artifact-1" }],
    { cancelled: true },
  ]);
  const loom = createLoomClient({ transport });
  const workflow = defineWorkflow({ name: "route-issue" });

  await loom.workflows.list();
  await loom.workflows.listRoutes(workflow);
  await loom.workflows.bindRoute(workflow, "/hooks/issue", { auth: "workspace" });
  await loom.workflows.unbindRoute(workflow, "/hooks/issue");
  await loom.workflows.listTriggers(workflow);
  await loom.workflows.bindTrigger("route-issue", "github.pull_request.closed", {
    filter: { action: "closed" },
  });
  await loom.workflows.unbindTrigger("route-issue", "github.pull_request.closed");
  await loom.runs.get("wrun-1");
  await loom.runs.events("wrun-1");
  await loom.runs.tasks("wrun-1");
  await loom.runs.sessions("wrun-1");
  await loom.runs.operations("wrun-1");
  await loom.runs.toolCalls("wrun-1");
  await loom.runs.artifacts("wrun-1", { type: "report" });
  await loom.runs.cancel("wrun-1");

  assert.deepEqual(transport.calls[0].args, ["workflow", "list", "--json"]);
  assert.deepEqual(transport.calls[1].args, ["workflow", "route", "list", "route-issue", "--json"]);
  assert.deepEqual(transport.calls[2].args, [
    "workflow",
    "route",
    "bind",
    "route-issue",
    "/hooks/issue",
    "--auth",
    "workspace",
    "--json",
  ]);
  assert.deepEqual(transport.calls[3].args, [
    "workflow",
    "route",
    "remove",
    "route-issue",
    "/hooks/issue",
    "--json",
  ]);
  assert.deepEqual(transport.calls[4].args, ["workflow", "trigger", "list", "route-issue", "--json"]);
  assert.deepEqual(transport.calls[5].args, [
    "workflow",
    "trigger",
    "bind",
    "route-issue",
    "github.pull_request.closed",
    "--filter",
    "{\"action\":\"closed\"}",
    "--json",
  ]);
  assert.deepEqual(transport.calls[6].args, [
    "workflow",
    "trigger",
    "remove",
    "route-issue",
    "github.pull_request.closed",
    "--json",
  ]);
  assert.deepEqual(transport.calls[7].args, ["workflow", "show", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[8].args, ["workflow", "logs", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[9].args, ["workflow", "tasks", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[10].args, ["workflow", "sessions", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[11].args, ["workflow", "operations", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[12].args, ["workflow", "tool-calls", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[13].args, ["workflow", "artifacts", "wrun-1", "--json", "--type", "report"]);
  assert.deepEqual(transport.calls[14].args, ["workflow", "cancel", "wrun-1", "--json"]);
});

test("loom SDK can use web API transport for workflow control-plane operations", async () => {
  const transport = new FakeRequestTransport([
    { data: [{ name: "route-issue" }] },
    { data: [{ binding_id: "route-1" }] },
    { binding_id: "route-1" },
    { binding_id: "route-1", status: "disabled" },
    { data: [{ binding_id: "trigger-1" }] },
    { binding_id: "trigger-1" },
    { binding_id: "trigger-1", status: "disabled" },
    { run: { run_id: "wrun-1" } },
    { run_id: "wrun-1" },
    { data: [{ type: "workflow_started" }] },
    { data: [{ task_run_id: "trun-1" }] },
    { data: [{ session_id: "session-1" }] },
    { data: [{ operation_id: "op-1" }] },
    { data: [{ call_id: "call-1" }] },
    { data: [{ artifact_id: "artifact-1" }] },
    { run_id: "wrun-1", status: "cancelled" },
    { operation_id: "op-1", status: "cancelled" },
  ]);
  const loom = createLoomClient({ transport });
  const workflow = defineWorkflow({ name: "route-issue" });

  assert.deepEqual(await loom.workflows.list(), [{ name: "route-issue" }]);
  assert.deepEqual(await loom.workflows.listRoutes(workflow), [{ binding_id: "route-1" }]);
  await loom.workflows.bindRoute(workflow, "/hooks/issue", { auth: "workspace" });
  await loom.workflows.unbindRoute(workflow, "/hooks/issue");
  assert.deepEqual(await loom.workflows.listTriggers(workflow), [{ binding_id: "trigger-1" }]);
  await loom.workflows.bindTrigger("route-issue", "github.pull_request.closed", {
    filter: { action: "closed" },
  });
  await loom.workflows.unbindTrigger("route-issue", "github.pull_request.closed");
  await loom.run(workflow, { input: { issue_id: "ISS-1" }, once: false });
  await loom.runs.get("wrun-1");
  assert.deepEqual(await loom.runs.events("wrun-1"), [{ type: "workflow_started" }]);
  assert.deepEqual(await loom.runs.tasks("wrun-1"), [{ task_run_id: "trun-1" }]);
  assert.deepEqual(await loom.runs.sessions("wrun-1"), [{ session_id: "session-1" }]);
  assert.deepEqual(await loom.runs.operations("wrun-1"), [{ operation_id: "op-1" }]);
  assert.deepEqual(await loom.runs.toolCalls("wrun-1"), [{ call_id: "call-1" }]);
  assert.deepEqual(await loom.runs.artifacts("wrun-1", { type: "report" }), [{ artifact_id: "artifact-1" }]);
  await loom.runs.cancel("wrun-1");
  await loom.operations.cancel("op-1", { reason: "user requested" });

  assert.deepEqual(transport.calls[0], {
    method: "GET",
    path: "/api/workspaces/WS/workflows",
    options: { query: undefined, body: undefined, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[1], {
    method: "GET",
    path: "/api/workspaces/WS/workflow-route-bindings",
    options: { query: { workflow: "route-issue" }, body: undefined, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[2], {
    method: "POST",
    path: "/api/workspaces/WS/workflows/route-issue/routes",
    options: { query: undefined, body: { path: "/hooks/issue", auth: "workspace" }, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[3], {
    method: "DELETE",
    path: "/api/workspaces/WS/workflows/route-issue/routes/hooks/issue",
    options: { query: undefined, body: undefined, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[5], {
    method: "POST",
    path: "/api/workspaces/WS/workflows/route-issue/triggers",
    options: {
      query: undefined,
      body: { event: "github.pull_request.closed", filter: { action: "closed" } },
      signal: undefined,
      headers: undefined,
    },
  });
  assert.deepEqual(transport.calls[7], {
    method: "POST",
    path: "/api/workspaces/WS/workflows/route-issue/runs",
    options: {
      query: undefined,
      body: { input: { issue_id: "ISS-1" }, once: false },
      signal: undefined,
      headers: undefined,
    },
  });
  assert.deepEqual(transport.calls[12], {
    method: "GET",
    path: "/api/workspaces/WS/workflow-runs/wrun-1/operations",
    options: { query: undefined, body: undefined, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[13], {
    method: "GET",
    path: "/api/workspaces/WS/workflow-runs/wrun-1/tool-calls",
    options: { query: undefined, body: undefined, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[14], {
    method: "GET",
    path: "/api/workspaces/WS/workflow-runs/wrun-1/artifacts",
    options: { query: { type: "report" }, body: undefined, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[15], {
    method: "POST",
    path: "/api/workspaces/WS/workflow-runs/wrun-1/cancel",
    options: { query: undefined, body: undefined, signal: undefined, headers: undefined },
  });
  assert.deepEqual(transport.calls[16], {
    method: "POST",
    path: "/api/workspaces/WS/agent-session-operations/op-1/cancel",
    options: { query: undefined, body: { reason: "user requested" }, signal: undefined, headers: undefined },
  });
});

test("FetchLoomTransport sends JSON requests with auth headers", async () => {
  const calls = [];
  const transport = new FetchLoomTransport({
    baseURL: "https://loom.example.test/",
    workspace: "WS",
    apiKey: "key-1",
    authToken: "token-1",
    fetch: async (url, init) => {
      calls.push({ url: String(url), init });
      return {
        ok: true,
        status: 200,
        async text() {
          return JSON.stringify({ ok: true });
        },
      };
    },
  });

  assert.deepEqual(await transport.request("POST", "/api/workspaces/WS/workflows/route-issue/runs", {
    query: { stream: true },
    body: { input: {} },
  }), { ok: true });

  assert.equal(calls[0].url, "https://loom.example.test/api/workspaces/WS/workflows/route-issue/runs?stream=true");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.headers["X-Fleet-API-Key"], "key-1");
  assert.equal(calls[0].init.headers.Authorization, "Bearer token-1");
  assert.equal(calls[0].init.body, "{\"input\":{}}");
});

test("loom SDK exposes durable session task event tool and admin namespaces", async () => {
  const plan = {
    agent_sessions: [{ session_id: "session-1", status: "running" }],
    agent_session_operations: [{ operation_id: "op-1", session_id: "session-1" }],
    agent_session_tool_calls: [{ call_id: "call-1", operation_id: "op-1" }],
    task_runs: [{ task_run_id: "trun-1", workflow_run_id: "wrun-1" }],
    run_events: [{ event_id: "evt-1", type: "workflow_started" }],
    tools: [{ name: "github_issue_read" }],
  };
  const transport = new FakeTransport([
    plan,
    plan,
    plan,
    plan,
    plan,
    plan,
    plan,
    plan,
    plan,
    [{ session_id: "session-run" }],
    [{ operation_id: "op-run" }],
    [{ call_id: "call-run" }],
    { operation_id: "op-1", status: "cancelled" },
    [{ task_run_id: "trun-run" }],
    [{ event_id: "evt-run" }],
    { ok: true },
    { ok: false },
    { ok: true, repaired: true },
  ]);
  const loom = createLoomClient({ transport });

  assert.deepEqual(await loom.sessions.list(), plan.agent_sessions);
  assert.deepEqual(await loom.sessions.get("session-1"), plan.agent_sessions[0]);
  assert.deepEqual(await loom.operations.list(), plan.agent_session_operations);
  assert.deepEqual(await loom.operations.get("op-1"), plan.agent_session_operations[0]);
  assert.deepEqual(await loom.toolCalls.list(), plan.agent_session_tool_calls);
  assert.deepEqual(await loom.toolCalls.get("call-1"), plan.agent_session_tool_calls[0]);
  assert.deepEqual(await loom.tasks.list(), plan.task_runs);
  assert.deepEqual(await loom.events.get("evt-1"), plan.run_events[0]);
  assert.deepEqual(await loom.tools.get("github_issue_read"), plan.tools[0]);
  assert.deepEqual(await loom.sessions.forRun("wrun-1"), [{ session_id: "session-run" }]);
  assert.deepEqual(await loom.operations.forRun("wrun-1"), [{ operation_id: "op-run" }]);
  assert.deepEqual(await loom.toolCalls.forRun("wrun-1"), [{ call_id: "call-run" }]);
  await loom.operations.cancel("op-1", { reason: "stale" });
  assert.deepEqual(await loom.tasks.forRun("wrun-1"), [{ task_run_id: "trun-run" }]);
  assert.deepEqual(await loom.events.forRun("wrun-1"), [{ event_id: "evt-run" }]);
  await loom.admin.status({ workspace: "WS" });
  await loom.admin.diagnose({ key: "WS" });
  await loom.admin.repair({ workspaceKey: "WS", timeout: 7 });

  assert.deepEqual(transport.calls[0].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[1].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[2].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[3].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[4].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[5].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[6].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[7].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[8].args, ["defs", "plan", "--json", "--from-workspace"]);
  assert.deepEqual(transport.calls[9].args, ["workflow", "sessions", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[10].args, ["workflow", "operations", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[11].args, ["workflow", "tool-calls", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[12].args, [
    "workflow",
    "operation-cancel",
    "op-1",
    "--reason",
    "stale",
    "--json",
  ]);
  assert.deepEqual(transport.calls[13].args, ["workflow", "tasks", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[14].args, ["workflow", "logs", "wrun-1", "--json"]);
  assert.deepEqual(transport.calls[15].args, ["workspace", "ops", "status", "WS", "--json"]);
  assert.deepEqual(transport.calls[16].args, ["workspace", "ops", "diagnose", "WS", "--json"]);
  assert.deepEqual(transport.calls[17].args, [
    "workspace",
    "ops",
    "repair",
    "WS",
    "--json",
    "--timeout",
    "7",
  ]);
});

test("loom SDK exposes control-plane agent namespace", async () => {
  const transport = new FakeTransport([
    [{ name: "nova" }],
    { name: "nova" },
    { name: "nova", role_name: "coder" },
    { agent: { name: "nova" }, command: { type: "start" } },
    { agent: { name: "nova" }, command: { type: "stop" } },
    { status: "removed" },
  ]);
  const loom = createLoomClient({ transport });
  const nova = defineAgent({ name: "nova" });

  await loom.agents.list();
  await loom.agents.get(nova);
  await loom.agents.create("nova", {
    role: "coder",
    auto: true,
    backend: "echo",
    repos: ["slack-src", "api-src"],
    repoGroups: ["frontend"],
    crossRepo: true,
    parent: "EPIC-1",
    mode: "service",
    taskFilter: "status:ready",
    maxConcurrency: 2,
    budgetPolicy: "default",
    task: "TASK-1",
    orchestrator: "session-1",
  });
  await loom.agents.start(nova);
  await loom.agents.stop("nova", { force: true });
  await loom.agents.remove("nova");

  assert.deepEqual(transport.calls[0].args, ["agentdef", "list", "--json"]);
  assert.deepEqual(transport.calls[1].args, ["agentdef", "show", "nova", "--json"]);
  assert.deepEqual(transport.calls[2].args, [
    "agentdef",
    "add",
    "nova",
    "--role",
    "coder",
    "--auto",
    "--backend",
    "echo",
    "--repos",
    "slack-src,api-src",
    "--repo-groups",
    "frontend",
    "--cross-repo",
    "--parent",
    "EPIC-1",
    "--mode",
    "service",
    "--task-filter",
    "status:ready",
    "--max-concurrency",
    "2",
    "--budget-policy",
    "default",
    "--task",
    "TASK-1",
    "--orchestrator",
    "session-1",
    "--json",
  ]);
  assert.deepEqual(transport.calls[3].args, ["agentdef", "start", "nova", "--json"]);
  assert.deepEqual(transport.calls[4].args, ["agentdef", "stop", "nova", "--force", "--json"]);
  assert.deepEqual(transport.calls[5].args, ["agentdef", "remove", "nova", "--json"]);

  assert.throws(() => loom.agents.create("missing-role"), /role is required/);
});
