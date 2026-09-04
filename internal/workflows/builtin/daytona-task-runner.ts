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
  const flueEvents = [];
  const setupEvents = [];
  const secrets = [];
  let sandbox;
  let sandboxId = "";

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
    let githubToken = "";
    try {
      githubToken = await readRuntimeCredential(taskContext.client, "github");
    } catch (error) {
      if (delivery.openPullRequest) {
        return failed("github_credentials_missing", errorMessage(error), taskRunId, request, logs);
      }
      logs.push("warning: GitHub credential lookup failed; cloning without GitHub credential: " + errorMessage(error));
    }
    if (delivery.openPullRequest && !githubToken) {
      return failed("github_credentials_missing", "saved GitHub credential is required for pull request mode", taskRunId, request, logs);
    }
    if (githubToken) {
      secrets.push(githubToken);
    }

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

    const clone = await setup.shell(cloneCommand(repoUrl, repoDir, delivery.baseBranch, githubToken), {
      timeout: numberValue(process.env.DAYTONA_CLONE_TIMEOUT_SECONDS, 180),
    });
    logs.push(commandLog(delivery.baseBranch ? "git clone " + delivery.baseBranch : "git clone", clone));
    if (clone.exitCode !== 0) {
      return failed("daytona_repo_clone_failed", textTail(clone.stdout + clone.stderr), taskRunId, request, logs, sandboxId, secrets);
    }

    const head = await setup.shell("git -C " + shellQuote(repoDir) + " rev-parse --short HEAD", { timeout: 30 });
    if (head.exitCode !== 0 || !head.stdout.trim()) {
      return failed("daytona_repo_head_failed", textTail(head.stdout + head.stderr), taskRunId, request, logs, sandboxId, secrets);
    }
    if (delivery.openPullRequest) {
      const checkout = await setup.shell(
        "git -C " + shellQuote(repoDir) + " checkout -B " + shellQuote(delivery.branch),
        { timeout: 30 },
      );
      logs.push(commandLog("git checkout task branch", checkout));
      if (checkout.exitCode !== 0) {
        return failed("daytona_branch_checkout_failed", textTail(checkout.stdout + checkout.stderr), taskRunId, request, logs, sandboxId, secrets);
      }
    }

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
    const markUntracked = await setup.shell("git -C " + shellQuote(repoDir) + " add -N -- . || true", { timeout: 30 });
    logs.push(commandLog("git add -N", markUntracked));
    const diffStat = await setup.shell("git -C " + shellQuote(repoDir) + " diff --stat -- . || true", { timeout: 30 });
    const diff = await setup.shell("git -C " + shellQuote(repoDir) + " diff --binary -- . || true", {
      timeout: numberValue(process.env.DAYTONA_DIFF_TIMEOUT_SECONDS, 60),
    });
    const patchArtifact = await uploadPatchArtifact(taskContext.client, {
      taskRunId,
      taskId,
      repoUrl,
      repoDir,
      head: head.stdout.trim(),
      diff: redact(diff.stdout || "", secrets),
      diffStat: redact(diffStat.stdout || "", secrets),
    }, logs);
    const published = delivery.openPullRequest
      ? await publishPullRequest(setup, {
          task,
          taskId,
          taskRunId,
          repoUrl,
          repoDir,
          baseBranch: delivery.baseBranch,
          branch: delivery.branch,
          githubToken,
          secrets,
          logs,
        })
      : null;
    const prArtifact = published
      ? await uploadPullRequestArtifact(taskContext.client, {
          taskRunId,
          taskId,
          repoUrl,
          baseBranch: delivery.baseBranch,
          branch: delivery.branch,
          pullRequest: published.pullRequest,
          commitSha: published.commitSha,
        }, logs)
      : null;
    const transcriptEntries = redactTranscriptEntries(transcriptCollector.entries, secrets);
    const transcriptJSONL = serializeTranscriptJSONL(transcriptEntries);
    const usage = flueUsageToTaskUsage(response && response.usage, { costUnit: "usd" });

    logs.push("codex/flue response:");
    logs.push(textTail(stringValue(response && response.text), 2000));
    logs.push("remote git diffstat:");
    logs.push(textTail(diffStat.stdout || "(no diff)", 2000));

    return {
      status: "completed",
      exitCode: 0,
      ...usage,
      logs: redact(logs.join("\n") + "\n", secrets),
      transcript: transcriptJSONL,
      transcript_entries: transcriptEntries,
      // Inline patch when there was no artifact client (daemon-leaf path).
      ...(patchArtifact && patchArtifact.inline ? { patch: patchArtifact.diff } : {}),
      artifactIds: [patchArtifact, prArtifact].filter(Boolean).map((artifact) => artifact.id).filter(Boolean),
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
        patch_artifact_id: patchArtifact && patchArtifact.id,
        github_pr_artifact_id: prArtifact && prArtifact.id,
        github_pr_url: published && published.pullRequest && published.pullRequest.html_url,
        github_pr_number: published && published.pullRequest && published.pullRequest.number,
        github_pr_head: published && delivery.branch,
        github_pr_base: published && delivery.baseBranch,
        github_pr_commit: published && published.commitSha,
        // Keys the host finalize barrier reads to record this task's stack node
        // (stackOutcome). Emitting them is what lets a dependent resolve its base.
        ...stackDeliveryMetadata({
          openPullRequest: delivery.openPullRequest,
          published,
          branch: delivery.branch,
          filesChanged: filesChangedFromDiffStat(diffStat.stdout),
        }),
        daytona_sandbox_env_leak_count: "0",
        response_text: redact(textTail(stringValue(response && response.text), 1000), secrets),
      }),
    };
  } catch (error) {
    return failed("daytona_task_runner_failed", errorMessage(error), taskRunId, request, logs, sandboxId, secrets);
  } finally {
    if (sandbox && process.env.KEEP_DAYTONA_SANDBOX !== "1") {
      try {
        await sandbox.delete(60);
      } catch (error) {
        console.error("warning: failed to delete Daytona sandbox " + sandboxId + ": " + errorMessage(error));
      }
    }
  }
}

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
      options.env,
      options.timeout,
    );
    return {
      stdout: response.result || "",
      stderr: "",
      exitCode: response.exitCode || 0,
    };
  }
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
    : provider === "github"
      ? process.env.GITHUB_TOKEN_FILE
      : "";
  const filePath = stringValue(fileEnv);
  if (!filePath) {
    return "";
  }
  return fs.readFileSync(filePath, "utf8").trim();
}

