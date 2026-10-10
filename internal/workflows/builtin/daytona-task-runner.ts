import fs from "node:fs";
import path from "node:path";
import * as bundledDaytonaSDK from "@daytona/sdk";
import * as bundledFlueRuntime from "@flue/runtime";
import * as bundledFlueRuntimeInternal from "@flue/runtime/internal";
import { TaskRunClient } from "@loom/sdk/runner";
import {
  createFlueTranscriptCollector,
  flueUsageToTaskUsage,
  redactTranscriptEntries,
  serializeTranscriptJSONL,
} from "@loom/sdk/runtime-adapters";

// Flue HEAD (durable-streams) requires every workflow module to default-export a
// defineWorkflow() definition; a bare `export function run` no longer normalizes.
// The *workflow* agent here is a credential-free stub (model: false) — the real
// in-sandbox Flue agent this runner drives is created separately at runtime via
// bundledFlueRuntimeInternal.createFlueContext. The request arrives via env: the
// task-runner host-bridge sets LOOM_TASK_RUN_REQUEST_JSON (driver/task_bridge.go),
// which requestPayload() already reads, so the inner run() body is unchanged.
export default bundledFlueRuntime.defineWorkflow({
  agent: bundledFlueRuntime.defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: builtinInvokePayload() })),
});

function builtinInvokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD || process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}";
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

// Flue HEAD validates the workflow return value with a strict JSON check that
// rejects undefined/function/symbol/bigint (json-snapshot.cloneJsonSerializable);
// the old runtime instead JSON-encoded the result for IPC transport, silently
// dropping undefined. Round-trip through JSON to restore that behavior so optional
// result fields left undefined never throw.
function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

const DEFAULT_MODEL = "openai-codex/gpt-5.3-codex-spark";
const DEFAULT_REPO_DIR = "/tmp/loom-daytona-task-repo";
// DEMO_MODES gates the e2e-only task modes that fabricate scaffolding instead of
// implementing real task work. They stay reachable for the e2e harness only when
// explicitly enabled by environment; request input cannot open these paths.
const DEMO_MODES = new Set(["e2e-smoke", "slack-pr-chain"]);

