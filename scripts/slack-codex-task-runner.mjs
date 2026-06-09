#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

const [, , worktreeArg, codexHomeArg, serverURLArg] = process.argv;

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
  if (request.provider_profile !== "flue-local") {
    fatal(4, `unexpected provider profile ${request.provider_profile}`);
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

function writeFile(file, data) {
  fs.writeFileSync(file, data ?? "", "utf8");
}

function textTail(value, max = 4000) {
  const trimmed = String(value || "").trim();
  if (trimmed.length <= max) {
    return trimmed;
  }
  return trimmed.slice(trimmed.length - max);
}

function collectUsage(value, usage = {}) {
  if (!value || typeof value !== "object") {
    return usage;
  }
  for (const key of [
    "input_tokens",
    "output_tokens",
    "cache_read_tokens",
    "cache_write_tokens",
    "total_tokens",
  ]) {
    if (Number.isFinite(value[key])) {
      usage[key] = value[key];
    }
  }
  for (const key of [
    "inputTokens",
    "outputTokens",
    "cacheReadTokens",
    "cacheWriteTokens",
    "totalTokens",
  ]) {
    if (Number.isFinite(value[key])) {
      const snake = key.replace(/[A-Z]/g, (ch) => `_${ch.toLowerCase()}`);
      usage[snake] = value[key];
    }
  }
  for (const child of Object.values(value)) {
    if (child && typeof child === "object") {
      collectUsage(child, usage);
    }
  }
  return usage;
}

function parseUsage(stdout) {
  const usage = {};
  for (const line of String(stdout || "").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed.startsWith("{")) {
      continue;
    }
    try {
      collectUsage(JSON.parse(trimmed), usage);
    } catch {
      // Non-JSON diagnostics can appear in stream output.
    }
  }
  return usage;
}

function resultPayload(request, status, exitCode, logsRef, artifactsRef, metadata, usage, error) {
  const payload = {
    status,
    exit_code: exitCode,
    logs_ref: logsRef,
    artifacts_ref: artifactsRef,
    input_tokens: usage.input_tokens || 0,
    output_tokens: usage.output_tokens || 0,
    cache_read_tokens: usage.cache_read_tokens || 0,
    cache_write_tokens: usage.cache_write_tokens || 0,
    runtime_metadata: {
      task_runner: "slack-codex-epic-runner",
      provider_profile: request.provider_profile,
      workspace_key: request.workspace_key,
      driver_run_id: request.driver_run_id,
      task_id: request.task_id,
      ...metadata,
    },
  };
  if (error) {
    payload.error_class = error.error_class;
    payload.error_message = error.error_message;
  }
  return payload;
}

function emit(payload) {
  console.log(JSON.stringify(payload));
}

const request = parseRequest();
validateRequest(request);

if (!worktreeArg) {
  fatal(6, "slack worktree path argument is required");
}

const worktreePath = path.resolve(worktreeArg);
const codexHome = codexHomeArg || "/root/.codex-rw";
const serverURL = serverURLArg || "http://127.0.0.1:8080";

if (!fs.existsSync(worktreePath)) {
  fatal(6, `slack worktree path does not exist: ${worktreePath}`);
}

const runDir = path.join("/tmp/loom-slack-codex-task-runs", safeName(request.task_run_id));
fs.mkdirSync(runDir, { recursive: true });

const promptPath = path.join(runDir, "prompt.txt");
const issuePath = path.join(runDir, "issue.json");
const codexStdoutPath = path.join(runDir, "codex.stdout.jsonl");
const codexStderrPath = path.join(runDir, "codex.stderr.log");
const patchPath = path.join(runDir, "git.diff");
const statPath = path.join(runDir, "git.diffstat");
const logsRef = `file://${codexStdoutPath}`;
const artifactsRef = `file://${runDir}`;

const issueLookup = run(
  "loom",
  [
    "--workspace",
    request.workspace_key,
    "data",
    "--server",
    serverURL,
    "-o",
    "json",
    "show",
    request.task_id,
  ],
  {
    cwd: worktreePath,
    env: {
      ...process.env,
      LOOM_WORKSPACE: request.workspace_key,
    },
  },
);

if (issueLookup.status !== 0) {
  writeFile(path.join(runDir, "issue-lookup.stderr.log"), issueLookup.stderr);
  emit(
    resultPayload(
      request,
      "failed",
      issueLookup.status || 1,
      `file://${path.join(runDir, "issue-lookup.stderr.log")}`,
      artifactsRef,
      { phase: "issue_lookup", worktree_path: worktreePath },
      {},
      {
        error_class: "issue_lookup_failed",
        error_message: textTail(issueLookup.stderr || issueLookup.error?.message || "issue lookup failed"),
      },
    ),
  );
  process.exit(0);
}

let issue;
try {
  issue = JSON.parse(issueLookup.stdout);
} catch (error) {
  writeFile(path.join(runDir, "issue-lookup.stdout.log"), issueLookup.stdout);
  emit(
    resultPayload(
      request,
      "failed",
      1,
      `file://${path.join(runDir, "issue-lookup.stdout.log")}`,
      artifactsRef,
      { phase: "issue_lookup", worktree_path: worktreePath },
      {},
      {
        error_class: "issue_lookup_decode_failed",
        error_message: error.message,
      },
    ),
  );
  process.exit(0);
}

writeFile(issuePath, JSON.stringify(issue, null, 2));

const prompt = `You are implementing one child task from Loom's Slack epic-runner demo.

Repository: ${worktreePath}
Task run: ${request.task_run_id}
Workspace: ${request.workspace_key}

Issue JSON:
${JSON.stringify(issue, null, 2)}

Work directly in the repository. Keep the change focused on this issue, preserve the dependency-free plain HTML/CSS/JavaScript structure, and do not update or close Loom issues yourself. The workflow driver records task completion.

Before finishing, run:
- npm test
- npm run build

Return a concise summary of the files changed and the validation results.`;

writeFile(promptPath, prompt);

const codex = run(
  "codex",
  ["exec", "--json", "--dangerously-bypass-approvals-and-sandbox", prompt],
  {
    cwd: worktreePath,
    env: {
      ...process.env,
      CODEX_HOME: codexHome,
      HOME: process.env.HOME || "/root",
      TERM: process.env.TERM || "xterm-256color",
    },
  },
);

writeFile(codexStdoutPath, codex.stdout);
writeFile(codexStderrPath, codex.stderr);

const diff = run("git", ["diff", "--", "."], { cwd: worktreePath, env: process.env });
const diffStat = run("git", ["diff", "--stat", "--", "."], { cwd: worktreePath, env: process.env });
writeFile(patchPath, diff.stdout);
writeFile(statPath, diffStat.stdout);

const usage = parseUsage(codex.stdout);
const metadata = {
  phase: "codex_exec",
  worktree_path: worktreePath,
  codex_home: codexHome,
  issue_title: issue.title || "",
  diffstat_path: statPath,
  patch_path: patchPath,
};

if (codex.error || codex.status !== 0) {
  emit(
    resultPayload(
      request,
      "failed",
      codex.status || 1,
      logsRef,
      artifactsRef,
      metadata,
      usage,
      {
        error_class: "codex_exec_failed",
        error_message: textTail(codex.stderr || codex.error?.message || "codex exec failed"),
      },
    ),
  );
  process.exit(0);
}

emit(resultPayload(request, "completed", 0, logsRef, artifactsRef, metadata, usage));
