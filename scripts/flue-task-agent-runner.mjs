#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { configureProvider, createAgent } from "@flue/runtime";
import { local } from "@flue/runtime/node";
import { createFlueContext, InMemorySessionStore, resolveModel } from "@flue/runtime/internal";

import {
  createTranscriptCollector,
  flueUsageToTaskUsage,
  serializeTranscriptJSONL,
} from "./flue-event-transcript.mjs";

const [, , worktreeArg, codexHomeArg, serverURLArg] = process.argv;
let activeRequest = null;

function fatal(code, message) {
  console.error(message);
  process.exit(code);
}

function parseRequest() {
  const raw = process.env.LOOM_TASK_RUN_REQUEST_JSON;
  if (!raw) {
    fatal(2, "LOOM_TASK_RUN_REQUEST_JSON is required");
  }
  try {
    return JSON.parse(raw);
  } catch (error) {
    fatal(2, `failed to parse LOOM_TASK_RUN_REQUEST_JSON: ${error.message}`);
  }
}

function validateRequest(request) {
  for (const field of ["workspace_key", "driver_run_id", "task_run_id", "task_id"]) {
    if (!request[field]) {
      fatal(3, `task runner request is missing ${field}`);
    }
  }
  const runnerKind = String(request.runner_kind || process.env.LOOM_TASK_RUNNER_KIND || "");
  if (runnerKind && runnerKind !== "flue-workflow") {
    fatal(4, `unexpected runner kind ${runnerKind}`);
  }
  if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
    fatal(5, "task-run lease token did not reach the task runner");
  }
}

function safeName(value) {
  return String(value || "unknown").replace(/[^A-Za-z0-9_.-]/g, "_");
}

function run(command, args, options = {}) {
  return spawnSync(command, args, {
    cwd: options.cwd,
    env: options.env,
    encoding: "utf8",
    maxBuffer: 50 * 1024 * 1024,
  });
}

function textTail(value, max = 4000) {
  const trimmed = String(value || "").trim();
  return trimmed.length <= max ? trimmed : trimmed.slice(trimmed.length - max);
}

function stringMetadata(values = {}) {
  const out = {};
  for (const [key, value] of Object.entries(values || {})) {
    if (value === undefined || value === null) {
      continue;
    }
    if (typeof value === "string") {
      out[key] = value;
      continue;
    }
    out[key] = String(value);
  }
  return out;
}

function unique(values) {
  return [...new Set(values.filter(Boolean))];
}

function codexAuthFileCandidates(codexHome) {
  const home = process.env.HOME || "";
  return unique([
    process.env.LOOM_CODEX_AUTH_FILE,
    process.env.CODEX_AUTH_FILE,
    codexHome ? path.join(codexHome, "auth.json") : "",
    process.env.CODEX_HOME ? path.join(process.env.CODEX_HOME, "auth.json") : "",
    process.env.LOOM_CODEX_AUTH_HOME ? path.join(process.env.LOOM_CODEX_AUTH_HOME, "auth.json") : "",
    home ? path.join(home, ".codex", "auth.json") : "",
    "/root/.codex/auth.json",
  ]);
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function loadCodexAuth(codexHome) {
  for (const file of codexAuthFileCandidates(codexHome)) {
    if (!fs.existsSync(file)) {
      continue;
    }
    const auth = readJSON(file);
    const tokens = auth && typeof auth === "object" ? auth.tokens : null;
    const piOAuth = auth && typeof auth === "object" ? auth["openai-codex"] : null;
    const accessToken =
      (tokens && typeof tokens.access_token === "string" && tokens.access_token) ||
      (piOAuth && typeof piOAuth.access === "string" && piOAuth.access) ||
      "";
    const refreshToken =
      (tokens && typeof tokens.refresh_token === "string" && tokens.refresh_token) ||
      (piOAuth && typeof piOAuth.refresh === "string" && piOAuth.refresh) ||
      "";
    if (accessToken) {
      return { accessToken, refreshToken, file };
    }
  }
  return null;
}

function decodeJWTPayload(token) {
  const parts = String(token || "").split(".");
  if (parts.length !== 3) {
    return null;
  }
  try {
    const payload = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const padded = payload.padEnd(payload.length + ((4 - (payload.length % 4)) % 4), "=");
    return JSON.parse(Buffer.from(padded, "base64").toString("utf8"));
  } catch {
    return null;
  }
}

function tokenExpiresSoon(token, skewMs = 60_000) {
  const payload = decodeJWTPayload(token);
  if (!payload || !Number.isFinite(payload.exp)) {
    return false;
  }
  return payload.exp * 1000 <= Date.now() + skewMs;
}

async function refreshCodexAccessToken(refreshToken) {
  const response = await fetch("https://auth.openai.com/oauth/token", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "refresh_token",
      refresh_token: refreshToken,
      client_id: "app_EMoamEEZ73f0CkXaXp7hrann",
    }),
  });
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(`Codex token refresh failed (${response.status}): ${text || response.statusText}`);
  }
  const json = await response.json();
  if (!json || typeof json.access_token !== "string" || !json.access_token) {
    throw new Error("Codex token refresh response did not include access_token");
  }
  return json.access_token;
}

