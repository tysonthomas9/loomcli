import { defineAgent, defineWorkflow } from "@flue/runtime";
import { DaytonaProviderSchemaV1, LoomAPIError, TaskRunClient } from "@loom/sdk/runner";

// This workflow is intentionally provider-blind. It submits one bounded,
// secret-free execution intent through the lease-authenticated TaskRun facade;
// only loom serve can resolve Daytona or GitHub credentials and operate the
// provider.
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: builtinInvokePayload() })),
});

export async function run(ctx = {}) {
  const request = requestPayload(ctx);
  const taskRunId = stringValue(request.task_run_id || request.taskRunId || process.env.LOOM_TASK_RUN_ID || "task-run");
  const taskId = stringValue(request.task_id || request.taskId || process.env.LOOM_TASK_ID);
  let client;
  let task;
  try {
    client = TaskRunClient.fromEnv();
    task = await client.getTask();
  } catch (error) {
    return failed(
      classifyBrokerError(error, "daytona_task_context_failed"),
      errorMessage(error),
      taskRunId,
      taskId,
    );
  }

  const repositoryUrl = stringValue(
    inputValue(request, "repoUrl") ||
      inputValue(request, "githubRepo") ||
      inputValue(request, "repositoryUrl"),
  );
  if (!repositoryUrl) {
    return failed(
      "daytona_repo_url_missing",
      "task input repoUrl is required for daytona-task-runner",
      taskRunId,
      taskId,
    );
  }

  const delivery = deliveryPlan(request, task, taskRunId);
  const intent = {
    schemaVersion: DaytonaProviderSchemaV1,
    repositoryUrl,
    baseRef: delivery.baseBranch,
    taskPrompt: taskPrompt(request, task, taskRunId),
    backend: "codex",
    model: stringValue(inputValue(request, "model")),
    mode: taskMode(request),
    delivery: {
      openPullRequest: delivery.openPullRequest,
      baseBranch: delivery.baseBranch,
      outputBranch: delivery.branch,
      draft: booleanValue(inputValue(request, "draftPullRequest")),
    },
  };

  try {
    const receipt = await client.daytona.execute(intent);
    return taskResult(receipt, request, taskRunId, taskId);
  } catch (error) {
    return failed(
      classifyBrokerError(error, "daytona_provider_broker_failed"),
      errorMessage(error),
      taskRunId,
      taskId,
    );
  }
}

function taskResult(receipt, request, taskRunId, taskId) {
  if (!receipt || receipt.schemaVersion !== DaytonaProviderSchemaV1) {
    return failed(
      "daytona_provider_result_invalid",
      "host-owned Daytona provider broker returned an unsupported response",
      taskRunId,
      taskId,
    );
  }
  const usage = receipt.usage || {};
  const sandbox = receipt.sandbox || {};
  const patch = receipt.patch || null;
  const pullRequest = receipt.pullRequest || null;
  return {
    status: receipt.status,
    exitCode: numberValue(receipt.exitCode, receipt.status === "completed" ? 0 : 1),
    errorClass: stringValue(receipt.errorClass),
    errorMessage: stringValue(receipt.errorMessage),
    logs: stringValue(receipt.logs),
    transcript: stringValue(receipt.transcript),
    transcript_entries: normalizeTranscriptEntries(receipt.transcriptEntries),
    ...(patch && typeof patch.content === "string" ? { patch: patch.content } : {}),
    ...(patch ? {
      patchBaseRef: stringValue(patch.baseRef),
      patchSummary: stringValue(patch.diffStat) || "Daytona remote repository patch",
      patchMimeType: "text/x-diff",
    } : {}),
    input_tokens: numberValue(usage.inputTokens, 0),
    output_tokens: numberValue(usage.outputTokens, 0),
    cache_read_tokens: numberValue(usage.cacheReadTokens, 0),
    cache_write_tokens: numberValue(usage.cacheWriteTokens, 0),
    estimated_cost_usd: numberValue(usage.estimatedCostUsd, 0),
    runtimeMetadata: stringMetadata({
      task_runner: "daytona-task-runner",
      runtime_strategy: "host-opaque-provider-broker",
      runner: request.runner || "daytona-task-runner",
      task_id: taskId,
      task_run_id: taskRunId,
      sandbox_provider: sandbox.provider,
      sandbox_id: sandbox.id,
      daytona_sandbox_id: sandbox.id,
      sandbox_cwd: sandbox.cwd,
      daytona_workdir: sandbox.workDir,
      daytona_repo_dir: sandbox.cwd,
      repository_ref: sandbox.repoRef,
      daytona_repo_url: inputValue(request, "repoUrl") || inputValue(request, "repositoryUrl"),
      daytona_repo_head: sandbox.repoRef,
      patch_base_ref: patch && patch.baseRef,
      patch_head_sha: patch && patch.headSha,
      pull_request_url: pullRequest && pullRequest.url,
      pull_request_number: pullRequest && pullRequest.number,
      pull_request_base: pullRequest && pullRequest.baseBranch,
      pull_request_head: pullRequest && pullRequest.headBranch,
      pull_request_commit: pullRequest && pullRequest.commitSha,
      github_pr_url: pullRequest && pullRequest.url,
      github_pr_number: pullRequest && pullRequest.number,
      github_pr_base: pullRequest && pullRequest.baseBranch,
      github_pr_head: pullRequest && pullRequest.headBranch,
      github_pr_commit: pullRequest && pullRequest.commitSha,
    }),
  };
}