export async function run(ctx = {}) {
  const request = requestPayload(ctx);
  const taskRunId = stringValue(request.task_run_id || request.taskRunId || process.env.LOOM_TASK_RUN_ID || "task-run");
  const taskId = stringValue(request.task_id || request.taskId || process.env.LOOM_TASK_ID);
  const logs = [];

  const mode = taskMode(request);
  if (DEMO_MODES.has(mode) && !demoModesEnabled(request)) {
    return failed(
      "daytona_demo_mode_disabled",
      `daytona task mode ${mode} is a demo-only path; set LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES=1 to enable it`,
      taskRunId,
      request,
      logs,
    );
  }
  if (booleanValue(inputValue(request, "openPullRequest")) || booleanValue(inputValue(request, "stackedPullRequests"))) {
    return failed("host_publish_required", "Daytona task runs freeze revisions; publish approved layers through the host", taskRunId, request, logs);
  }
  const flueEvents = [];
  const setupEvents = [];
  const secrets = [];
  let sandbox;
  let sandboxId = "";
  let captureAccepted = false;
  let captureAttempted = false;
  let captureContext = null;
  let captureSetup = null;

  try {
    const imports = await loadRuntimeImports();
    const model = stringValue(process.env.LOOM_FLUE_AGENT_MODEL) || DEFAULT_MODEL;
    const auth = await configureCodexAuth(imports, model, request);
    if (!auth.ok) {
      return failed("codex_auth_failed", auth.error, taskRunId, request, logs);
    }
    secrets.push(auth.accessToken, auth.refreshToken);

    const taskContext = await loadTaskContext(logs);
    let daytonaKey = "";
    try {
      daytonaKey = await readRuntimeCredential(taskContext.client, "daytona");
    } catch (error) {
      return failed("daytona_credentials_missing", errorMessage(error), taskRunId, request, logs);
    }
    if (!daytonaKey) {
      return failed("daytona_credentials_missing", "saved Daytona credential is required", taskRunId, request, logs);
    }
    secrets.push(daytonaKey);

    const repoUrl = stringValue(
      inputValue(request, "repoUrl") ||
        inputValue(request, "githubRepo") ||
        inputValue(request, "repositoryUrl") ||
        process.env.DAYTONA_REPO_URL,
    );
    if (!repoUrl) {
      return failed("daytona_repo_url_missing", "task input repoUrl or DAYTONA_REPO_URL is required for daytona-task-runner", taskRunId, request, logs);
    }

    const task = taskContext.task;
    const delivery = deliveryPlan(request, task, taskRunId);
    const sdk = imports.daytona;
    const Daytona = sdk.Daytona || (sdk.default && sdk.default.Daytona);
    if (typeof Daytona !== "function") {
      return failed("daytona_sdk_invalid", "Daytona SDK import did not expose Daytona", taskRunId, request, logs);
    }

    const daytonaConfig = { apiKey: daytonaKey };
    if (process.env.DAYTONA_API_URL) {
      daytonaConfig.apiUrl = process.env.DAYTONA_API_URL;
    }
    if (process.env.DAYTONA_TARGET) {
      daytonaConfig.target = process.env.DAYTONA_TARGET;
    }

    const client = new Daytona(daytonaConfig);
    if (numberValue(request.scheduler_attempt, 0) > 0) {
      const state = (await captureOp("capture-state", request, {})).body;
      if (state.status === "pending" && state.sandboxId) {
        sandboxId = state.sandboxId;
        captureContext = {
          request, repoUrl: state.repoUrl, repoDir: state.repoDir,
          baseSha: state.baseSha, attempt: state.attempt, taskRunId,
        };
        sandbox = await client.get(state.sandboxId);
        if (sandbox.state && sandbox.state !== "started") {
          await sandbox.start();
        }
        captureAttempted = true;
        const capture = await retryRetainedCapture(sandbox, captureContext, secrets);
        captureAccepted = capture.complete;
        if (!capture.complete) {
          return failed("capture_incomplete", "remote capture retained excluded or incomplete files", taskRunId, request, logs, sandboxId, secrets);
        }
        return {
          status: "completed", exitCode: 0,
          logs: redact("recovered remote capture " + capture.captureSha + "\n", secrets),
          runtimeMetadata: stringMetadata({
            task_runner: "daytona-task-runner", daytona_sandbox_id: sandboxId,
            remote_capture_status: "frozen", remote_capture_sha: capture.captureSha,
            remote_capture_attempt: captureContext.attempt,
            remote_capture_tree_hash: capture.treeHash,
            remote_capture_change_id: capture.changeId, remote_capture_revision: capture.revision,
          }),
        };
      }
    }
    sandbox = await client.create({
      labels: {
        loom: "epic-runner",
        runner: "daytona-task-runner",
        task_run_id: taskRunId,
      },
      autoStopInterval: numberValue(process.env.DAYTONA_AUTO_STOP_MINUTES, 15),
      autoDeleteInterval: numberValue(process.env.DAYTONA_AUTO_DELETE_MINUTES, 0),
    });
    sandboxId = stringValue(sandbox.id || sandbox.sandboxId);
    logs.push(`created Daytona sandbox ${sandboxId || "<unknown>"}`);

    const workDir = stringValue(await sandbox.getWorkDir().catch(() => "")) || "/home/daytona";
    const repoDir = stringValue(process.env.DAYTONA_REPO_DIR) || DEFAULT_REPO_DIR;
    const setup = await createHarness(imports, {
      id: `${taskRunId}-setup`,
      request,
      events: setupEvents,
      sandbox,
      cwd: workDir,
      model: false,
      name: "daytona-setup",
    });
    captureSetup = setup;

    const clone = await setup.shell(cloneCommand(repoUrl, repoDir, delivery.baseBranch), {
      timeout: numberValue(process.env.DAYTONA_CLONE_TIMEOUT_SECONDS, 180),
    });
    logs.push(commandLog(delivery.baseBranch ? "git clone " + delivery.baseBranch : "git clone", clone));
    if (clone.exitCode !== 0) {
      return failed("daytona_repo_clone_failed", textTail(clone.stdout + clone.stderr), taskRunId, request, logs, sandboxId, secrets);
    }

    const head = await setup.shell("git -C " + shellQuote(repoDir) + " rev-parse HEAD", { timeout: 30 });
    if (head.exitCode !== 0 || !head.stdout.trim()) {
      return failed("daytona_repo_head_failed", textTail(head.stdout + head.stderr), taskRunId, request, logs, sandboxId, secrets);
    }
    captureContext = { request, repoUrl, repoDir, baseSha: head.stdout.trim(), taskRunId };
    const registration = await captureOp("capture-register", request, {
      repoUrl, repoDir, baseSha: captureContext.baseSha, sandboxId,
    });
    captureContext.attempt = stringValue(registration.body.attempt);
    const leakProbe = await setup.shell(sandboxLeakProbeCommand(), { timeout: 30 });
    const leakedEnvCount = numberValue(leakProbe.stdout.trim(), 0);
    if (leakedEnvCount !== 0) {
      return failed("daytona_sandbox_env_leak", "sensitive runner environment reached Daytona sandbox", taskRunId, request, logs, sandboxId, secrets);
    }

    const transcriptCollector = createFlueTranscriptCollector();
    const harness = await createHarness(imports, {
      id: taskRunId,
      request,
      events: flueEvents,
      transcriptCollector,
      sandbox,
      cwd: repoDir,
      model,
      name: "daytona-task-agent",
    });
    const flueSession = `task-${taskRunId}`;
    const session = await harness.session(flueSession);
    const prompt = buildPrompt(request, task, repoDir);
    const response = await session.prompt(prompt);
    captureAttempted = true;
    const capture = await captureRemoteWork(setup, sandbox, captureContext, secrets);
    captureAccepted = capture.complete;
    if (!capture.complete) {
      const result = failed("capture_incomplete", "remote capture retained excluded or incomplete files", taskRunId, request, logs, sandboxId, secrets);
      result.runtimeMetadata.remote_capture_status = "retained";
      result.runtimeMetadata.remote_capture_reason = result.errorMessage;
      return result;
    }
    const transcriptEntries = redactTranscriptEntries(transcriptCollector.entries, secrets);
    const transcriptJSONL = serializeTranscriptJSONL(transcriptEntries);
    const usage = flueUsageToTaskUsage(response && response.usage, { costUnit: "usd" });

    logs.push("codex/flue response:");
    logs.push(textTail(stringValue(response && response.text), 2000));
    logs.push("remote capture " + capture.captureSha);

    return {
      status: "completed",
      exitCode: 0,
      ...usage,
      logs: redact(logs.join("\n") + "\n", secrets),
      transcript: transcriptJSONL,
      transcript_entries: transcriptEntries,
      runtimeMetadata: stringMetadata({
        task_runner: "daytona-task-runner",
        runtime_strategy: "flue-daytona-codex",
        runner: request.runner || "daytona-task-runner",
        runner_kind: request.runner_kind || request.runnerKind || process.env.LOOM_TASK_RUNNER_KIND,
        runner_entrypoint: request.runner_entrypoint || request.runnerEntrypoint || process.env.LOOM_TASK_RUNNER_ENTRYPOINT,
        task_id: taskId,
        phase: "flue_agent",
        loom_task_session_id: "flue-" + taskRunId,
        flue_session: flueSession,
        flue_harness: "daytona-task-agent",
        model,
        auth_provider: auth.provider,
        auth_configured: auth.configured,
        cost_unit: "usd",
        cost_source: "flue_prompt_response_usage",
        sandbox_provider: "daytona",
        sandbox_id: sandboxId,
        daytona_sandbox_id: sandboxId,
        daytona_workdir: workDir,
        daytona_repo_url: repoUrl,
        daytona_repo_dir: repoDir,
        daytona_repo_head: head.stdout.trim(),
        remote_capture_status: "frozen",
        remote_capture_attempt: captureContext.attempt,
        remote_capture_sha: capture.captureSha,
        remote_capture_tree_hash: capture.treeHash,
        remote_capture_change_id: capture.changeId,
        remote_capture_revision: capture.revision,
        daytona_sandbox_env_leak_count: "0",
        response_text: redact(textTail(stringValue(response && response.text), 1000), secrets),
      }),
    };
  } catch (error) {
    if (captureContext && captureSetup && !captureAttempted) {
      try {
        captureAttempted = true;
        await captureRemoteWork(captureSetup, sandbox, { ...captureContext, outcome: "failed" }, secrets);
      } catch (captureError) {
        logs.push("failed-run capture: " + errorMessage(captureError));
      }
    }
    const result = failed("daytona_task_runner_failed", errorMessage(error), taskRunId, request, logs, sandboxId, secrets);
    if (sandboxId && captureContext) {
      result.runtimeMetadata.remote_capture_status = captureContext ? "pending" : "retained";
      result.runtimeMetadata.remote_capture_reason = result.errorMessage;
      if (captureContext) {
        result.runtimeMetadata.remote_capture_attempt = stringValue(captureContext.attempt);
        result.runtimeMetadata.remote_capture_base_sha = captureContext.baseSha;
        result.runtimeMetadata.remote_capture_repo_url = captureContext.repoUrl;
        result.runtimeMetadata.daytona_repo_dir = captureContext.repoDir;
      }
    }
    return result;
  } finally {
    if (sandbox && captureAccepted && process.env.KEEP_DAYTONA_SANDBOX !== "1") {
      try {
        await sandbox.delete(60);
      } catch (error) {
        console.error("warning: failed to delete Daytona sandbox " + sandboxId + ": " + errorMessage(error));
      }
    }
  }
}

