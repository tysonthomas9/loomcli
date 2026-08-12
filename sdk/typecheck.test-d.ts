// SDK v1 typecheck gate (SP1 contract freeze): compiled by `tsc -p
// tsconfig.typecheck.json` (npm run typecheck, part of npm test) and NEVER
// executed. It exercises every exported op signature so driver.d.ts /
// runner.d.ts / index.d.ts drift fails CI, and uses @ts-expect-error to
// prove the frozen enums (result statuses, watch event types, error codes)
// actually reject out-of-contract values.

import {
  ArtifactHandle,
  CompleteRunResponse,
  DaytonaProviderResult,
  DriverApiError,
  DriverApiErrorCode,
  LoomAwaitEventResult,
  LoomAwaitListResult,
  LoomAwaitStatus,
  LoomConnectorCallResult,
  LoomDriverClient,
  LoomDriverResult,
  LoomDriverResultStatus,
  LoomEpicWatchEventType,
  LoomWorkflowAwaitResult,
  LoomWorkflowStartResult,
  RunnerEnv,
  TaskRun,
  TaskRunClient,
  WorkflowSuspended,
  createLoomClient,
  createFlueTranscriptCollector,
  createLoomDriverClient,
  flueEventsToTaskUsage,
  flueUsageToTaskUsage,
  isWorkflowSuspended,
  serializeTranscriptJSONL,
} from "./index.js";
import type {
  FlueTranscriptCollector,
  LoomTaskUsage,
  LoomTranscriptEntry,
} from "./index.js";

export function expectType<T>(_value: T): void {
  void _value;
}