function normalizeTranscriptEntries(entries) {
  if (!Array.isArray(entries)) {
    return [];
  }
  return entries.map((entry) => ({
    seq: numberValue(entry && entry.sequence, 0),
    timestamp: stringValue(entry && entry.timestamp),
    role: stringValue(entry && entry.role),
    type: stringValue(entry && entry.type),
    text: stringValue(entry && entry.text) || undefined,
    tool_name: stringValue(entry && entry.toolName) || undefined,
    tool_use_id: stringValue(entry && entry.toolUseId) || undefined,
    output: stringValue(entry && entry.output) || undefined,
    uuid: stringValue(entry && entry.uuid) || undefined,
  }));
}

function classifyBrokerError(error, fallback) {
  if (!(error instanceof LoomAPIError)) {
    return fallback;
  }
  switch (error.code) {
    case "lease_denied":
    case "not_owner":
      return "daytona_provider_lease_denied";
    case "unavailable":
      return "daytona_provider_broker_unavailable";
    case "invalid":
      return "daytona_provider_intent_invalid";
    default:
      return fallback;
  }
}

export function deliveryPlan(request, task, taskRunId) {
  const lineage = lineageCarrier(request);
  const openPullRequest = !!lineage ||
    taskMode(request) === "slack-pr-chain" ||
    booleanValue(inputValue(request, "openPullRequest"));
  const rootBase = stringValue(
    inputValue(request, "baseBranch") ||
      inputValue(request, "targetBranch") ||
      "main",
  );
  const taskId = stringValue(request.task_id || request.taskId || task && task.id || "task");
  const driverRunId = stringValue(
    request.driver_run_id ||
      request.driverRunId ||
      inputValue(request, "driverRunId") ||
      taskRunId,
  );
  return {
    openPullRequest,
    branch: lineage && lineage.outputBranch
      ? lineage.outputBranch
      : taskBranchName(driverRunId, taskId),
    baseBranch: lineage && lineage.baseRef ? lineage.baseRef : rootBase,
  };
}

function taskPrompt(request, task, taskRunId) {
  const explicit = stringValue(inputValue(request, "taskPrompt"));
  if (explicit) {
    return explicit;
  }
  const title = stringValue(task && task.title);
  const description = stringValue(task && (task.description || task.body));
  const design = stringValue(task && task.design);
  return [
    "Implement this Loom task inside the admitted repository.",
    "Task run: " + taskRunId,
    title ? "Title: " + title : "",
    description ? "\nDescription:\n" + description : "",
    design ? "\nDesign:\n" + design : "",
    "\nKeep the change focused, run relevant validation, and do not print environment variables or credentials.",
  ].filter(Boolean).join("\n");
}

function lineageCarrier(request) {
  const raw = inputValue(request, "lineage");
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const outputBranch = stringValue(raw.outputBranch);
  if (!outputBranch) {
    return null;
  }
  return {
    baseRef: stringValue(raw.baseRef),
    outputBranch,
  };
}

function taskBranchName(driverRunId, taskId) {
  return ["loom/daytona", safeGitRefPart(driverRunId || "run"), safeGitRefPart(taskId || "task")].join("/");
}

function safeGitRefPart(value) {
  return String(value || "item")
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/[.][.]+/g, ".")
    .replace(/^[./-]+|[./-]+$/g, "") || "item";
}

function taskMode(request) {
  return stringValue(inputValue(request, "mode"));
}

function inputValue(request, key) {
  const input = request && request.input && typeof request.input === "object" && !Array.isArray(request.input)
    ? request.input
    : {};
  return input[key];
}

function requestPayload(ctx) {
  if (ctx && ctx.payload && typeof ctx.payload === "object" && !Array.isArray(ctx.payload)) {
    return ctx.payload;
  }
  return builtinInvokePayload();
}

function builtinInvokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD || process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}";
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

function failed(errorClass, errorMessage, taskRunId, taskId) {
  return {
    status: "failed",
    exitCode: 1,
    errorClass,
    errorMessage,
    logs: `${errorClass}: ${errorMessage}\n`,
    runtimeMetadata: stringMetadata({
      task_runner: "daytona-task-runner",
      runtime_strategy: "host-opaque-provider-broker",
      task_run_id: taskRunId,
      task_id: taskId,
      phase: errorClass,
    }),
  };
}

function stringMetadata(values) {
  const out = {};
  for (const [key, value] of Object.entries(values || {})) {
    if (value === undefined || value === null || String(value).trim() === "") {
      continue;
    }
    out[key] = String(value);
  }
  return out;
}

function stringValue(value) {
  return value === undefined || value === null ? "" : String(value).trim();
}

function numberValue(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function booleanValue(value) {
  switch (stringValue(value).toLowerCase()) {
    case "1":
    case "true":
    case "yes":
    case "on":
      return true;
    default:
      return false;
  }
}

function errorMessage(error) {
  return error && error.message ? error.message : String(error || "unknown error");
}

function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}