async function captureOp(operation, request, params) {
  const base = stringValue(process.env.LOOM_TASK_RUN_API_URL).replace(/\/$/, "");
  const workspace = stringValue(request.workspace_key || request.workspaceKey || process.env.LOOM_DRIVER_WORKSPACE);
  if (!base || !workspace) {
    throw new Error("remote capture requires the serve task-run API and workspace identity");
  }
  const headers = {
    "Content-Type": "application/json",
    Authorization: "Bearer " + stringValue(process.env.LOOM_TASK_RUN_LEASE_TOKEN),
    "X-Loom-Task-Run-Id": stringValue(request.task_run_id || request.taskRunId || process.env.LOOM_TASK_RUN_ID),
    "X-Loom-Task-Run-Node-Id": stringValue(process.env.LOOM_TASK_RUN_NODE_ID),
    "X-Loom-Task-Run-Lease-Id": stringValue(process.env.LOOM_TASK_RUN_LEASE_ID),
    "X-Loom-Task-Run-Fencing-Token": stringValue(process.env.LOOM_TASK_RUN_FENCING_TOKEN),
  };
  const route = `/api/workspaces/${encodeURIComponent(workspace)}/task-run`;
  const response = await fetch(base + route + "/" + operation, {
    method: "POST", headers, body: JSON.stringify(params),
  });
  const body = await response.json();
  if (!response.ok) {
    throw new Error("remote capture " + operation + ": " + stringValue(body.message || body.error || response.status));
  }
  return { body, proxyURL: base + route + "/capture.git" };
}

async function captureRemoteWork(setup, sandbox, input, secrets) {
  const prepared = await captureOp("capture-token", input.request, {
    repoUrl: input.repoUrl, baseSha: input.baseSha,
  });
  const token = stringValue(prepared.body.token);
  if (!token || !prepared.body.ref) {
    throw new Error("host did not return a capture token and ref");
  }
  secrets.push(token);
  input.attempt = stringValue(prepared.body.attempt);
  const tokenPath = "/tmp/loom-capture-token-" + Math.random().toString(16).slice(2);
  await sandbox.fs.uploadFile(Buffer.from(token, "utf8"), tokenPath);
  const args = {
    repo: input.repoDir, workspace: stringValue(input.request.workspace_key || input.request.workspaceKey || process.env.LOOM_DRIVER_WORKSPACE),
    attempt: stringValue(prepared.body.attempt), ref: stringValue(prepared.body.ref),
    proxyURL: prepared.proxyURL, tokenPath,
  };
  const command = "node -e " + shellQuote(REMOTE_CAPTURE_SCRIPT) + " -- " + shellQuote(JSON.stringify(args));
  const result = await setup.shell(command, { timeout: numberValue(process.env.DAYTONA_CAPTURE_TIMEOUT_SECONDS, 180) });
  if (result.exitCode !== 0) {
    throw new Error("remote capture push failed: " + textTail(result.stderr || result.stdout, 1000));
  }
  let capture;
  try {
    capture = JSON.parse(result.stdout.trim());
  } catch {
    throw new Error("remote capture returned invalid manifest");
  }
  if (capture.pushError) {
    await captureOp("capture-pending", input.request, {
      repoUrl: input.repoUrl, baseSha: input.baseSha, captureSha: capture.captureSha,
      treeHash: capture.treeHash, complete: capture.complete,
      outcome: input.outcome || "failed", reason: capture.pushError,
    });
    throw new Error("provider capture push failed; sandbox retained: " + capture.pushError);
  }
  const finalized = await captureOp("capture-finalize", input.request, {
    repoUrl: input.repoUrl, baseSha: input.baseSha, captureSha: capture.captureSha,
    treeHash: capture.treeHash, complete: capture.complete,
    outcome: input.outcome || (capture.complete ? "completed" : "failed"),
  });
  return { ...capture, ...finalized.body };
}