async function configureCodexAuth(model, codexHome) {
  let resolved;
  try {
    resolved = resolveModel(model);
  } catch (error) {
    return { ok: false, error: `failed to resolve model ${model}: ${error.message}` };
  }
  if (resolved.provider !== "openai-codex") {
    return { ok: true, provider: resolved.provider, configured: false };
  }
  const auth = loadCodexAuth(codexHome);
  if (!auth) {
    return { ok: false, error: "openai-codex model selected but no Codex auth token was found" };
  }
  let apiKey = auth.accessToken;
  if (tokenExpiresSoon(apiKey)) {
    if (!auth.refreshToken) {
      return { ok: false, error: "Codex access token is expired and no refresh token was found" };
    }
    try {
      apiKey = await refreshCodexAccessToken(auth.refreshToken);
    } catch (error) {
      return { ok: false, error: error.message || String(error) };
    }
  }
  configureProvider("openai-codex", { apiKey });
  return { ok: true, provider: resolved.provider, configured: true, authFile: auth.file };
}

function lookupIssue(request, worktreePath, serverURL, runDir) {
  if (!serverURL) {
    return { ok: true, issue: null };
  }
  const lookup = run(
    "loom",
    ["--workspace", request.workspace_key, "data", "--server", serverURL, "-o", "json", "show", request.task_id],
    {
      cwd: worktreePath,
      env: {
        ...process.env,
        LOOM_WORKSPACE: request.workspace_key,
      },
    },
  );
  if (lookup.status !== 0) {
    fs.writeFileSync(path.join(runDir, "issue-lookup.stderr.log"), lookup.stderr || "", "utf8");
    return {
      ok: false,
      error_class: "issue_lookup_failed",
      error_message: textTail(lookup.stderr || lookup.error?.message || "issue lookup failed"),
    };
  }
  try {
    const issue = JSON.parse(lookup.stdout || "null");
    fs.writeFileSync(path.join(runDir, "issue.json"), JSON.stringify(issue, null, 2), "utf8");
    return { ok: true, issue };
  } catch (error) {
    fs.writeFileSync(path.join(runDir, "issue-lookup.stdout.log"), lookup.stdout || "", "utf8");
    return {
      ok: false,
      error_class: "issue_lookup_decode_failed",
      error_message: error.message,
    };
  }
}

function buildPrompt(request, worktreePath, issue) {
  const issueBlock = issue ? JSON.stringify(issue, null, 2) : `Task ID: ${request.task_id}`;
  return `You are implementing one child task from a Loom workflow.

Repository: ${worktreePath}
Task run: ${request.task_run_id}
Workspace: ${request.workspace_key}

Task context:
${issueBlock}

Work directly in the repository. Keep the change focused on this task. Do not update or close Loom issues yourself; the workflow driver records task completion.

Before finishing, run the project's relevant validation commands if they are available. Return a concise summary of files changed and validation results.`;
}

function resultPayload(request, status, exitCode, fields = {}) {
  const { runtime_metadata: runtimeMetadata = {}, ...rest } = fields;
  return {
    status,
    exit_code: exitCode,
    runtime_metadata: stringMetadata({
      task_runner: "flue-task-agent-runner",
      runner: request.runner || process.env.LOOM_TASK_RUNNER || "",
      runner_ref: request.runner_ref || process.env.LOOM_TASK_RUNNER_REF || "",
      runner_kind: request.runner_kind || process.env.LOOM_TASK_RUNNER_KIND || "",
      runner_entrypoint: request.runner_entrypoint || process.env.LOOM_TASK_RUNNER_ENTRYPOINT || "",
      provider_profile: request.provider_profile,
      workspace_key: request.workspace_key,
      driver_run_id: request.driver_run_id,
      task_id: request.task_id,
      runtime: "flue",
      backend: "flue",
      ...runtimeMetadata,
    }),
    ...rest,
  };
}

