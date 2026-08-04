#!/usr/bin/env node
import { fork } from "node:child_process";
import path from "node:path";
import { pathToFileURL } from "node:url";

function requestFromEnv() {
  const raw = process.env.LOOM_TASK_RUN_REQUEST_JSON || "";
  if (!raw.trim()) {
    throw new Error("LOOM_TASK_RUN_REQUEST_JSON is required");
  }
  return JSON.parse(raw);
}

function firstNonEmpty(...values) {
  for (const value of values) {
    const text = String(value || "").trim();
    if (text) {
      return text;
    }
  }
  return "";
}

function failure(errorClass, error) {
  return {
    status: "failed",
    exit_code: 1,
    error_class: errorClass,
    error_message: error && error.message ? error.message : String(error || "task runner failed"),
    runtime_metadata: {
      task_runner_invoker: "loom-task-runner-invoker",
    },
  };
}

const TERMINAL_STATUSES = new Set(["completed", "failed", "cancelled"]);

// validateBridgeResult enforces the strict result-validation algorithm (design
// §4.1) shared with the Go launcher JS. A runner result is only trusted when it
// is a non-empty object carrying a terminal status with a consistent exit code.
// Anything empty/null/`{}`/missing/unknown/non-terminal — and any `completed`
// with a nonzero exit — is INVALID and rewritten to a failed `invalid_task_result`.
// Status is NEVER defaulted to `completed`; a missing result is failure, not success.
function validateBridgeResult(result) {
  if (!result || typeof result !== "object" || Array.isArray(result) || Object.keys(result).length === 0) {
    return invalidResult("runner returned an empty or non-object result");
  }
  const out = { ...result };
  const status = typeof out.status === "string" ? out.status.trim() : "";
  if (!status || !TERMINAL_STATUSES.has(status)) {
    return invalidResult(`runner returned ${status ? `unknown status ${status}` : "no status"}`);
  }
  out.status = status;
  const rawExit = out.exit_code ?? out.exitCode;
  let exit;
  if (rawExit === undefined || rawExit === null) {
    exit = undefined;
  } else {
    const n = Number(rawExit);
    if (!Number.isFinite(n)) {
      return invalidResult(`runner reported a non-numeric exit code ${JSON.stringify(rawExit)}`);
    }
    exit = n;
  }
  if (status === "completed") {
    if (exit !== undefined && exit !== 0) {
      return invalidResult(`runner reported status completed with nonzero exit code ${exit}`);
    }
    out.exit_code = exit === undefined ? 0 : exit;
  } else {
    out.exit_code = exit === undefined ? 1 : exit;
  }
  delete out.exitCode;
  return out;
}

function invalidResult(reason) {
  return {
    status: "failed",
    exit_code: 1,
    error_class: "invalid_task_result",
    error_message: reason,
  };
}

function normalizeBridgeResult(result, request, kind, entrypoint) {
  const out = validateBridgeResult(result);
  const runtimeMetadata = out.runtime_metadata || out.runtimeMetadata || {};
  out.runtime_metadata = stringMetadata({
    ...runtimeMetadata,
    task_runner_invoker: "loom-task-runner-invoker",
    runner: firstNonEmpty(request.runner, process.env.LOOM_TASK_RUNNER),
    runner_kind: kind,
    runner_entrypoint: entrypoint,
  });
  delete out.runtimeMetadata;
  return out;
}

function stringMetadata(values = {}) {
  const out = {};
  for (const [key, value] of Object.entries(values || {})) {
    if (value === undefined || value === null) {
      continue;
    }
    out[key] = typeof value === "string" ? value : String(value);
  }
  return out;
}

function resolveModuleEntrypoint(entrypoint) {
  if (!entrypoint) {
    throw new Error("node-module runner entrypoint is required");
  }
  if (path.isAbsolute(entrypoint)) {
    return entrypoint;
  }
  const root = firstNonEmpty(process.env.LOOM_TASK_RUNNER_BUNDLE_ROOT, process.env.LOOM_WORKTREE_PATH, process.cwd());
  return path.resolve(root, entrypoint);
}

async function runNodeModule(request, entrypoint) {
  const modulePath = resolveModuleEntrypoint(entrypoint);
  const imported = await import(pathToFileURL(modulePath).href);
  const run = imported.run || imported.default;
  if (typeof run !== "function") {
    throw new Error(`node-module runner ${entrypoint} must export run() or default`);
  }
  return await run({
    request,
    input: request.input,
    env: process.env,
    worktreePath: firstNonEmpty(process.env.LOOM_WORKTREE_PATH, process.cwd()),
  });
}