export async function retryRetainedCapture(sandbox, input, secrets) {
  const api = new DaytonaSandboxApi(sandbox);
  const setup = { shell: (command, options) => api.exec(command, { cwd: input.repoDir, timeout: options.timeout }) };
  return captureRemoteWork(setup, sandbox, input, secrets);
}

const REMOTE_CAPTURE_SCRIPT = String.raw`
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const cp = require("node:child_process");
const input = JSON.parse(process.argv[1]);
const repo = input.repo;
const git = (args, env) => {
  const clean = { ...process.env, ...env, GIT_TERMINAL_PROMPT: "0", GIT_ASKPASS: "/bin/false", SSH_ASKPASS: "/bin/false" };
  delete clean.GIT_CONFIG_PARAMETERS;
  return cp.execFileSync("git", args, { cwd: repo, env: clean, maxBuffer: 64 * 1024 * 1024 });
};
const value = (args, env) => git(args, env).toString("utf8").trim();
const paths = (args) => git(args).toString("utf8").split("\0").filter(Boolean);
const secret = (name) => name.split("/").some((part) => {
  const lower = part.toLowerCase();
  // Mirrors capture.SecretPath: only SSH key names also match with ".pub".
  return lower.startsWith(".env") || lower.endsWith(".pem") || lower.endsWith(".key") ||
    ["id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"].includes(lower.replace(/\.pub$/, "")) ||
    [".npmrc", ".netrc", "credentials.json"].includes(lower);
});
const head = value(["rev-parse", "HEAD"]);
const tracked = new Set(paths(["ls-tree", "-r", "--name-only", "-z", "HEAD"]));
const changed = paths(["diff", "--no-renames", "--name-only", "-z", "HEAD"]);
const untracked = paths(["ls-files", "--others", "--exclude-standard", "-z"]);
const ignored = paths(["ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z"]);
const ignoredIndex = new Set(paths(["ls-files", "--cached", "--ignored", "--exclude-standard", "-z"]));
const entries = [];
const stage = [];
let total = 0;
let complete = true;
for (const name of [...new Set([...changed, ...untracked])].sort()) {
  const existing = tracked.has(name);
  let info;
  try { info = fs.lstatSync(path.join(repo, name)); } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  let category = "captured";
  let reason = "";
  if (!existing && ignoredIndex.has(name)) category = "listed";
  else if (!existing && secret(name)) category = "secret_suspect";
  else if (!info && !existing) { category = "incomplete"; reason = "new file disappeared"; }
  else if (info && !info.isFile() && !info.isSymbolicLink()) { category = "incomplete"; reason = "unsupported file type"; }
  else if (info && info.isFile() && !(info.mode & 0o444)) { category = "incomplete"; reason = "unreadable file"; }
  else if (info && info.size > 100 * 1024 * 1024) { category = "incomplete"; reason = "per-file cap exceeded"; }
  else if (info && total + info.size > 2 * 1024 * 1024 * 1024) { category = "incomplete"; reason = "capture cap exceeded"; }
  if (category === "captured") { stage.push(name); total += info ? info.size : 0; }
  if (category === "secret_suspect" || category === "incomplete") complete = false;
  entries.push({ path: name, class: category, size: info ? info.size : 0, ...(reason ? { reason } : {}) });
}
// Ignored files are listed with their size, never captured (D18), like Capture.
const sizeOf = (target) => {
  const info = fs.lstatSync(target);
  if (!info.isDirectory()) return info.size;
  let size = 0;
  for (const child of fs.readdirSync(target)) size += sizeOf(path.join(target, child));
  return size;
};
for (const name of ignored) {
  try {
    entries.push({ path: name, class: "listed", size: sizeOf(path.join(repo, name.replace(/\/$/, ""))) });
  } catch (error) {
    entries.push({ path: name, class: "incomplete", size: 0, reason: String(error.message || error) });
    complete = false;
  }
}
entries.sort((left, right) => left.path.localeCompare(right.path));
const manifest = { workspace: input.workspace, attempt: input.attempt, entries, complete, retained: !complete };
const gitPath = value(["rev-parse", "--git-path", "loom/capture"]);
const manifestDir = path.isAbsolute(gitPath) ? gitPath : path.join(repo, gitPath);
fs.mkdirSync(manifestDir, { recursive: true, mode: 0o700 });
fs.writeFileSync(path.join(manifestDir, input.workspace + "-" + input.attempt + ".json"), JSON.stringify(manifest));
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "loom-capture-"));
try {
  const index = path.join(scratch, "index");
  const indexEnv = { GIT_INDEX_FILE: index };
  git(["read-tree", "HEAD"], indexEnv);
  if (stage.length) {
    const pathspec = path.join(scratch, "paths");
    fs.writeFileSync(pathspec, stage.join("\0") + "\0");
    git(["add", "-A", "--pathspec-from-file=" + pathspec, "--pathspec-file-nul"], indexEnv);
  }
  const treeHash = value(["write-tree"], indexEnv);
  const headTree = value(["rev-parse", "HEAD^{tree}"]);
  const captureSha = treeHash === headTree ? head : value(["commit-tree", treeHash, "-p", head, "-m", "loom: remote uncommitted work"], {
    GIT_AUTHOR_NAME: "Loom", GIT_AUTHOR_EMAIL: "loom@localhost",
    GIT_COMMITTER_NAME: "Loom", GIT_COMMITTER_EMAIL: "loom@localhost",
  });
  git(["update-ref", input.ref, captureSha]);
  const token = fs.readFileSync(input.tokenPath, "utf8").trim();
  let pushError = "";
  try {
  git(["push", input.proxyURL, captureSha + ":" + input.ref], {
      GIT_CONFIG_COUNT: "2", GIT_CONFIG_KEY_0: "credential.helper",
      GIT_CONFIG_VALUE_0: "", GIT_CONFIG_KEY_1: "http.extraHeader",
      GIT_CONFIG_VALUE_1: "Authorization: Bearer " + token,
    });
  } catch (error) {
    pushError = String(error.stderr || error.message || error).trim().slice(-1000);
  }
  process.stdout.write(JSON.stringify({ ...manifest, captureSha, treeHash, pushError }));
} finally {
  fs.rmSync(scratch, { recursive: true, force: true });
  try { fs.unlinkSync(input.tokenPath); } catch {}
}
`;

