import fs from "node:fs";
import path from "node:path";
import * as v from "valibot";
import * as bundledFlueRuntime from "@flue/runtime";
import * as bundledFlueRuntimeInternal from "@flue/runtime/internal";
import {
  createFlueTranscriptCollector,
  flueUsageToTaskUsage,
  redactTranscriptEntries,
  serializeTranscriptJSONL,
} from "@loom/sdk/runtime-adapters";

const SCHEMA_VERSION = "daytona-task-run-execution.v1";
const HostInputSchema = v.strictObject({
  schemaVersion: v.literal(SCHEMA_VERSION),
  execution: v.strictObject({
    workspaceKey: v.string(),
    taskRunId: v.string(),
    workItemId: v.string(),
    driverRunId: v.string(),
    intent: v.strictObject({
      schemaVersion: v.literal(SCHEMA_VERSION),
      repositoryUrl: v.string(),
      baseRef: v.optional(v.string()),
      taskPrompt: v.string(),
      backend: v.literal("codex"),
      model: v.optional(v.string()),
      mode: v.optional(v.string()),
      delivery: v.strictObject({
        openPullRequest: v.boolean(),
        baseBranch: v.optional(v.string()),
        outputBranch: v.optional(v.string()),
        draft: v.optional(v.boolean()),
      }),
    }),
  }),
  credentials: v.strictObject({
    daytona: v.string(),
    github: v.optional(v.string()),
  }),
});

// This workflow is internal bundle machinery, not a selectable task runner. Its
// input is admitted only by the host launcher over a private stdin/IPC channel.
// Provider credentials never enter runner intent, argv, or subprocess env.
export default bundledFlueRuntime.defineWorkflow({
  agent: bundledFlueRuntime.defineAgent(() => ({ model: false })),
  input: HostInputSchema,
  run: async ({ input }) => toJsonResult(await run(input)),
});

function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