async function runFlueWorkflow(request, entrypoint) {
  const serverPath = firstNonEmpty(process.env.LOOM_TASK_RUNNER_SERVER_PATH, process.env.LOOM_FLUE_SERVER_PATH);
  const bundleRoot = firstNonEmpty(process.env.LOOM_TASK_RUNNER_BUNDLE_ROOT, process.env.LOOM_FLUE_BUNDLE_ROOT, process.cwd());
  if (!serverPath) {
    throw new Error("flue-workflow runner requires LOOM_TASK_RUNNER_SERVER_PATH");
  }
  if (!entrypoint) {
    throw new Error("flue-workflow runner entrypoint is required");
  }

  return await new Promise((resolve, reject) => {
    let settled = false;
    let invoked = false;
    const child = fork(serverPath, [], {
      cwd: bundleRoot,
      env: {
        ...process.env,
        FLUE_MODE: "local",
        FLUE_CLI_TARGET: "workflow",
        FLUE_CLI_NAME: entrypoint,
        // Flue HEAD gates one-shot IPC mode behind this explicit internal flag
        // (in addition to FLUE_CLI_TARGET + an inherited IPC channel). Without
        // it the generated entry serves HTTP on :3000 instead of performing the
        // invoke/result handshake this invoker depends on.
        FLUE_INTERNAL_CLI_IPC: "1",
      },
      stdio: ["ignore", "pipe", "pipe", "ipc"],
    });

    child.stdout?.on("data", (data) => process.stderr.write(data));
    child.stderr?.on("data", (data) => process.stderr.write(data));

    const stopChild = () => {
      try {
        child.disconnect();
      } catch {}
      if (!child.killed) {
        child.kill();
      }
    };

    const finish = (value) => {
      if (settled) {
        return;
      }
      settled = true;
      stopChild();
      // Pass the raw result through unchanged (including null/`{}`): strict
      // validation in normalizeBridgeResult decides terminal status. A missing
      // result must surface as `invalid_task_result`, never a fake completion.
      resolve(value);
    };

    const fail = (error) => {
      if (settled) {
        return;
      }
      settled = true;
      stopChild();
      reject(error);
    };

    child.on("message", (message) => {
      if (!message || typeof message !== "object") {
        return;
      }
      if (message.type === "ready" && !invoked) {
        invoked = true;
        child.send({
          version: 1,
          type: "invoke",
          requestId: request.task_run_id || process.env.LOOM_TASK_RUN_ID || "task-runner",
          payload: request,
        });
        return;
      }
      if (message.type === "result") {
        finish(message.result);
        return;
      }
      if (message.type === "error") {
        const error = message.error || {};
        fail(new Error(error.message || error.details || "Flue workflow runner failed"));
      }
    });

    child.on("error", fail);
    child.on("exit", (code, signal) => {
      if (settled) {
        return;
      }
      fail(new Error(`Flue workflow runner exited before result (code=${code ?? ""} signal=${signal || ""})`));
    });
  });
}

async function main() {
  const request = requestFromEnv();
  if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
    throw new Error("task-run lease token did not reach task runner invoker");
  }
  const kind = firstNonEmpty(process.env.LOOM_TASK_RUNNER_KIND, request.runner_kind);
  const entrypoint = firstNonEmpty(process.env.LOOM_TASK_RUNNER_ENTRYPOINT, request.runner_entrypoint);
  let result;
  switch (kind) {
    case "node-module":
      result = await runNodeModule(request, entrypoint);
      break;
    case "flue-workflow":
      result = await runFlueWorkflow(request, entrypoint);
      break;
    default:
      throw new Error(`unsupported runner kind ${JSON.stringify(kind)}`);
  }
  console.log(JSON.stringify(normalizeBridgeResult(result, request, kind, entrypoint)));
}

// Only auto-run when invoked as a script; importing for tests must not execute.
if (import.meta.url === pathToFileURL(process.argv[1] || "").href) {
  main().catch((error) => {
    console.log(JSON.stringify(failure("task_runner_invoker_failed", error)));
  });
}

export { normalizeBridgeResult, validateBridgeResult, invalidResult };