export function remoteCaptureScript() { return REMOTE_CAPTURE_SCRIPT; }

async function loadRuntimeImports() {
  const runtimeImport = stringValue(process.env.FLUE_RUNTIME_IMPORT);
  const internalImport = stringValue(process.env.FLUE_RUNTIME_INTERNAL_IMPORT);
  const daytonaImport = stringValue(process.env.DAYTONA_SDK_IMPORT);
  const [runtime, internal, daytona] = await Promise.all([
    runtimeImport ? import(runtimeImport) : Promise.resolve(bundledFlueRuntime),
    internalImport ? import(internalImport) : Promise.resolve(bundledFlueRuntimeInternal),
    daytonaImport ? import(daytonaImport) : Promise.resolve(bundledDaytonaSDK),
  ]);
  return { runtime, internal, daytona };
}

function requestPayload(ctx) {
  if (ctx && ctx.payload && typeof ctx.payload === "object") {
    return ctx.payload;
  }
  try {
    return JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}");
  } catch {
    return {};
  }
}

async function createHarness(imports, options) {
  const ctx = imports.internal.createFlueContext({
    id: options.id,
    payload: options.request,
    env: process.env,
    agentConfig: {
      systemPrompt: "",
      skills: {},
      model: undefined,
      resolveModel: imports.internal.resolveModel,
    },
    createDefaultEnv: async () => {
      throw new Error("daytona-task-runner requires explicit Daytona sandbox");
    },
    defaultStore: new imports.internal.InMemorySessionStore(),
  });
  ctx.setEventCallback((event) => {
    options.events.push(event);
    if (options.transcriptCollector) {
      options.transcriptCollector.push(event);
    }
  });
  const agent = imports.runtime.createAgent(() => ({
    model: options.model,
    cwd: options.cwd,
    sandbox: daytonaSandbox(imports.runtime, options.sandbox, options.cwd),
    instructions: "You are a focused coding agent running for a Loom child TaskRun inside an isolated Daytona sandbox.",
  }));
  return ctx.initializeRootHarness(agent);
}

function daytonaSandbox(runtime, sandbox, cwd) {
  return {
    async createSessionEnv() {
      return runtime.createSandboxSessionEnv(new DaytonaSandboxApi(sandbox), cwd);
    },
  };
}

class DaytonaSandboxApi {
  constructor(sandbox) {
    this.sandbox = sandbox;
  }
  async readFile(filePath) {
    const buffer = await this.sandbox.fs.downloadFile(filePath);
    return buffer.toString("utf-8");
  }
  async readFileBuffer(filePath) {
    const buffer = await this.sandbox.fs.downloadFile(filePath);
    return new Uint8Array(buffer);
  }
  async writeFile(filePath, content) {
    const buffer = typeof content === "string" ? Buffer.from(content, "utf-8") : Buffer.from(content);
    await this.sandbox.fs.uploadFile(buffer, filePath);
  }
  async stat(filePath) {
    const info = await this.sandbox.fs.getFileDetails(filePath);
    return {
      isFile: !info.isDir,
      isDirectory: info.isDir || false,
      isSymbolicLink: false,
      size: info.size || 0,
      mtime: info.modTime ? new Date(info.modTime) : new Date(),
    };
  }
  async readdir(filePath) {
    const entries = await this.sandbox.fs.listFiles(filePath);
    return entries.map((entry) => entry.name).filter(Boolean);
  }
  async exists(filePath) {
    try {
      await this.sandbox.fs.getFileDetails(filePath);
      return true;
    } catch {
      return false;
    }
  }
  async mkdir(filePath, options) {
    if (options && options.recursive) {
      await this.exec("mkdir -p " + shellQuote(filePath));
      return;
    }
    await this.sandbox.fs.createFolder(filePath, "755");
  }
  async rm(filePath, options) {
    await this.sandbox.fs.deleteFile(filePath, options && options.recursive);
  }
  async exec(command, options = {}) {
    const response = await this.sandbox.process.executeCommand(
      command,
      options.cwd,
      sandboxGitEnv(options.env),
      options.timeout,
    );
    return {
      stdout: response.result || "",
      stderr: "",
      exitCode: response.exitCode || 0,
    };
  }
}