export async function exerciseLoomDriverClientSurface(): Promise<void> {
  const client: LoomDriverClient = createLoomDriverClient({
    apiUrl: "http://localhost:8080",
	runToken: "run-token",
    env: { LOOM_DRIVER_WORKSPACE: "WS" },
    input: { epicId: "EPIC-1" },
  });
  expectType<LoomDriverClient>(LoomDriverClient.fromEnv({ runToken: "tok" }));
  expectType<LoomDriverClient>(createLoomClient({ epicId: "EPIC-1" }));
  expectType<string>(client.runToken);
  expectType<string>(client.workspace);
  expectType<string>(client.driverRunId);

  // epics
  expectType<Record<string, unknown> | null>(await client.epics.get({ epicId: "EPIC-1" }));
  expectType<Record<string, unknown> | null>(await client.epics.snapshot({}));
  for await (const event of client.epics.watch({ epicId: "EPIC-1", afterSeq: 7, reconnectMs: 250 })) {
    expectType<LoomEpicWatchEventType>(event.type);
    expectType<string>(event.id);
    expectType<unknown>(event.data);
  }

  // agents
  expectType<Record<string, unknown>[] | null>(await client.agents.list());
  await client.agents.orchestrationSession({ agent: "lead" });
  await client.agents.updateParent({ agent: "lead", parent: "EPIC-2", expectParent: "EPIC-1" });
  await client.agents.deliverAssignment({ agent: "lead" });
  await client.agents.message({ agent: "lead", message: "hello" });

  // tasks + taskRuns
  expectType<Record<string, unknown> | null>(await client.tasks.claimReady({ epicId: "EPIC-1", sourceRepo: "alpha" }));
  expectType<Record<string, unknown> | null>(await client.tasks.claim({ taskId: "TASK-1", epicId: "EPIC-1", limit: 5 }));
  expectType<Record<string, unknown> | null>(await client.tasks.claimReview({ taskId: "TASK-1" }));
  expectType<Record<string, unknown> | null>(await client.tasks.handoffReview({
    taskId: "TASK-1", taskRunId: "task-run-1", status: "open", reason: "changes requested",
  }));
  expectType<Record<string, unknown> | null>(await client.tasks.handoffReview({
    taskId: "TASK-2", taskRunId: "task-run-2", status: "review",
    priority: 2, labels: ["bug", "reviewed"], commentBody: "Automated bug triage completed.",
    externalRef: "local-branch:loom/TASK-2@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  }));
  await client.tasks.complete({ taskId: "TASK-1", taskRunId: "task-run-1", artifactIds: ["a1"] });
  await client.tasks.release({ taskId: "TASK-1" });
  await client.tasks.releaseReview({ taskId: "TASK-1" });
  expectType<Record<string, unknown>>(await client.taskRuns.request({
    taskId: "TASK-1",
    runner: "local-task-runner",
    capabilities: ["git"],
    closeTask: false,
    retainWorkItemClaim: true,
    // Optional task-run payload (diff+rubric) delivered verbatim to the runner.
    input: { kind: "github-review", diff: "patch", rubric: { mustPass: ["builds"] } },
  }));
  await client.taskRuns.get({ taskRunId: "task-run-1" });
  await client.taskRuns.await({ taskRunId: "task-run-1", pollMs: 500, timeoutMs: 60_000 });
  await client.taskRuns.active({ epicId: "EPIC-1", limit: 5 });
  await client.taskRuns.recoverStale({ maxAgeSeconds: 300, errorClass: "stale" });

  // roles (read-only prompt materialization for prompt agents)
  expectType<{ role: Record<string, unknown> | null; prompt: string } | null>(
    await client.roles.get({ name: "docs-assistant" }),
  );

  // connectors
  const granted: LoomConnectorCallResult = await client.connectors.github.merge({ expectedHeadSha: "sha-1" });
  expectType<"granted">(granted.decision);
  expectType<string>(granted.callId);
  await client.connectors.github.postReview({ expectedHeadSha: "sha-1" });
  await client.connectors.github.readPullRequest({ owner: "octo", repo: "hello", number: 1 });
  await client.connectors.github.listPulls();
  await client.connectors.github.compare({ base: "main", head: "topic" });
  await client.connectors.github.postIssueComment({ body: "hi" });
  await client.connectors.slack.post({ channel: "C1", text: "hi" });
  await client.connectors.slack.readConversations();
  await client.connectors.datadog.readMonitors();
  await client.connectors.datadog.readAlert();
  await client.connectors.datadog.declareIncident();
  await client.connectors.dispatch({ action: "github.pull_request.read", callSeq: 3, args: { owner: "octo" } });

  // events + workflows
  const resolved: LoomAwaitEventResult = await client.events.await({
    pattern: "approval:octo/hello#1@sha",
    actor: ["reviewer"],
    timeoutMs: 60_000,
  });
  expectType<LoomAwaitStatus>(resolved.status);
  expectType<string>(resolved.event.id);
  const listed: LoomAwaitListResult = await client.events.list();
  expectType<string>(listed.runId);
  const started: LoomWorkflowStartResult = await client.workflows.start({
    workflow: "child-flow",
    input: { anything: true },
    idempotencyKey: "key-1",
  });
  expectType<string>(started.childRunId);
  const childResult: LoomWorkflowAwaitResult = await client.workflows.await({
    childRunId: started.childRunId,
    timeoutMs: 60_000,
  });
  expectType<string | undefined>(childResult.child?.status);

  // result helpers
  const completed: LoomDriverResult = client.completed({ summary: "ok" });
  expectType<LoomDriverResultStatus>(completed.status);
  client.failed({ summary: "boom", errorClass: "driver_failed" });
  client.needsReview({ summary: "look", taskRunId: "task-run-1" });
}

export function exerciseFrozenEnums(client: LoomDriverClient): void {
  // @ts-expect-error "running" is not a frozen terminal result status
  const badStatus: LoomDriverResultStatus = "running";
  void badStatus;
  // @ts-expect-error watch event types are frozen to snapshot|taskRun|closed
  const badWatchType: LoomEpicWatchEventType = "message";
  void badWatchType;
  // @ts-expect-error error codes are frozen; unknown codes need a contract bump
  const badCode: DriverApiErrorCode = "not_a_code";
  void badCode;
  // @ts-expect-error pattern is required on events.await
  void client.events.await({ timeoutMs: 1000 });
  // @ts-expect-error github.merge requires expectedHeadSha (irreversible op)
  void client.connectors.github.merge({});
  // @ts-expect-error connectors.dispatch requires action
  void client.connectors.dispatch({});
}

export function exerciseErrorAndSuspendTypes(): void {
  const err = new DriverApiError("boom", {
    code: "token_expired",
    retryable: false,
    status: 401,
    details: { reason: "ttl" },
  });
  expectType<DriverApiErrorCode>(err.code);
  expectType<boolean>(err.retryable);
  expectType<number>(err.status);
  expectType<unknown>(err.details);

  const suspended = new WorkflowSuspended(1);
  expectType<"workflow_suspended">(suspended.type);
  expectType<number>(suspended.awaitIndex);
  expectType<"suspended_awaiting_event">(suspended.result.status);
  expectType<boolean>(isWorkflowSuspended(suspended));
}

export async function exerciseRunnerSurface(): Promise<void> {
  const runner: TaskRunClient = TaskRunClient.fromEnv({ LOOM_TASK_RUN_API_URL: "http://localhost:8080" });
  expectType<string>(runner.taskRunId);
  expectType<number | string>(runner.fencingToken);
  expectType<Record<string, unknown>>(runner.request());
  expectType<unknown>(runner.input());
  const run: TaskRun = await runner.getTaskRun();
  expectType<string>(run.task_run_id);
  expectType<string | undefined>(run.runner);
  await runner.heartbeat({ runtimeMetadata: { step: "build" } });
  await runner.appendLog({ requestId: "log-1", text: "line", stream: "stdout", timestamp: new Date() });
  await runner.logs.append({ request_id: "log-2", text: "line", timestamp: "2026-07-16T20:31:00Z" });
  const daytona: DaytonaProviderResult = await runner.daytona.execute({
    repositoryUrl: "https://github.com/octocat/Hello-World.git",
    taskPrompt: "Make a focused change.",
    backend: "codex",
    delivery: { openPullRequest: false },
  });
  expectType<"daytona">(daytona.sandbox.provider);
  // @ts-expect-error Daytona intent cannot carry provider credentials
  await runner.daytona.execute({ repositoryUrl: "https://github.com/o/r", taskPrompt: "x", backend: "codex", delivery: { openPullRequest: false }, credentials: "secret" });
  // @ts-expect-error task runners cannot retrieve plaintext provider credentials
  await runner.getRuntimeCredential({ provider: "github" });
  // @ts-expect-error no credential-returning namespace is exposed
  await runner.runtimeCredentials.get({ provider: "daytona" });
  // @ts-expect-error append replay identity is required
  await runner.logs.append({ text: "line", timestamp: new Date() });
  // @ts-expect-error immutable append timestamp is required
  await runner.logs.append({ requestId: "log-3", text: "line" });
  const handle: ArtifactHandle = await runner.declareArtifact({ type: "diff", summary: "patch" });
  await handle.upload("content", { mimeType: "text/plain" });
  await handle.finalize({ summary: "done" });
  await runner.artifacts.list({ type: "diff", limit: 10 });
  const completion: CompleteRunResponse = await runner.completeRun({ status: "completed", exitCode: 0 });
  expectType<TaskRun | undefined>(completion.task_run);
  expectType<"LOOM_TASK_RUN_API_URL">(RunnerEnv.apiUrl);
  expectType<"LOOM_TASK_RUN_REQUEST_JSON">(RunnerEnv.requestJson);

  // The serve facade is the only supported endpoint config.
  const serveRunner: TaskRunClient = TaskRunClient.fromEnv({}, { apiUrl: "http://127.0.0.1:8080" });
  expectType<string>(serveRunner.apiUrl);
}

export function exerciseRuntimeAdapterSurface(): void {
  const collector: FlueTranscriptCollector = createFlueTranscriptCollector();
  const pushed: LoomTranscriptEntry[] = collector.push({
    type: "turn_request",
    purpose: "agent",
    input: { messages: [{ role: "user", content: "hello" }] },
  });
  expectType<LoomTranscriptEntry[]>(pushed);
  expectType<LoomTranscriptEntry[]>(collector.entries);
  expectType<string>(serializeTranscriptJSONL(collector.entries));
  const directUsage: LoomTaskUsage = flueUsageToTaskUsage({
    input: 1,
    output: 2,
    cost: { total: 0.01 },
  }, { costUnit: "usd" });
  expectType<number | undefined>(directUsage.estimated_cost_usd);
  expectType<LoomTaskUsage>(flueEventsToTaskUsage([{ type: "turn", turnId: "t1", usage: { input: 1, output: 2 } }]));
}