async function main() {
  const request = parseRequest();
  activeRequest = request;
  validateRequest(request);

  const worktreePath = path.resolve(worktreeArg || process.env.LOOM_WORKTREE_PATH || process.cwd());
  if (!fs.existsSync(worktreePath)) {
    fatal(6, `worktree path does not exist: ${worktreePath}`);
  }

  if (codexHomeArg) {
    process.env.CODEX_HOME = codexHomeArg;
  }

  const runDir = path.join("/tmp/loom-flue-task-agent-runs", safeName(request.task_run_id));
  fs.mkdirSync(runDir, { recursive: true });

  const sessionID = `flue-${request.task_run_id}`;
  const harnessName = "task-agent";
  const model = process.env.LOOM_FLUE_AGENT_MODEL || "openai-codex/gpt-5.3-codex-spark";
  const serverURL = serverURLArg || process.env.LOOM_TASK_RUN_SERVER_URL || process.env.LOOM_SERVER_URL || "";
  const authConfig = await configureCodexAuth(model, codexHomeArg);
  if (!authConfig.ok) {
    console.log(JSON.stringify(resultPayload(request, "failed", 1, {
      error_class: "codex_auth_failed",
      error_message: textTail(authConfig.error),
      runtime_metadata: {
        phase: "codex_auth",
        flue_session: sessionID,
        flue_harness: harnessName,
        worktree_path: worktreePath,
        model,
      },
    })));
    return;
  }
  const issueResult = lookupIssue(request, worktreePath, serverURL, runDir);
  if (!issueResult.ok) {
    console.log(JSON.stringify(resultPayload(request, "failed", 1, {
      error_class: issueResult.error_class,
      error_message: issueResult.error_message,
      logs_ref: `file://${path.join(runDir, "issue-lookup.stderr.log")}`,
      runtime_metadata: {
        phase: "issue_lookup",
        flue_session: sessionID,
        flue_harness: harnessName,
        worktree_path: worktreePath,
      },
    })));
    return;
  }

  const collector = createTranscriptCollector();
  const flueEvents = [];
  const ctx = createFlueContext({
    id: sessionID,
    payload: request,
    env: process.env,
    agentConfig: {
      systemPrompt: "",
      skills: {},
      model: undefined,
      resolveModel,
    },
    createDefaultEnv: async () => local({ cwd: worktreePath, env: sandboxEnv() }).createSessionEnv(),
    defaultStore: new InMemorySessionStore(),
  });
  ctx.setEventCallback((event) => {
    flueEvents.push(event);
    collector.push(event);
  });

  const agent = createAgent(() => ({
    model,
    sandbox: local({ cwd: worktreePath, env: sandboxEnv() }),
    instructions: "You are a focused coding agent running inside Loom child task execution.",
  }));

  const prompt = buildPrompt(request, worktreePath, issueResult.issue);
  fs.writeFileSync(path.join(runDir, "prompt.txt"), prompt, "utf8");

  let response;
  try {
    const harness = await ctx.init(agent, { name: harnessName });
    const session = await harness.session(sessionID);
    response = await session.prompt(prompt);
  } catch (error) {
    const logs = runnerLogs(flueEvents, error);
    console.log(JSON.stringify(resultPayload(request, "failed", 1, {
      logs,
      transcript: serializeTranscriptJSONL(collector.entries),
      transcript_entries: collector.entries,
      error_class: "flue_agent_failed",
      error_message: textTail(error && error.message ? error.message : String(error)),
      runtime_metadata: {
        phase: "flue_agent",
        flue_session: sessionID,
        flue_harness: harnessName,
        worktree_path: worktreePath,
        model,
        auth_provider: authConfig.provider,
        auth_configured: authConfig.configured,
      },
    })));
    return;
  }

  const usage = flueUsageToTaskUsage(response && response.usage, { costUnit: "usd" });
  console.log(JSON.stringify(resultPayload(request, "completed", 0, {
    ...usage,
    logs: runnerLogs(flueEvents),
    transcript: serializeTranscriptJSONL(collector.entries),
    transcript_entries: collector.entries,
    runtime_metadata: {
      phase: "flue_agent",
      flue_session: sessionID,
      flue_harness: harnessName,
      worktree_path: worktreePath,
      model,
      auth_provider: authConfig.provider,
      auth_configured: authConfig.configured,
      response_text: textTail(response && response.text ? response.text : "", 1000),
    },
  })));
}

function sandboxEnv() {
  return {
    LOOM_TASK_RUN_ID: process.env.LOOM_TASK_RUN_ID,
    LOOM_TASK_ID: process.env.LOOM_TASK_ID,
    LOOM_DRIVER_WORKSPACE: process.env.LOOM_DRIVER_WORKSPACE,
  };
}

function runnerLogs(events, error) {
  const lines = events.map((event) => JSON.stringify(event));
  if (error) {
    lines.push(JSON.stringify({
      type: "runner_error",
      message: error && error.message ? error.message : String(error),
      stack: error && error.stack ? error.stack : "",
    }));
  }
  return lines.join("\n") + (lines.length ? "\n" : "");
}

main().catch((error) => {
  const request = activeRequest || {};
  console.log(JSON.stringify({
    status: "failed",
    exit_code: 1,
    error_class: "flue_task_runner_crashed",
    error_message: textTail(error && error.message ? error.message : String(error)),
    runtime_metadata: {
      task_runner: "flue-task-agent-runner",
      runner: request.runner || process.env.LOOM_TASK_RUNNER || "",
      runner_ref: request.runner_ref || process.env.LOOM_TASK_RUNNER_REF || "",
      runner_kind: request.runner_kind || process.env.LOOM_TASK_RUNNER_KIND || "",
      runner_entrypoint: request.runner_entrypoint || process.env.LOOM_TASK_RUNNER_ENTRYPOINT || "",
      runtime: "flue",
      backend: "flue",
    },
  }));
});