export function sandboxGitEnv(extra = {}) {
  const env = { ...extra };
  env.GIT_CONFIG_PARAMETERS = "";
  env.GIT_CONFIG_COUNT = "1";
  env.GIT_CONFIG_KEY_0 = "credential.helper";
  env.GIT_CONFIG_VALUE_0 = "";
  env.GIT_TERMINAL_PROMPT = "0";
  env.GIT_ASKPASS = "/bin/false";
  env.SSH_ASKPASS = "/bin/false";
  return env;
}

async function configureCodexAuth(imports, model, request) {
  let resolved;
  try {
    resolved = imports.internal.resolveModel(model);
  } catch (error) {
    return { ok: false, error: "failed to resolve model " + model + ": " + errorMessage(error) };
  }
  if (resolved.provider !== "openai-codex") {
    return { ok: true, provider: resolved.provider, configured: false };
  }
  const auth = loadCodexAuth();
  if (!auth) {
    return { ok: false, error: "openai-codex model selected but no Codex auth token was found" };
  }
  let apiKey = auth.accessToken;
  if (booleanValue(inputValue(request, "refreshCodexAuth")) || tokenExpiresSoon(apiKey)) {
    if (!auth.refreshToken) {
      return { ok: false, error: "Codex access token is expired and no refresh token was found" };
    }
    try {
      apiKey = await refreshCodexAccessToken(auth.refreshToken);
    } catch (error) {
      return { ok: false, error: errorMessage(error) };
    }
  }
  imports.runtime.registerProvider("openai-codex", { apiKey });
  return {
    ok: true,
    provider: resolved.provider,
    configured: true,
    accessToken: apiKey,
    refreshToken: auth.refreshToken,
  };
}

function loadCodexAuth() {
  for (const file of codexAuthFileCandidates()) {
    if (!file || !fs.existsSync(file)) {
      continue;
    }
    try {
      const auth = JSON.parse(fs.readFileSync(file, "utf8"));
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
        return { accessToken, refreshToken };
      }
    } catch {
      // Try the next candidate.
    }
  }
  return null;
}

function codexAuthFileCandidates() {
  const home = process.env.HOME || "";
  const codexHome = process.env.CODEX_HOME || "";
  return unique([
    process.env.LOOM_CODEX_AUTH_FILE,
    process.env.CODEX_AUTH_FILE,
    codexHome ? path.join(codexHome, "auth.json") : "",
    home ? path.join(home, ".codex", "auth.json") : "",
    "/root/.codex-rw/auth.json",
    "/root/.codex/auth.json",
  ]);
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
    throw new Error("Codex token refresh failed (" + response.status + "): " + (text || response.statusText));
  }
  const json = await response.json();
  if (!json || typeof json.access_token !== "string" || !json.access_token) {
    throw new Error("Codex token refresh response did not include access_token");
  }
  return json.access_token;
}

function tokenExpiresSoon(token, skewMs = 60000) {
  const payload = decodeJWTPayload(token);
  if (!payload || !Number.isFinite(payload.exp)) {
    return false;
  }
  return payload.exp * 1000 <= Date.now() + skewMs;
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

async function readRuntimeCredential(client, provider) {
  let apiError = null;
  if (client && client.runtimeCredentials && typeof client.runtimeCredentials.get === "function") {
    try {
      const credential = await client.runtimeCredentials.get({ provider });
      const value = stringValue(credential && credential.value);
      if (value) {
        return value;
      }
    } catch (error) {
      apiError = error;
    }
  }

  const fileValue = readRuntimeCredentialFile(provider);
  if (fileValue) {
    return fileValue;
  }

  if (apiError) {
    throw apiError;
  }
  throw new Error("@loom/sdk/runner runtime credential API is unavailable");
}

function readRuntimeCredentialFile(provider) {
  const fileEnv = provider === "daytona"
    ? process.env.DAYTONA_CREDENTIAL_FILE
      : "";
  const filePath = stringValue(fileEnv);
  if (!filePath) {
    return "";
  }
  return fs.readFileSync(filePath, "utf8").trim();
}

export function deliveryPlan(request, task, taskRunId) {
  const mode = taskMode(request);
  // Lineage carrier injected by the host bridge for a stacked epic task: the
  // canonical output branch + the predecessor base ref, computed from the host
  // stack store (which the sandbox cannot read). When present it is authoritative
  // — the daytona runner pushes the exact same canonical branch the local runner
  // and the publisher use, so the topology matches across runtimes.
  const lineage = lineageCarrier(request);
  const openPullRequest = !!lineage || mode === "slack-pr-chain" || booleanValue(inputValue(request, "openPullRequest"));
  const configuredBase = stringValue(
    inputValue(request, "baseBranch") ||
      inputValue(request, "targetBranch"),
  );
  const rootBaseBranch = openPullRequest ? (configuredBase || "main") : configuredBase;
  const taskId = stringValue(request.task_id || request.taskId || task && task.id || "task");
  const driverRunId = stringValue(request.driver_run_id || request.driverRunId || inputValue(request, "driverRunId") || taskRunId);
  const stacked = lineage
    ? true
    : openPullRequest && booleanValue(defaultValue(inputValue(request, "stackedPullRequests"), mode === "slack-pr-chain" ? "1" : "0"));
  const dependencyIds = blockingDependencyIds(task);
  const baseTaskId = stringValue(inputValue(request, "prBaseTaskId") || inputValue(request, "baseTaskId")) ||
    (dependencyIds.length === 1 ? dependencyIds[0] : "") ||
    (mode === "slack-pr-chain" ? previousSequentialTaskId(taskId) : "");
  const branch = lineage && lineage.outputBranch
    ? lineage.outputBranch
    : taskBranchName(driverRunId, taskId);
  const baseBranch = lineage && lineage.baseRef
    ? lineage.baseRef
    : (stacked && baseTaskId ? taskBranchName(driverRunId, baseTaskId) : rootBaseBranch);
  return {
    mode,
    openPullRequest,
    branch,
    baseBranch,
    rootBaseBranch,
    stacked,
    dependencyIds,
    baseTaskId,
    stackId: lineage ? stringValue(lineage.stackId) : "",
  };
}

// lineageCarrier returns the host-injected stack lineage for this task, or null
// when the task is not stacked. Shaped by internal/driver TaskLineage:
// { stackId, baseRef, outputBranch }.
function lineageCarrier(request) {
  const raw = inputValue(request, "lineage");
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const outputBranch = stringValue(raw.outputBranch);
  if (!outputBranch) {
    return null;
  }
  return { stackId: stringValue(raw.stackId), baseRef: stringValue(raw.baseRef), outputBranch };
}

function taskMode(request) {
  return stringValue(inputValue(request, "mode") || process.env.DAYTONA_TASK_MODE);
}

function demoModesEnabled(_request) {
  return stringValue(process.env.LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES) === "1";
}

function taskBranchName(driverRunId, taskId) {
  const prefix = stringValue(process.env.DAYTONA_PR_BRANCH_PREFIX) || "loom/slack-pr-chain";
  return [prefix, safeGitRefPart(driverRunId || "run"), safeGitRefPart(taskId || "task")].join("/");
}

function safeGitRefPart(value) {
  return String(value || "item")
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/[.][.]+/g, ".")
    .replace(/^[./-]+|[./-]+$/g, "") || "item";
}