const DEFAULT_MODEL = "openai-codex/gpt-5.3-codex-spark";
const DEFAULT_REPO_DIR = "/tmp/loom-daytona-task-repo";
export async function run(hostInput = {}) {
  const execution = hostInput.execution || {};
  const intent = execution.intent || {};
  const encodedCredentials = hostInput.credentials || {};
  const credentials = {
    daytona: decodeCredential(encodedCredentials.daytona),
    github: decodeCredential(encodedCredentials.github),
  };
  const taskRunId = stringValue(execution.taskRunId || "task-run");
  const taskId = stringValue(execution.workItemId);
  const request = {
    workspace_key: stringValue(execution.workspaceKey),
    task_run_id: taskRunId,
    task_id: taskId,
    driver_run_id: stringValue(execution.driverRunId),
    input: {
      repoUrl: stringValue(intent.repositoryUrl),
      taskPrompt: stringValue(intent.taskPrompt),
      model: stringValue(intent.model),
      mode: stringValue(intent.mode),
    },
  };
  const logs = [];
  const flueEvents = [];
  const setupEvents = [];
  const secrets = secretRepresentations(credentials.daytona, credentials.github);
  let sandbox;
  let sandboxId = "";

  try {
    const imports = await loadRuntimeImports();
    const model = stringValue(process.env.LOOM_FLUE_AGENT_MODEL) || DEFAULT_MODEL;
    const auth = await configureCodexAuth(imports, model, request);
    if (!auth.ok) {
      return safeResult(failed("codex_auth_failed", auth.error, taskRunId, request, logs, sandboxId, secrets), secrets);
    }
    secrets.push(...secretRepresentations(auth.accessToken, auth.refreshToken));

    const daytonaKey = stringValue(credentials.daytona);
    if (!daytonaKey) {
      return safeResult(failed("daytona_credentials_missing", "saved Daytona credential is required", taskRunId, request, logs), secrets);
    }

    const repoUrl = stringValue(intent.repositoryUrl);
    if (!repoUrl) {
      return safeResult(failed("daytona_repo_url_missing", "repositoryUrl is required", taskRunId, request, logs), secrets);
    }

    const task = { id: taskId, title: stringValue(intent.taskPrompt) };
    const delivery = {
      mode: stringValue(intent.mode),
      openPullRequest: !!(intent.delivery && intent.delivery.openPullRequest),
      branch: stringValue(intent.delivery && intent.delivery.outputBranch),
      baseBranch: stringValue(intent.delivery && intent.delivery.baseBranch) || stringValue(intent.baseRef),
      rootBaseBranch: stringValue(intent.delivery && intent.delivery.baseBranch) || stringValue(intent.baseRef),
      stacked: false,
      dependencyIds: [],
      baseTaskId: "",
      stackId: "",
      draft: !!(intent.delivery && intent.delivery.draft),
    };
    const githubToken = stringValue(credentials.github);
    if (delivery.openPullRequest && !githubToken) {
      return safeResult(failed("github_credentials_missing", "saved GitHub credential is required for pull request mode", taskRunId, request, logs), secrets);
    }
    if (githubToken) {
      secrets.push(Buffer.from("x-access-token:" + githubToken, "utf8").toString("base64"));
    }

    const sdk = imports.daytona;
    const Daytona = sdk.Daytona || (sdk.default && sdk.default.Daytona);
    if (typeof Daytona !== "function") {
      return safeResult(failed("daytona_sdk_invalid", "Daytona SDK import did not expose Daytona", taskRunId, request, logs, sandboxId, secrets), secrets);
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
    const published = delivery.openPullRequest
      ? await publishPullRequest(setup, {
          task,
          taskId,
          taskRunId,
          repoUrl,
          repoDir,
          baseBranch: delivery.baseBranch,
          branch: delivery.branch,
          draft: delivery.draft,
          githubToken,
          secrets,
          logs,
        })
      : null;
    const transcriptEntries = normalizeHostTranscriptEntries(
      redactTranscriptEntries(transcriptCollector.entries, secrets),
    );
    const transcriptJSONL = serializeTranscriptJSONL(transcriptEntries);
    const usage = flueUsageToTaskUsage(response && response.usage, { costUnit: "usd" });

    logs.push("codex/flue response:");
    logs.push(textTail(stringValue(response && response.text), 2000));
    logs.push("remote git diffstat:");
    logs.push(textTail(diffStat.stdout || "(no diff)", 2000));

    const result = {
      schemaVersion: SCHEMA_VERSION,
      status: "completed",
      exitCode: 0,
      logs: redact(logs.join("\n") + "\n", secrets),
      transcript: transcriptJSONL,
      transcriptEntries,
      usage: {
        inputTokens: numberValue(usage.input_tokens, 0),
        outputTokens: numberValue(usage.output_tokens, 0),
        cacheReadTokens: numberValue(usage.cache_read_tokens, 0),
        cacheWriteTokens: numberValue(usage.cache_write_tokens, 0),
        estimatedCostUsd: numberValue(usage.estimated_cost_usd, 0),
      },
      sandbox: {
        provider: "daytona",
        id: sandboxId,
        workDir,
        cwd: repoDir,
        repoRef: head.stdout.trim(),
      },
      ...(diff.stdout && diff.stdout.trim()
        ? {
            patch: {
              content: redact(diff.stdout, secrets),
              diffStat: redact(diffStat.stdout || "", secrets),
              baseRef: delivery.baseBranch,
              headSha: head.stdout.trim(),
            },
          }
        : {}),
      ...(published
        ? {
            pullRequest: {
              url: stringValue(published.pullRequest && published.pullRequest.html_url),
              number: numberValue(published.pullRequest && published.pullRequest.number, 0),
              baseBranch: delivery.baseBranch,
              headBranch: delivery.branch,
              commitSha: published.commitSha,
            },
          }
        : {}),
    };
    return safeResult(result, secrets);
  } catch (error) {
    return safeResult(failed("daytona_task_runner_failed", errorMessage(error), taskRunId, request, logs, sandboxId, secrets), secrets);
  } finally {
    if (sandbox && process.env.KEEP_DAYTONA_SANDBOX !== "1") {
      try {
        await sandbox.delete(60);
      } catch (error) {
        console.error(redact("warning: failed to delete Daytona sandbox " + sandboxId + ": " + errorMessage(error), secrets));
      }
    }
  }
}

async function loadRuntimeImports() {
  const runtimeImport = stringValue(process.env.FLUE_RUNTIME_IMPORT);
  const internalImport = stringValue(process.env.FLUE_RUNTIME_INTERNAL_IMPORT);
  const daytonaImport = stringValue(process.env.DAYTONA_SDK_IMPORT);
  if (!daytonaImport) {
    throw new Error("DAYTONA_SDK_IMPORT is required");
  }
  const [runtime, internal, daytona] = await Promise.all([
    runtimeImport ? import(runtimeImport) : Promise.resolve(bundledFlueRuntime),
    internalImport ? import(internalImport) : Promise.resolve(bundledFlueRuntimeInternal),
    import(daytonaImport),
  ]);
  return { runtime, internal, daytona };
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

function taskMode(request) {
  return stringValue(inputValue(request, "mode"));
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
    draft: !!input.draft,
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
    schemaVersion: SCHEMA_VERSION,
    status: "failed",
    exitCode: 1,
    errorClass,
    errorMessage: redact(textTail(message), secrets),
    logs: redact(logs.concat([errorClass + ": " + message]).join("\n") + "\n", secrets),
    transcript: "",
    transcriptEntries: [],
    usage: {
      inputTokens: 0,
      outputTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      estimatedCostUsd: 0,
    },
    sandbox: {
      provider: "daytona",
      id: sandboxId,
      workDir: "",
      cwd: "",
      repoRef: "",
    },
  };
}

function normalizeHostTranscriptEntries(entries) {
  if (!Array.isArray(entries)) {
    return [];
  }
  return entries.map((entry, index) => ({
    sequence: numberValue(entry && (entry.seq ?? entry.sequence), index + 1),
    timestamp: stringValue(entry && entry.timestamp),
    role: stringValue(entry && entry.role),
    type: stringValue(entry && entry.type),
    text: stringValue(entry && entry.text),
    toolName: stringValue(entry && (entry.tool_name ?? entry.toolName)),
    toolUseId: stringValue(entry && (entry.tool_use_id ?? entry.toolUseId)),
    output: stringValue(entry && entry.output),
    uuid: stringValue(entry && entry.uuid),
  }));
}

function safeResult(result, secrets) {
  const serialized = JSON.stringify(result);
  const leaked = secrets.some((secret) => {
    const value = stringValue(secret);
    return value.length >= 4 && serialized.includes(value);
  });
  if (!leaked) {
    return result;
  }
  return {
    schemaVersion: SCHEMA_VERSION,
    status: "failed",
    exitCode: 1,
    errorClass: "daytona_response_secret_detected",
    errorMessage: "provider response was rejected by credential-containment policy",
    logs: "daytona_response_secret_detected\n",
    transcript: "",
    transcriptEntries: [],
    usage: {
      inputTokens: 0,
      outputTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      estimatedCostUsd: 0,
    },
    sandbox: { provider: "daytona", id: "", workDir: "", cwd: "", repoRef: "" },
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

function decodeCredential(value) {
  const encoded = stringValue(value);
  if (!encoded) {
    return "";
  }
  return Buffer.from(encoded, "base64").toString("utf8").trim();
}

function secretRepresentations(...values) {
  return unique(values.flatMap((value) => {
    const secret = stringValue(value);
    if (!secret) {
      return [];
    }
    return [secret, Buffer.from(secret, "utf8").toString("base64")];
  }));
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

function unique(values) {
  return [...new Set(values.filter(Boolean))];
}