// filesChangedFromDiffStat reads the file count off a `git diff --stat` summary
// line (" 3 files changed, 5 insertions(+), 2 deletions(-)"). Returns 0 when the
// diff is empty, so an empty unit is reported as such rather than left unknown.
export function filesChangedFromDiffStat(diffStat) {
  const match = /(\d+)\s+files?\s+changed/.exec(stringValue(diffStat));
  return match ? Number(match[1]) : 0;
}

// stackDeliveryMetadata emits the keys the host finalize barrier matches on
// (internal/driver/task_worktree_resolver.go stackOutcome). Without them
// stackOutcome returns ok=false, finalizeStackNode records nothing, and the
// stack node keeps the OutputBranch it was assigned at registration — so
// BaseBranchSliding can hand a dependent task a branch that was never pushed.
export function stackDeliveryMetadata({ openPullRequest, published, branch, filesChanged }) {
  const meta = { files_changed: String(filesChanged) };
  if (published) {
    meta.delivery = "pull_request";
    meta.github_branch = branch;
    meta.github_head_sha = stringValue(published.commitSha);
    return meta;
  }
  meta.delivery = openPullRequest && filesChanged === 0
    ? "pull_request_skipped_no_changes"
    : "patch_back";
  return meta;
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

async function uploadPatchArtifact(client, input, logs) {
  const diff = input.diff === undefined || input.diff === null ? "" : String(input.diff);
  if (!diff.trim()) {
    return null;
  }
  if (!client) {
    // No @loom/sdk/runner artifact client (e.g. the daemon-leaf path, which runs as
    // a session rather than a driver TaskRun). Surface the patch INLINE on the result
    // instead of failing — mirrors local-task-runner's top-level patch. The driver
    // path always has a client and uploads as before.
    return { id: "", inline: true, diff: input.diff || "", diffStat: input.diffStat || "" };
  }
  const metadata = stringMetadata({
    task_run_id: input.taskRunId,
    task_id: input.taskId,
    runtime_strategy: "flue-daytona-codex",
    task_runner: "daytona-task-runner",
    sandbox_provider: "daytona",
    repo_url: input.repoUrl,
    repo_dir: input.repoDir,
    repo_head: input.head,
    diff_stat: textTail(input.diffStat, 1000),
  });
  const artifact = await client.artifacts.declare({
    id: "patch-" + safeName(input.taskRunId),
    type: "patch",
    taskId: input.taskId,
    summary: "Daytona remote repository patch",
    mimeType: "text/x-diff",
    metadata,
  });
  await artifact.upload(diff, { mimeType: "text/x-diff" });
  await artifact.finalize({
    summary: "Daytona remote repository patch",
    mimeType: "text/x-diff",
    sizeBytes: Buffer.byteLength(diff, "utf8"),
    metadata,
  });
  logs.push("uploaded remote patch artifact " + artifact.id);
  return artifact;
}

async function uploadPullRequestArtifact(client, input, logs) {
  if (!client || !input.pullRequest) {
    return null;
  }
  const body = JSON.stringify({
    task_run_id: input.taskRunId,
    task_id: input.taskId,
    repo_url: input.repoUrl,
    base_branch: input.baseBranch,
    head_branch: input.branch,
    commit_sha: input.commitSha,
    pull_request: input.pullRequest,
  }, null, 2) + "\n";
  const metadata = stringMetadata({
    task_run_id: input.taskRunId,
    task_id: input.taskId,
    runtime_strategy: "flue-daytona-codex",
    task_runner: "daytona-task-runner",
    repo_url: input.repoUrl,
    base_branch: input.baseBranch,
    head_branch: input.branch,
    commit_sha: input.commitSha,
    pr_url: input.pullRequest.html_url,
    pr_number: input.pullRequest.number,
  });
  const artifact = await client.artifacts.declare({
    id: "github-pr-" + safeName(input.taskRunId),
    type: "github_pull_request",
    taskId: input.taskId,
    summary: "GitHub pull request for Daytona task run",
    mimeType: "application/json",
    metadata,
  });
  await artifact.upload(body, { mimeType: "application/json" });
  await artifact.finalize({
    summary: "GitHub pull request for Daytona task run",
    mimeType: "application/json",
    sizeBytes: Buffer.byteLength(body, "utf8"),
    metadata,
  });
  logs.push("uploaded GitHub PR artifact " + artifact.id);
  return artifact;
}

async function publishPullRequest(setup, input) {
  const status = await setup.shell("git -C " + shellQuote(input.repoDir) + " status --short", { timeout: 30 });
  input.logs.push(commandLog("git status before publish", status));
  if (status.exitCode !== 0) {
    throw new Error("git status failed before publishing PR: " + textTail(status.stdout + status.stderr));
  }
  if (!status.stdout.trim()) {
    throw new Error("slack-pr-chain mode expected repository changes, but the Codex run left the worktree clean");
  }

  const authorName = stringValue(process.env.DAYTONA_GIT_AUTHOR_NAME) || "Loom Daytona Runner";
  const authorEmail = stringValue(process.env.DAYTONA_GIT_AUTHOR_EMAIL) || "loom-daytona@example.test";
  const config = await setup.shell(
    "git -C " + shellQuote(input.repoDir) + " config user.name " + shellQuote(authorName) +
      " && git -C " + shellQuote(input.repoDir) + " config user.email " + shellQuote(authorEmail),
    { timeout: 30 },
  );
  input.logs.push(commandLog("git config author", config));
  if (config.exitCode !== 0) {
    throw new Error("git author configuration failed: " + textTail(config.stdout + config.stderr));
  }

  const add = await setup.shell("git -C " + shellQuote(input.repoDir) + " add -A", { timeout: 30 });
  input.logs.push(commandLog("git add", add));
  if (add.exitCode !== 0) {
    throw new Error("git add failed: " + textTail(add.stdout + add.stderr));
  }

  const commitMessage = input.taskId + ": " + taskTitle(input.task);
  const commit = await setup.shell(
    "git -C " + shellQuote(input.repoDir) + " commit -m " + shellQuote(commitMessage),
    { timeout: numberValue(process.env.DAYTONA_COMMIT_TIMEOUT_SECONDS, 60) },
  );
  input.logs.push(commandLog("git commit", commit));
  if (commit.exitCode !== 0) {
    throw new Error("git commit failed: " + textTail(commit.stdout + commit.stderr));
  }

  const sha = await setup.shell("git -C " + shellQuote(input.repoDir) + " rev-parse HEAD", { timeout: 30 });
  if (sha.exitCode !== 0 || !sha.stdout.trim()) {
    throw new Error("git rev-parse failed after commit: " + textTail(sha.stdout + sha.stderr));
  }
  const commitSha = sha.stdout.trim();

  const push = await setup.shell(
    gitWithGitHubAuth(input.githubToken) + " -C " + shellQuote(input.repoDir) +
      " push --force-with-lease origin HEAD:refs/heads/" + shellQuote(input.branch),
    { timeout: numberValue(process.env.DAYTONA_PUSH_TIMEOUT_SECONDS, 180) },
  );
  input.logs.push(commandLog("git push " + input.branch, push));
  if (push.exitCode !== 0) {
    throw new Error("git push failed: " + textTail(push.stdout + push.stderr));
  }

  const repo = parseGitHubRepo(input.repoUrl);
  if (!repo) {
    throw new Error("DAYTONA_REPO_URL is not a github.com repository URL: " + input.repoUrl);
  }
  const pullRequest = await createOrFindPullRequest({
    token: input.githubToken,
    owner: repo.owner,
    repo: repo.repo,
    title: commitMessage,
    body: pullRequestBody(input, commitSha),
    head: input.branch,
    base: input.baseBranch,
    draft: booleanValue(defaultValue(process.env.DAYTONA_PR_DRAFT, "1")),
  });
  input.logs.push("opened GitHub PR " + pullRequest.html_url);
  return { commitSha, pullRequest };
}

async function createOrFindPullRequest(input) {
  const created = await githubFetch(input.token, "POST", "/repos/" + input.owner + "/" + input.repo + "/pulls", {
    title: input.title,
    head: input.head,
    base: input.base,
    body: input.body,
    draft: input.draft,
  });
  if (created.ok) {
    return created.json;
  }
  if (created.status !== 422) {
    throw new Error("GitHub PR create failed (" + created.status + "): " + textTail(created.text));
  }
  const head = input.owner + ":" + input.head;
  const query = new URLSearchParams({ state: "open", head, base: input.base });
  const existing = await githubFetch(input.token, "GET", "/repos/" + input.owner + "/" + input.repo + "/pulls?" + query.toString());
  if (!existing.ok) {
    throw new Error("GitHub PR lookup failed after create conflict (" + existing.status + "): " + textTail(existing.text));
  }
  const match = Array.isArray(existing.json) ? existing.json[0] : null;
  if (!match) {
    throw new Error("GitHub PR create returned 422 and no open PR matched head " + head + " base " + input.base);
  }
  return match;
}

async function githubFetch(token, method, path, body) {
  const response = await fetch("https://api.github.com" + path, {
    method,
    headers: {
      "Accept": "application/vnd.github+json",
      "Authorization": "Bearer " + token,
      "Content-Type": "application/json",
      "X-GitHub-Api-Version": "2022-11-28",
      "User-Agent": "loom-daytona-task-runner",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let json = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = null;
  }
  return { ok: response.ok, status: response.status, text, json };
}

function pullRequestBody(input, commitSha) {
  return [
    "Created by the Loom Daytona task runner.",
    "",
    "- Task: `" + input.taskId + "`",
    "- Task run: `" + input.taskRunId + "`",
    "- Base branch: `" + input.baseBranch + "`",
    "- Head branch: `" + input.branch + "`",
    "- Commit: `" + commitSha + "`",
    "",
    "The Loom task run stores the transcript, patch artifact, and PR metadata artifact.",
  ].join("\n");
}

function taskTitle(task) {
  return stringValue(task && task.title) || "Loom task";
}

function parseGitHubRepo(repoUrl) {
  const text = stringValue(repoUrl).replace(/\.git$/, "");
  let match = text.match(/^https:\/\/github\.com\/([^/]+)\/([^/]+)$/);
  if (!match) {
    match = text.match(/^git@github\.com:([^/]+)\/([^/]+)$/);
  }
  if (!match) {
    return null;
  }
  return { owner: match[1], repo: match[2] };
}

export function cloneCommand(repoUrl, repoDir, branch, githubToken) {
  // Full clone (NOT --depth 1): a stacked task bases on its predecessor's branch,
  // and the PR diff + the post-drain reconcile's merge-base checks need the real
  // base SHA and history, which a shallow tip does not provide.
  const parts = [
    "rm -rf " + shellQuote(repoDir),
    gitWithGitHubAuth(githubToken) + " clone" +
      (branch ? " --branch " + shellQuote(branch) : "") +
      " " + shellQuote(repoUrl) + " " + shellQuote(repoDir),
  ];
  return parts.join(" && ");
}

function gitWithGitHubAuth(token) {
  if (!token) {
    return "git";
  }
  const encoded = Buffer.from("x-access-token:" + token, "utf8").toString("base64");
  return "git -c " + shellQuote("http.https://github.com/.extraheader=AUTHORIZATION: basic " + encoded);
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
  // This list must mirror env.go trustedLocalProviderCredentials to prevent drift:
  // any cred name added to the widened LOCAL-runner env must also be enumerated
  // here so a future regression that leaks it into the Daytona sandbox is caught.
  return "node -e " + shellQuote([
    "const names=[",
    "['DAYTONA','API','KEY'],",
    "['GITHUB','TOKEN'],",
    "['GH','TOKEN'],",
    "['CODEX','HOME'],",
    "['LOOM','TASK','RUN','LEASE','TOKEN'],",
    "['LOOM','DRIVER','TASK','RUNNER','CMD','JSON'],",
    "['ANTHROPIC','API','KEY'],",
    "['OPENAI','API','KEY'],",
    "['CODEX','API','KEY'],",
    "['GEMINI','API','KEY'],",
    "['GOOGLE','API','KEY'],",
    "['GOOGLE','APPLICATION','CREDENTIALS'],",
    "['CURSOR','API','KEY'],",
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