function blockingDependencyIds(task) {
  const dependencies = task && Array.isArray(task.dependencies) ? task.dependencies : [];
  return dependencies
    .filter((dep) => {
      const depType = stringValue(dep && (dep.type || dep.dep_type || dep.depType));
      return !depType || depType === "blocks";
    })
    .map((dep) => stringValue(dep && (dep.depends_on_id || dep.dependsOnId || dep.id)))
    .filter(Boolean);
}

function previousSequentialTaskId(taskId) {
  const match = stringValue(taskId).match(/^(.*?)(\d+)$/);
  if (!match) {
    return "";
  }
  const number = Number(match[2]);
  if (!Number.isInteger(number) || number <= 2) {
    return "";
  }
  return match[1] + String(number - 1);
}

async function loadTaskContext(logs) {
  let client;
  try {
    client = TaskRunClient.fromEnv();
    const task = await client.getTask();
    logs.push("loaded Loom task context through @loom/sdk/runner");
    return { client, task };
  } catch (error) {
    logs.push("warning: task context lookup failed: " + errorMessage(error));
    return { client: client || null, task: null };
  }
}

export function cloneCommand(repoUrl, repoDir, branch) {
  if (/^https?:\/\//i.test(repoUrl)) {
    const parsed = new URL(repoUrl);
    if (parsed.username || parsed.password) {
      throw new Error("repository URL must not contain credentials");
    }
  }
  // Full clone (NOT --depth 1): a stacked task bases on its predecessor's branch,
  // and the PR diff + the post-drain reconcile's merge-base checks need the real
  // base SHA and history, which a shallow tip does not provide.
  const parts = [
    "rm -rf " + shellQuote(repoDir),
    "git clone" +
      (branch ? " --branch " + shellQuote(branch) : "") +
      " " + shellQuote(repoUrl) + " " + shellQuote(repoDir),
    "git -C " + shellQuote(repoDir) + " config remote.origin.pushurl loom-no-push://task-copy",
    "git -C " + shellQuote(repoDir) + " config credential.helper ''",
  ];
  return parts.join(" && ");
}

function buildPrompt(request, task, repoDir) {
  const mode = taskMode(request);
  // Explicit task instruction for paths without a driver TaskRun to load the task
  // from (single-shot invocations, the daemon leaf). Takes precedence over the mode
  // templates so the sandbox agent knows exactly what to implement.
  const explicit = stringValue(inputValue(request, "taskPrompt"));
  if (explicit) {
    return [
      "You are implementing a task in a Loom-managed git repository.",
      "Repository cwd: " + repoDir,
      "",
      explicit,
      "",
      "Work directly in the repository; keep the change focused and minimal.",
      "Do not print environment variables or credentials.",
      "Return a concise summary of the files you changed.",
    ].join("\n");
  }
  if (mode === "e2e-smoke") {
    return [
      "You are executing a Loom Daytona/Codex e2e smoke task.",
      "Repository cwd: " + repoDir,
      "Task ID: " + stringValue(request.task_id || request.taskId),
      "",
      "Create or update `.loom-e2e/" + safeName(request.task_id || request.taskId || "task") + ".md`.",
      "The file should contain a short note that this task ran inside the Daytona-backed Flue runner.",
      "Do not print environment variables or credentials.",
      "Run `git status --short` and return a concise summary.",
    ].join("\n");
  }
  if (mode === "slack-pr-chain") {
    return [
      "You are implementing one task in a realistic Loom epic-runner e2e.",
      "The target product is a tiny Slack-style collaboration app.",
      "Repository cwd: " + repoDir,
      "Task run: " + stringValue(request.task_run_id || request.taskRunId),
      "Workspace: " + stringValue(request.workspace_key || request.workspaceKey),
      "",
      "Task context:",
      JSON.stringify(task || { task_id: request.task_id || request.taskId }, null, 2),
      "",
      "Implement only this task's slice. Preserve existing behavior from earlier stacked PR branches.",
      "Use simple static web code and Node built-in tests unless the repo already has a different toolchain.",
      "Before finishing, run `npm test` if package.json defines it; otherwise run the most relevant validation command available.",
      "Do not commit, push, open PRs, update Loom issues, or print environment variables or credentials.",
      "Return a concise summary of files changed and validation results.",
    ].join("\n");
  }

  return [
    "You are implementing one child task from a Loom epic runner workflow.",
    "Repository cwd: " + repoDir,
    "Task run: " + stringValue(request.task_run_id || request.taskRunId),
    "Workspace: " + stringValue(request.workspace_key || request.workspaceKey),
    "",
    "Task context:",
    JSON.stringify(task || { task_id: request.task_id || request.taskId }, null, 2),
    "",
    "Work directly in the repository. Keep the change focused on this task.",
    "Use Git only to inspect work. Do not commit, push, create worktrees, apply patches, clean, or reset --hard; Loom captures a revision for review.",
    "Do not update or close Loom issues yourself; the workflow driver records task completion.",
    "Do not print environment variables or credentials.",
    "Before finishing, run relevant validation commands if they are available.",
    "Return a concise summary of files changed and validation results.",
  ].join("\n");
}

function failed(errorClass, message, taskRunId, request, logs = [], sandboxId = "", secrets = []) {
  return {
    status: "failed",
    exitCode: 1,
    errorClass,
    errorMessage: redact(textTail(message), secrets),
    logs: redact(logs.concat([errorClass + ": " + message]).join("\n") + "\n", secrets),
    runtimeMetadata: stringMetadata({
      task_runner: "daytona-task-runner",
      runtime_strategy: "flue-daytona-codex",
      runner: request.runner || "daytona-task-runner",
      task_id: request.task_id || request.taskId || "",
      daytona_sandbox_id: sandboxId,
      phase: errorClass,
    }),
  };
}

function commandLog(label, result) {
  return [
    label + " exit=" + numberValue(result.exitCode, 0),
    textTail(result.stdout || "", 1000),
    textTail(result.stderr || "", 1000),
  ].filter(Boolean).join("\n");
}

export function sandboxLeakProbeCommand() {
  // The canonical list lives in internal/driver/testdata/sensitive-env-names.json
  // (vendored byte-for-byte from meta-harness's contract/sensitive-env-names.json).
  // The entries below are its `runner_infra ++ provider_credentials` union, in that
  // exact order, and both internal/driver/sensitive_env_contract_test.go and this
  // module's .test.mjs fail if the two diverge — so this is no longer a list kept in
  // step by hand. To CHANGE it, edit the artifact in meta-harness, re-run
  // scripts/sync-sensitive-env-names.sh --to <this repo>, and land both PRs.
  //
  // Kept as a literal, not read from the JSON: this module is bundled into the
  // builtin-workflow bundle and must stay self-contained with no filesystem
  // dependency. Names are emitted split on "_" so the probe source carries no
  // literal secret name.
  return "node -e " + shellQuote([
    "const names=[",
    // runner_infra
    "['DAYTONA','API','KEY'],",
    "['LOOM','TASK','RUN','LEASE','TOKEN'],",
    "['LOOM','DRIVER','TASK','RUNNER','CMD','JSON'],",
    // provider_credentials — mirrors env.go trustedLocalProviderCredentials
    "['GITHUB','TOKEN'],",
    "['GH','TOKEN'],",
    "['CODEX','HOME'],",
    "['ANTHROPIC','API','KEY'],",
    "['OPENAI','API','KEY'],",
    "['CODEX','API','KEY'],",
    "['GEMINI','API','KEY'],",
    "['GOOGLE','API','KEY'],",
    "['GOOGLE','APPLICATION','CREDENTIALS'],",
    "['CURSOR','API','KEY'],",
    "['CLAUDE','CODE','OAUTH','TOKEN'],",
    "].map((parts)=>parts.join('_'));",
    "let count=0;",
    "for (const name of names) if (process.env[name]) count++;",
    "console.log(count);",
  ].join(""));
}

function inputValue(request, key) {
  const input = request && request.input && typeof request.input === "object" ? request.input : {};
  return input[key];
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

function redact(value, secrets) {
  let text = String(value || "");
  for (const secret of secrets.filter(Boolean)) {
    text = text.split(secret).join("[redacted]");
  }
  return text;
}

function shellQuote(value) {
  return "'" + String(value).replace(/'/g, "'\\''") + "'";
}

function safeName(value) {
  return String(value || "unknown").replace(/[^A-Za-z0-9_.-]/g, "_");
}

function textTail(value, max = 4000) {
  const text = String(value || "").trim();
  return text.length <= max ? text : text.slice(text.length - max);
}

function errorMessage(error) {
  return error && error.message ? error.message : String(error || "unknown error");
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

function defaultValue(value, fallback) {
  const text = stringValue(value);
  return text ? text : fallback;
}

function unique(values) {
  return [...new Set(values.filter(Boolean))];
}
