import { execFile } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { defineAgent, defineWorkflow } from "@flue/runtime";
import {
  ensureSessionMetaLead,
  minimalTranscript,
  parseStreamJSONTranscript,
  streamFailureMessage,
  taskUsageFromEntries,
} from "../lib/transcript-convert.ts";

export { parseStreamJSONTranscript, taskUsageFromEntries } from "../lib/transcript-convert.ts";

// Flue HEAD (durable-streams) requires every workflow module to default-export a
// defineWorkflow() definition; a bare `export function run` no longer normalizes.
// This runner is not an LLM agent (it execFiles a backend CLI), so the bound
// agent is a credential-free stub (model: false, no harness usage). The request
// arrives via env — the task-runner host-bridge sets LOOM_TASK_RUN_REQUEST_JSON
// (driver/task_bridge.go) — which requestPayload() already reads, so the inner
// run() body is unchanged.
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
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

// Local task runner: runs the user-selected backend CLI over a prepared worktree.
//
// This is NOT the Flue agent runtime — it execFiles the same backend CLIs that
// loom's local/agent tooling uses (claude/codex/opencode/gemini/cursor) directly,
// mirroring the Go arg builders in internal/cli/backends/backend_*.go. It returns
// `completed` ONLY when the CLI exits 0; every other outcome (unsupported backend,
// missing worktree, missing binary, nonzero/spawn failure) fails closed with a real
// exit code and a specific error class. There is no synthetic "completed" path: a
// task can never fake-complete here.

const DEFAULT_BACKEND = "codex";

// SUPPORTED maps each backend to its default binary name. The binary is
// overridable via LOOM_<BACKEND>_BIN so tests can inject a fake CLI and so
// operators can pin a specific path.
const SUPPORTED = {
  claude: "claude",
  codex: "codex",
  opencode: "opencode",
  gemini: "gemini",
  cursor: "cursor-agent", // the headless agent CLI; `cursor` is the IDE launcher
};

// STREAM_JSON_BACKENDS get first-class stream-json -> canonical transcript_entries.
const STREAM_JSON_BACKENDS = new Set(["codex", "claude", "cursor", "opencode", "gemini"]);

export async function run(ctx = {}) {
  const request = await requestPayload(ctx);
  const taskRunId = stringValue(request.task_run_id || request.taskRunId || process.env.LOOM_TASK_RUN_ID || "task-run");
  const taskId = stringValue(request.task_id || request.taskId || process.env.LOOM_TASK_ID);
  const logs = [];

  const backend = resolveBackend();
  if (!Object.prototype.hasOwnProperty.call(SUPPORTED, backend)) {
    return failed(
      "local_backend_unsupported",
      `LOOM_TASK_RUNNER_BACKEND=${backend} is not a supported local backend (expected one of ${Object.keys(SUPPORTED).join(", ")})`,
      { taskRunId, taskId, backend, request, logs },
    );
  }

  const worktree = stringValue(process.env.LOOM_WORKTREE_PATH);
  if (!worktree || !dirExists(worktree)) {
    return failed(
      "local_worktree_missing",
      worktree
        ? `LOOM_WORKTREE_PATH ${worktree} does not exist`
        : "LOOM_WORKTREE_PATH is required for the local task runner",
      { taskRunId, taskId, backend, request, logs },
    );
  }

  const binary = resolveBinary(backend);
  if (!binary) {
    return failed(
      "local_backend_unavailable",
      `${backend} CLI (${binaryName(backend)}) was not found on PATH`,
      { taskRunId, taskId, backend, request, logs },
    );
  }

  const headBefore = await gitHead(worktree);

  // Run the CLI in an ISOLATED git worktree checked out at HEAD so the host
  // worktree (LOOM_WORKTREE_PATH) stays clean — the driver host-bridge applies
  // the returned patch to that clean host worktree via patch-back. Editing the
  // host worktree in place would make patch-back re-apply changes that already
  // exist (conflict). When the host worktree is not a git repo / has no HEAD,
  // fall back to in-place execution with no base_ref (patch-back not possible).
  // Stacked mode (the host bridge sets LOOM_TASK_RUN_STACKED when the task
  // belongs to a stack): the host worktree is ALREADY a per-task checkout cut
  // from the predecessor's branch, so the agent runs IN PLACE (no isolated
  // double-wrap), commits there, and pushes the canonical output branch. The
  // post-drain reconcile opens/links the PR with the right base — the runner
  // does not open an independent loom/<taskid> PR.
  const stacked = booleanValue(process.env.LOOM_TASK_RUN_STACKED);
  const stackBranch = stringValue(process.env.LOOM_TASK_RUN_OUTPUT_BRANCH);
  const stackBaseRef = stringValue(process.env.LOOM_TASK_RUN_BASE_REF);
  const stackId = stringValue(process.env.LOOM_TASK_RUN_STACK_ID);

  const isolated = stacked ? null : await setupIsolatedWorktree(worktree, taskRunId, logs);
  const execWorktree = isolated ? isolated.path : worktree;
  const baseRef = stacked ? stackBaseRef : (isolated ? isolated.base : "");
  if (stacked) {
    logs.push("stacked mode: running in place at " + worktree + " (base " + (stackBaseRef || "?") + "), pushing " + (stackBranch || "?"));
  }

  const task = await loadTask(request, logs);
  // LOOM_TASK_RUN_PROMPT lets a host that already composed the agent prompt (the
  // daemon execution leaf, which builds role-specific planning/task prompts) deliver
  // it verbatim, instead of this runner's generic buildPrompt — so routing the daemon
  // leaf through this runner (Phase U) preserves the leaf's exact prompt.
  const promptOverride = process.env.LOOM_TASK_RUN_PROMPT;
  const prompt = typeof promptOverride === "string" && promptOverride.trim() !== ""
    ? promptOverride
    : buildPrompt(request, task, execWorktree);
  const args = backendArgs(backend, execWorktree, prompt);
  const usesStdinPrompt = backendUsesStdinPrompt(backend);

  const openPR = booleanValue(inputValue(request, "openPullRequest"));
  let exitCode;
  let stdout = "";
  let stderr = "";
  let patchInfo;
  let prInfo = null;
  let stackInfo = null;
  let prFailure = null;
  try {
    let result;
    try {
      result = await execBackend(binary, args, {
        cwd: execWorktree,
        input: usesStdinPrompt ? prompt : undefined,
        live: true,
      });
    } catch (error) {
      return failed("local_agent_failed", `failed to spawn ${backend} CLI: ${errorMessage(error)}`, {
        taskRunId,
        taskId,
        backend,
        request,
        logs,
        headBefore,
      });
    }
    exitCode = result.code;
    stdout = result.stdout;
    stderr = result.stderr;

    logs.push(`${backend} CLI exit=${exitCode}`);
    if (stdout.trim()) {
      logs.push(textTail(stdout, 4000));
    }
    if (stderr.trim()) {
      logs.push("stderr:\n" + textTail(stderr, 2000));
    }

    patchInfo = await capturePatch(execWorktree, baseRef);

    // Stacked delivery: commit in place and push the canonical branch on the
    // predecessor base. No PR is opened here — the post-drain reconcile does it.
    if (stacked && exitCode === 0) {
      if (patchInfo.filesChanged === 0) {
        logs.push("stacked: the agent produced no changes; no branch pushed (empty unit)");
      } else {
        const token = await resolveGitHubToken();
        const slug = token ? await resolveRepoSlug(worktree, request) : null;
        if (!token) {
          prFailure = { class: "github_credentials_missing", message: "stackedPullRequests requires a GitHub credential (GITHUB_TOKEN/GH_TOKEN, or a local `gh auth login`)" };
        } else if (!slug) {
          prFailure = { class: "github_repo_unresolved", message: "stackedPullRequests requires a GitHub repo (githubRepo/repoUrl input or an origin remote)" };
        } else if (!stackBranch) {
          prFailure = { class: "stack_branch_missing", message: "stacked mode requires LOOM_TASK_RUN_OUTPUT_BRANCH (the canonical branch to push)" };
        } else {
          const title = stringValue((task && (task.title || task.name)) || ("Loom task " + (taskId || taskRunId)));
          try {
            stackInfo = await deliverStackBranch({ worktreePath: execWorktree, token, owner: slug.owner, repo: slug.repo, branch: stackBranch, title });
            logs.push("pushed stack branch " + stackInfo.branch + " @ " + stackInfo.head.slice(0, 12));
          } catch (error) {
            prFailure = { class: "stack_push_failed", message: "failed to push stack branch: " + errorMessage(error) };
          }
        }
      }
    } else if (openPR && exitCode === 0) {
      if (!isolated) {
        prFailure = { class: "github_repo_unresolved", message: "openPullRequest requires a git worktree (no isolated worktree was created)" };
      } else if (patchInfo.filesChanged === 0) {
        logs.push("openPullRequest: the agent produced no changes; no PR opened");
      } else {
        const token = await resolveGitHubToken();
        const slug = token ? await resolveRepoSlug(worktree, request) : null;
        if (!token) {
          prFailure = { class: "github_credentials_missing", message: "openPullRequest requires a GitHub credential (GITHUB_TOKEN/GH_TOKEN, or a local `gh auth login`)" };
        } else if (!slug) {
          prFailure = { class: "github_repo_unresolved", message: "openPullRequest requires a GitHub repo (githubRepo/repoUrl input or an origin remote)" };
        } else {
          const base = stringValue(inputValue(request, "baseBranch")) || "main";
          const branch = "loom/" + String(taskId || taskRunId).replace(/[^A-Za-z0-9_.-]/g, "-").toLowerCase();
          const title = stringValue((task && (task.title || task.name)) || ("Loom task " + (taskId || taskRunId)));
          const prBody = "Automated change by the Loom local-task-runner (" + backend + "). Task " + (taskId || taskRunId) + ".";
          try {
            prInfo = await deliverPullRequest({ isolatedPath: isolated.path, token, owner: slug.owner, repo: slug.repo, base, branch, title, body: prBody });
            logs.push("opened pull request " + prInfo.url);
          } catch (error) {
            prFailure = { class: "github_pr_failed", message: "failed to open pull request: " + errorMessage(error) };
          }
        }
      }
    }
  } finally {
    if (isolated) {
      await removeIsolatedWorktree(worktree, isolated.path, logs);
    }
  }

  // Fail closed when PR delivery was requested but could not be completed.
  if (prFailure) {
    return failed(prFailure.class, prFailure.message, { taskRunId, taskId, backend, request, logs, headBefore });
  }

  let transcriptEntries = STREAM_JSON_BACKENDS.has(backend)
    ? parseStreamJSONTranscript(backend, stdout)
    : minimalTranscript(backend, taskId || taskRunId, prompt, stdout);
  // Fall back to the prompt + stdout tail if a stream-json backend yielded no
  // parseable content (non-JSON output / early exit), so evidence isn't lost.
  if (STREAM_JSON_BACKENDS.has(backend) && !transcriptEntries.some((e) => e.role !== "system")) {
    transcriptEntries = minimalTranscript(backend, taskId || taskRunId, prompt, stdout);
  }
  // Tool outputs now persist (the `output` field) and the agent inherits host
  // secrets — scrub known secret values that may have been echoed into output.
  transcriptEntries = redactTranscriptSecrets(transcriptEntries);
  // Lead with a canonical session_meta entry. The stream-json parse paths emit
  // text/tool_use/tool_result + a terminal result, but not session_meta (only the
  // minimal fallback did) — and the canonical transcript vocabulary (aether #5d)
  // requires a session_meta head. The daemon TS leaf surfaces this transcript
  // verbatim, so adding it here fixes both the leaf and driver transcripts.
  transcriptEntries = ensureSessionMetaLead(transcriptEntries, backend, taskId || taskRunId);
  // Surface the token usage the parser computed (embedded in the terminal result
  // entry) as top-level fields so the Go host-bridge ingests it into the fleet-db
  // TaskRun — without this, local-CLI runs report zero usage while daytona does not.
  const taskUsage = taskUsageFromEntries(transcriptEntries);
  // Cost is taken ONLY from what the backend CLI itself reports — never estimated
  // from a price table. Verified per backend (real-CLI capture, 2026-06-23):
  //   claude   -> total_cost_usd (model-accurate: Opus vs Sonnet, cache tiers, web)
  //   opencode -> per-step `cost` (real when metered; a legitimate 0 on subscription)
  //   codex    -> NO cost, NOT even a model in `exec --json` output
  //   cursor   -> NO cost; model is proprietary ("Composer"), no public rate
  //   gemini   -> NO cost (usageMetadata tokens only)
  // For the backends that expose no cost we leave estimated_cost_usd unset (unknown)
  // rather than fabricate a token x rate guess for an unknown/unpriceable model.
  const streamFailure = streamFailureMessage(backend, stdout);

  const metadata = stringMetadata({
    task_runner: "local-task-runner",
    runtime_strategy: "local-cli-" + backend,
    runner: request.runner || request.runner_ref || "local-task-runner",
    runner_kind: request.runner_kind || request.runnerKind || process.env.LOOM_TASK_RUNNER_KIND,
    runner_entrypoint: request.runner_entrypoint || request.runnerEntrypoint || process.env.LOOM_TASK_RUNNER_ENTRYPOINT,
    backend,
    model: resolveModel(backend),
    task_id: taskId,
    worktree_path: worktree,
    exec_worktree_path: execWorktree,
    base_ref: baseRef,
    repo_head_before: headBefore,
    repo_head_after: patchInfo.head,
    files_changed: String(patchInfo.filesChanged),
    lines_added: String(patchInfo.linesAdded),
    lines_removed: String(patchInfo.linesRemoved),
    cli_exit_code: String(exitCode),
  });
  if (streamFailure) {
    metadata.stream_error = streamFailure;
  }

  if (stackInfo) {
    // Stacked: the pushed canonical branch IS the delivery; the reconcile opens
    // the PR. github_branch + sha drive the host finalize barrier (published).
    metadata.delivery = "stack_branch";
    metadata.github_branch = stackInfo.branch;
    metadata.github_head_sha = stackInfo.head;
    if (stackId) {
      metadata.stack_id = stackId;
    }
  } else if (prInfo) {
    metadata.delivery = "pull_request";
    metadata.github_pr_url = prInfo.url;
    metadata.github_pr_number = String(prInfo.number);
    metadata.github_branch = prInfo.branch;
  } else if (openPR || stacked) {
    metadata.delivery = "pull_request_skipped_no_changes";
  } else {
    metadata.delivery = "patch_back";
  }

  if (exitCode !== 0 || streamFailure) {
    const failureExitCode = exitCode !== 0 ? exitCode : 1;
    const failureMessage = streamFailure
      ? `${backend} CLI reported stream error: ${streamFailure}`
      : `${backend} CLI exited with code ${exitCode}`;
    return {
      status: "failed",
      exitCode: failureExitCode,
      errorClass: "local_agent_failed",
      errorMessage: failureMessage,
      logs: logs.join("\n") + "\n",
      ...taskUsage,
      transcript_entries: transcriptEntries,
      patch: patchInfo.patch,
      // base_ref lets the driver host-bridge patch-back apply this patch to the
      // (clean) host worktree. Empty when running in place (no patch-back).
      base_ref: baseRef,
      patch_base_ref: baseRef,
      runtimeMetadata: { ...metadata, phase: "local_agent_failed" },
    };
  }

  const completed = {
    status: "completed",
    exitCode: 0,
    logs: logs.join("\n") + "\n",
    ...taskUsage,
    transcript_entries: transcriptEntries,
    runtimeMetadata: metadata,
  };
  if (prInfo || stackInfo || stacked) {
    // PR / stacked mode: the pull request or pushed branch IS the delivery (and
    // stacked mode runs in place, so there is nothing to patch-back) — return no
    // top-level patch so the driver host-bridge skips patch-back.
    return completed;
  }
  completed.patch = patchInfo.patch;
  // base_ref lets the driver host-bridge patch-back apply this patch to the
  // (clean) host worktree. Empty when running in place (no patch-back).
  completed.base_ref = baseRef;
  completed.patch_base_ref = baseRef;
  return completed;
}

// resolveBackend reads the host-bridge-injected backend selection.
export function resolveBackend() {
  return stringValue(process.env.LOOM_TASK_RUNNER_BACKEND).toLowerCase() || DEFAULT_BACKEND;
}

function binaryName(backend) {
  return SUPPORTED[backend] || backend;
}

// resolveBinary returns the runnable binary for a backend, honoring the
// LOOM_<BACKEND>_BIN override and otherwise searching PATH. Returns "" when the
// binary cannot be found.
export function resolveBinary(backend, env = process.env) {
  const override = stringValue(env["LOOM_" + backend.toUpperCase() + "_BIN"]);
  if (override) {
    if (override.includes(path.sep) || path.isAbsolute(override)) {
      return isExecutableFile(override) ? override : "";
    }
    const resolved = lookPath(override, env);
    return resolved || (isExecutableFile(override) ? override : "");
  }
  return lookPath(binaryName(backend), env);
}

// lookPath mirrors exec.LookPath: search each PATH entry for an executable file.
function lookPath(name, env) {
  if (name.includes(path.sep) || path.isAbsolute(name)) {
    return isExecutableFile(name) ? name : "";
  }
  const pathValue = stringValue(env.PATH || env.Path || "");
  const entries = pathValue.split(path.delimiter).filter(Boolean);
  for (const dir of entries) {
    const candidate = path.join(dir, name);
    if (isExecutableFile(candidate)) {
      return candidate;
    }
  }
  return "";
}

function isExecutableFile(filePath) {
  try {
    const stat = fs.statSync(filePath);
    if (!stat.isFile()) {
      return false;
    }
    fs.accessSync(filePath, fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function dirExists(filePath) {
  try {
    return fs.statSync(filePath).isDirectory();
  } catch {
    return false;
  }
}

// DefaultMaxBudgetUSD mirrors internal/cli/backends/backend_claude.go.
const DEFAULT_MAX_BUDGET_USD = 50.0;

// resolveMaxBudgetUSD mirrors backend_claude.go resolveMaxBudgetUSD: read
// LOOM_MAX_BUDGET_USD (default $50.00); "0" opts out (returns ""); invalid or
// negative values fall back to the default. Returns a "%.2f"-formatted string.
function resolveMaxBudgetUSD() {
  const raw = stringValue(process.env.LOOM_MAX_BUDGET_USD);
  if (!raw) {
    return DEFAULT_MAX_BUDGET_USD.toFixed(2);
  }
  const value = Number(raw);
  if (!Number.isFinite(value) || value < 0) {
    return DEFAULT_MAX_BUDGET_USD.toFixed(2);
  }
  if (value === 0) {
    return ""; // explicit opt-out
  }
  return value.toFixed(2);
}

// resolveAgentEffort mirrors backend_claude.go resolveAgentEffort:
// LOOM_AGENT_EFFORT, then LOOM_CLAUDE_EFFORT.
function resolveAgentEffort() {
  return stringValue(process.env.LOOM_AGENT_EFFORT) || stringValue(process.env.LOOM_CLAUDE_EFFORT);
}

// backendArgs mirrors the non-interactive argument lists built in
// internal/cli/backends/backend_<backend>.go. Keep these in sync with those
// builders so the local runner invokes each CLI exactly as loom's agent path does.
export function backendArgs(backend, worktree, prompt) {
  switch (backend) {
    case "codex":
      // Headless (no-PTY) codex: trailing "-" => read the prompt from stdin
      // (mirrors github-review-task-runner.ts). The Go path passes the prompt
      // positionally because it runs under a PTY; without a PTY codex blocks on
      // an open stdin pipe, so we deliver the prompt over stdin instead.
      return ["exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "-"];
    case "claude": {
      // buildClaudeNonInteractiveArgs: -p --verbose --output-format stream-json
      // --dangerously-skip-permissions [--max-budget-usd N] [--effort E]; prompt last.
      // (--resume is owned by the durability/resume path and is added there once
      // session carry-forward exists — it is not part of a cold local run.)
      const claudeArgs = ["-p", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"];
      const budget = resolveMaxBudgetUSD();
      if (budget) {
        claudeArgs.push("--max-budget-usd", budget);
      }
      const effort = resolveAgentEffort();
      if (effort) {
        claudeArgs.push("--effort", effort);
      }
      claudeArgs.push(prompt);
      return claudeArgs;
    }
    case "gemini":
      // defaultGeminiNonInteractiveInvoker: --approval-mode=yolo -p <prompt> -o stream-json.
      return ["--approval-mode=yolo", "-p", prompt, "-o", "stream-json"];
    case "opencode": {
      // defaultOpenCodeNonInteractiveInvoker: run --format json --dir <worktree>; prompt via stdin.
      // Mirror backend_opencode.go openCodeModelArgs(): pin the model from LOOM_OPENCODE_MODEL when set.
      const opencodeArgs = ["run", "--format", "json", "--dir", worktree];
      const opencodeModel = stringValue(process.env.LOOM_OPENCODE_MODEL);
      if (opencodeModel) {
        opencodeArgs.push("--model", opencodeModel);
      }
      return opencodeArgs;
    }
    case "cursor":
      // defaultCursorNonInteractiveInvoker: -p --output-format stream-json --force <prompt>.
      return ["-p", "--output-format", "stream-json", "--force", prompt];
    default:
      return [prompt];
  }
}

// backendUsesStdinPrompt reports whether the prompt is delivered over stdin
// rather than as a positional argument. OpenCode (harnessInvocation.Prompt) and
// headless codex (trailing "-") both read the prompt from stdin.
function backendUsesStdinPrompt(backend) {
  return backend === "opencode" || backend === "codex";
}

function resolveModel(backend) {
  switch (backend) {
    case "opencode":
      return stringValue(process.env.LOOM_OPENCODE_MODEL);
    default:
      return "";
  }
}

async function execBackend(binary, args, options) {
  return await new Promise((resolve, reject) => {
    const child = execFile(
      binary,
      args,
      {
        cwd: options.cwd,
        // IS_SANDBOX=1: the local task runner always executes backend CLIs as root inside loom's
        // isolated task-run container. claude-code refuses `--dangerously-skip-permissions` under
        // root unless this sandbox signal is set; harmless for the other backends (codex/cursor/etc).
        env: { ...(options.env || process.env), IS_SANDBOX: "1" },
        maxBuffer: 64 * 1024 * 1024,
        timeout: numberValue(process.env.LOOM_LOCAL_TASK_TIMEOUT_MS, 30 * 60 * 1000),
      },
      (error, stdout, stderr) => {
        if (error && typeof error.code !== "number" && !error.killed) {
          // Spawn failure (ENOENT, EACCES, ...) — no exit code available.
          reject(error);
          return;
        }
        const code = error && typeof error.code === "number" ? error.code : error && error.killed ? 124 : 0;
        resolve({ code, stdout: String(stdout || ""), stderr: String(stderr || "") });
      },
    );
    if (options.live === true && booleanValue(process.env.LOOM_TASK_RUNNER_STREAM_STDERR)) {
      // Tee the backend's live output to OUR stderr so, when the daemon leaf runs this
      // runner (Phase U), the supervisor's output-timeout watchdog — which stats the
      // agent log mtime — sees per-turn activity, exactly as the Go leaf's wrapper PTY
      // path did (internal/cli/agent/plan.go). stdout stays clean for the result line.
      if (child.stdout) child.stdout.on("data", (chunk) => process.stderr.write(chunk));
      if (child.stderr) child.stderr.on("data", (chunk) => process.stderr.write(chunk));
    }
    // Always close the child's stdin. Backends that take the prompt over stdin
    // (opencode, headless codex) receive it here; positional-prompt backends
    // (claude, gemini, cursor) get an immediate EOF so they never block reading
    // an open non-TTY stdin pipe (the cause of the codex hang under execFile).
    if (child.stdin) {
      if (options.input !== undefined) {
        child.stdin.end(options.input);
      } else {
        child.stdin.end();
      }
    }
  });
}

// setupIsolatedWorktree creates a linked git worktree checked out at the host
// worktree's HEAD, so the backend CLI edits an isolated copy and the host
// worktree stays clean for the driver host-bridge to patch-back the result.
// Returns null when the host worktree is not a git repo / has no HEAD (the CLI
// then runs in place, like the legacy behavior, with no base_ref).
async function setupIsolatedWorktree(hostWorktree, taskRunId, logs) {
  const head = await gitHead(hostWorktree);
  if (!head) {
    logs.push("isolated worktree: host worktree has no git HEAD; running in place (no patch base_ref)");
    return null;
  }
  const safe = String(taskRunId || "task").replace(/[^A-Za-z0-9_.-]/g, "_");
  // Keep the isolated worktree near the host repo instead of under os.tmpdir().
  // Some local CLIs, notably OpenCode, enforce project-directory permissions and
  // auto-reject writes to temp/external directories even when --dir points there.
  const isolatedPath = path.join(path.dirname(hostWorktree), ".loom-local-runner-" + safe + "-" + Date.now());
  const add = await execBackend("git", ["-C", hostWorktree, "worktree", "add", "--detach", isolatedPath, "HEAD"], {
    cwd: hostWorktree,
  });
  if (add.code !== 0) {
    logs.push("isolated worktree: `git worktree add` failed (" + textTail(add.stderr, 400) + "); running in place");
    return null;
  }
  logs.push("isolated worktree at " + isolatedPath + " (base " + head + ")");
  return { path: isolatedPath, base: head };
}

// removeIsolatedWorktree tears down the linked worktree (best-effort) so stale
// worktrees do not accumulate in the host repo.
async function removeIsolatedWorktree(hostWorktree, isolatedPath, logs) {
  try {
    await execBackend("git", ["-C", hostWorktree, "worktree", "remove", "--force", isolatedPath], { cwd: hostWorktree });
    await execBackend("git", ["-C", hostWorktree, "worktree", "prune"], { cwd: hostWorktree });
  } catch (error) {
    if (logs) {
      logs.push("isolated worktree cleanup warning: " + errorMessage(error));
    }
  }
}

// ---------------------------------------------------------------------------
// Opt-in GitHub pull-request delivery.
//
// By default the local task runner returns a patch and the driver host-bridge
// applies it (patch-back). When the workflow opts in via openPullRequest AND a
// GitHub credential is available (GITHUB_TOKEN/GH_TOKEN passed through by the
// host bridge, or a local `gh auth login`), the runner instead commits the
// isolated worktree's changes to a branch, pushes it, and opens a PR — and
// returns NO top-level patch so the host-bridge skips patch-back. If PR mode is
// requested but no credential/repo is resolvable, it fails closed.
// ---------------------------------------------------------------------------

function booleanValue(value) {
  return ["1", "true", "yes", "on"].includes(stringValue(value).toLowerCase());
}

function inputValue(request, key) {
  if (request && request.input && typeof request.input === "object" && request.input[key] !== undefined) {
    return request.input[key];
  }
  if (request && request[key] !== undefined) {
    return request[key];
  }
  return undefined;
}

// resolveGitHubToken prefers the host-bridge-passed env credential, then a
// local `gh` login. Returns "" when neither is available.
async function resolveGitHubToken() {
  const envToken = stringValue(process.env.GITHUB_TOKEN) || stringValue(process.env.GH_TOKEN);
  if (envToken) {
    return envToken;
  }
  try {
    const r = await execBackend("gh", ["auth", "token"], { cwd: os.tmpdir() });
    if (r.code === 0) {
      const t = stringValue(r.stdout);
      if (t) {
        return t;
      }
    }
  } catch {
    // gh not installed / not logged in — fall through.
  }
  return "";
}

export function parseRepoSlug(url) {
  const text = stringValue(url);
  if (!text) {
    return null;
  }
  let m = text.match(/github\.com[:/]+([^/]+)\/([^/]+?)(?:\.git)?\/?$/i);
  if (!m) {
    m = text.match(/^([^/\s]+)\/([^/\s]+?)(?:\.git)?$/);
  }
  return m ? { owner: m[1], repo: m[2] } : null;
}

// resolveRepoSlug resolves owner/repo from the request input, else the host
// worktree's origin remote.
async function resolveRepoSlug(worktree, request) {
  const fromInput = parseRepoSlug(inputValue(request, "githubRepo") || inputValue(request, "repoUrl"));
  if (fromInput) {
    return fromInput;
  }
  try {
    const r = await execBackend("git", ["-C", worktree, "remote", "get-url", "origin"], { cwd: worktree });
    if (r.code === 0) {
      return parseRepoSlug(r.stdout);
    }
  } catch {
    // no origin remote.
  }
  return null;
}

async function githubFetch(token, method, apiPath, body) {
  const res = await fetch("https://api.github.com" + apiPath, {
    method,
    headers: {
      authorization: "Bearer " + token,
      accept: "application/vnd.github+json",
      "user-agent": "loom-local-task-runner",
      "x-github-api-version": "2022-11-28",
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    json = null;
  }
  return { ok: res.ok, status: res.status, json, text };
}

// scrubToken removes a credential from text before it is surfaced in an error
// message or log, covering both the bare token and any `x-access-token:...@`
// URL form.
export function scrubToken(text, ...tokens) {
  let out = String(text || "");
  for (const token of tokens) {
    if (token) {
      out = out.split(token).join("***");
    }
  }
  return out.replace(/x-access-token:[^@\s]+@/g, "x-access-token:***@");
}

// shannonEntropy is the byte-frequency Shannon entropy of s (bits/symbol), ported
// verbatim from internal/sessions/redact/redact.go. The secret segments it scores
// are ASCII by construction, so per-char iteration matches Go's per-byte.
function shannonEntropy(s) {
  if (!s) {
    return 0;
  }
  const freq = new Map();
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    freq.set(ch, (freq.get(ch) || 0) + 1);
  }
  let entropy = 0;
  for (const count of freq.values()) {
    const p = count / s.length;
    entropy -= p * Math.log2(p);
  }
  return entropy;
}

// SECRET_PATTERNS is a curated, high-precision subset of the gitleaks default
// ruleset the Go redactor applies. These prefixed shapes have near-zero false
// positives and catch structured secrets whose entropy is below threshold (a PEM
// block, or a JWT broken into low-entropy dot-separated segments). The full
// 180-rule gitleaks set is a follow-up; the canonical Go redactor (gitleaks +
// entropy) still runs on the native-transcript path.
const SECRET_PATTERNS = [
  /-----BEGIN[A-Z ]*PRIVATE KEY-----[\s\S]*?-----END[A-Z ]*PRIVATE KEY-----/g, // PEM private keys
  /AKIA[0-9A-Z]{16}/g, // AWS access key id
  /gh[pousr]_[A-Za-z0-9]{30,}/g, // GitHub tokens (ghp_/gho_/ghu_/ghs_/ghr_)
  /github_pat_[A-Za-z0-9_]{60,}/g, // GitHub fine-grained PAT
  /sk-(?:ant-)?[A-Za-z0-9_-]{20,}/g, // OpenAI / Anthropic API keys
  /AIza[0-9A-Za-z_-]{35}/g, // Google API key
  /xox[baprs]-[A-Za-z0-9-]{10,}/g, // Slack token
  /eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}/g, // JWT
];

// redactSecretsInText replaces secrets in text with "REDACTED", flagging a
// substring if EITHER its Shannon entropy exceeds 4.5 OR it matches a known secret
// shape — the layered approach of internal/sessions/redact/redact.go.
export function redactSecretsInText(text) {
  const s = text == null ? "" : String(text);
  if (!s) {
    return s;
  }
  const regions = [];
  // Entropy layer: high-entropy [A-Za-z0-9+_=-]{10,} segments (/ excluded so file
  // paths are not matched as one token).
  const segment = /[A-Za-z0-9+_=-]{10,}/g;
  let m;
  while ((m = segment.exec(s)) !== null) {
    let start = m.index;
    const end = start + m[0].length;
    // Don't consume a character that is part of a JSON escape sequence (e.g. the
    // 'n' in "\n"), which would leave a dangling backslash before "REDACTED".
    if (start > 0 && s[start - 1] === "\\" && "ntrbfu\"\\/".includes(s[start])) {
      start += 1;
      if (end - start < 10) {
        continue;
      }
    }
    if (shannonEntropy(s.slice(start, end)) > 4.5) {
      regions.push([start, end]);
    }
  }
  // Pattern layer: known high-precision secret shapes.
  for (const pattern of SECRET_PATTERNS) {
    pattern.lastIndex = 0;
    let pm;
    while ((pm = pattern.exec(s)) !== null) {
      regions.push([pm.index, pm.index + pm[0].length]);
      if (pm[0].length === 0) {
        pattern.lastIndex += 1;
      }
    }
  }
  if (!regions.length) {
    return s;
  }
  regions.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const merged = [];
  for (const [start, end] of regions) {
    const last = merged[merged.length - 1];
    if (last && start <= last[1]) {
      last[1] = Math.max(last[1], end);
    } else {
      merged.push([start, end]);
    }
  }
  let out = "";
  let cursor = 0;
  for (const [start, end] of merged) {
    out += s.slice(cursor, start) + "REDACTED";
    cursor = end;
  }
  return out + s.slice(cursor);
}

// redactTranscriptSecrets scrubs secrets the agent could have echoed into tool
// output/text — now persisted via the `output` field — before the transcript is
// written: first the exact values of known secret env vars, then entropy/pattern
// detection for secrets that are NOT in that env allowlist.
function redactTranscriptSecrets(entries, env = process.env) {
  const names = [
    "GITHUB_TOKEN", "GH_TOKEN", "LOOM_PR_GIT_PASSWORD",
    "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_API_KEY",
    "GEMINI_API_KEY", "GOOGLE_API_KEY", "CURSOR_API_KEY",
    "LOOM_FLEET_DB_API_KEY", "LOOM_TASK_RUN_LEASE_TOKEN",
  ];
  const secrets = [];
  for (const name of names) {
    const value = env[name];
    if (value && String(value).length >= 8) {
      secrets.push(String(value));
    }
  }
  const redact = (value) => redactSecretsInText(secrets.length ? scrubToken(value, ...secrets) : value);
  for (const entry of entries) {
    if (entry.text) {
      entry.text = redact(entry.text);
    }
    if (entry.output) {
      entry.output = redact(entry.output);
    }
  }
  return entries;
}

// deliverPullRequest commits the isolated worktree's changes onto a branch,
// pushes it (token supplied via env-backed credential helper, never in argv),
// and opens (or finds) a PR. Returns { url, number, branch }.
async function deliverPullRequest({ isolatedPath, token, owner, repo, base, branch, title, body }) {
  const git = async (...args) => {
    const r = await execBackend("git", ["-C", isolatedPath, ...args], { cwd: isolatedPath });
    if (r.code !== 0) {
      throw new Error("git " + args[0] + " failed: " + textTail(r.stderr || r.stdout, 400));
    }
    return r;
  };
  await git("checkout", "-b", branch);
  await git("add", "-A");
  await git("-c", "user.email=loom@example.test", "-c", "user.name=Loom", "commit", "-m", title);
  // Push WITHOUT the token in argv: supply it through a credential helper that
  // reads it from the subprocess ENV (LOOM_PR_GIT_PASSWORD), with a token-free
  // remote URL. This keeps the credential out of `ps`/argv and out of git's URL
  // logging. The empty `credential.helper=` first clears any inherited helper.
  const pushRes = await execBackend(
    "git",
    [
      "-C", isolatedPath,
      "-c", "credential.helper=",
      "-c", 'credential.helper=!f() { echo username=x-access-token; echo "password=$LOOM_PR_GIT_PASSWORD"; }; f',
      "push", "--force",
      "https://github.com/" + owner + "/" + repo + ".git",
      "HEAD:refs/heads/" + branch,
    ],
    { cwd: isolatedPath, env: { ...process.env, LOOM_PR_GIT_PASSWORD: token, GIT_TERMINAL_PROMPT: "0" } },
  );
  if (pushRes.code !== 0) {
    throw new Error("git push failed: " + scrubToken(textTail(pushRes.stderr || pushRes.stdout, 400), token));
  }

  const created = await githubFetch(token, "POST", "/repos/" + owner + "/" + repo + "/pulls", {
    title,
    head: branch,
    base,
    body,
    draft: true,
  });
  if (created.ok && created.json) {
    return { url: created.json.html_url, number: created.json.number, branch };
  }
  if (created.status === 422) {
    const q = new URLSearchParams({ state: "open", head: owner + ":" + branch, base });
    const existing = await githubFetch(token, "GET", "/repos/" + owner + "/" + repo + "/pulls?" + q.toString());
    if (existing.ok && Array.isArray(existing.json) && existing.json.length > 0) {
      return { url: existing.json[0].html_url, number: existing.json[0].number, branch };
    }
  }
  throw new Error("PR create failed (" + created.status + "): " + textTail(created.text, 400));
}

// deliverStackBranch commits the in-place (per-task) worktree's changes and
// pushes them to the canonical stack branch — WITHOUT opening a PR. The worktree
// is already a detached checkout on the predecessor's branch (the host resolver
// cut it there), so the commit lands directly on top of the predecessor and the
// pushed branch's base == the predecessor branch by construction. The post-drain
// reconcile opens/links the PR and sets bases. Returns { branch, head }.
async function deliverStackBranch({ worktreePath, token, owner, repo, branch, title }) {
  const git = async (...args) => {
    const r = await execBackend("git", ["-C", worktreePath, ...args], { cwd: worktreePath });
    if (r.code !== 0) {
      throw new Error("git " + args[0] + " failed: " + textTail(r.stderr || r.stdout, 400));
    }
    return r;
  };
  await git("add", "-A");
  await git("-c", "user.email=loom@example.test", "-c", "user.name=Loom", "commit", "-m", title);
  const head = stringValue((await git("rev-parse", "HEAD")).stdout).trim();
  // Push token via an env-backed credential helper (never in argv), token-free
  // remote URL — same hardening as deliverPullRequest. Each task owns its
  // canonical branch, so --force is safe (only this task pushes loom/stack/.../<task>).
  const pushRes = await execBackend(
    "git",
    [
      "-C", worktreePath,
      "-c", "credential.helper=",
      "-c", 'credential.helper=!f() { echo username=x-access-token; echo "password=$LOOM_PR_GIT_PASSWORD"; }; f',
      "push", "--force",
      "https://github.com/" + owner + "/" + repo + ".git",
      "HEAD:refs/heads/" + branch,
    ],
    { cwd: worktreePath, env: { ...process.env, LOOM_PR_GIT_PASSWORD: token, GIT_TERMINAL_PROMPT: "0" } },
  );
  if (pushRes.code !== 0) {
    throw new Error("git push failed: " + scrubToken(textTail(pushRes.stderr || pushRes.stdout, 400), token));
  }
  return { branch, head };
}

async function gitHead(worktree) {
  try {
    const result = await execBackend("git", ["-C", worktree, "rev-parse", "HEAD"], { cwd: worktree });
    return result.code === 0 ? result.stdout.trim() : "";
  } catch {
    return "";
  }
}

// capturePatch records untracked files (git add -N) and returns a binary diff
// plus diffstat-derived counts, mirroring the Daytona runner's patch capture. It
// diffs against `base` (when known) rather than HEAD/the index, so a change the
// agent COMMITTED in this worktree is captured too — not just uncommitted working-
// tree edits. Without a base it falls back to the working-tree diff. This keeps the
// captured patch complete whether the agent committed (the daemon TS leaf's prompt
// asks it to) or left the change in the working tree.
async function capturePatch(worktree, base) {
  const head = await gitHead(worktree);
  try {
    await execBackend("git", ["-C", worktree, "add", "-N", "--", "."], { cwd: worktree });
  } catch {
    // best-effort: an empty or non-git worktree just yields an empty patch.
  }
  const range = base ? [base] : [];
  let patch = "";
  try {
    const diff = await execBackend("git", ["-C", worktree, "diff", "--binary", ...range, "--", "."], { cwd: worktree });
    if (diff.code === 0) {
      patch = diff.stdout;
    }
  } catch {
    patch = "";
  }
  let stat = "";
  try {
    const diffStat = await execBackend("git", ["-C", worktree, "diff", "--numstat", ...range, "--", "."], { cwd: worktree });
    if (diffStat.code === 0) {
      stat = diffStat.stdout;
    }
  } catch {
    stat = "";
  }
  const counts = parseNumstat(stat);
  return { head, patch, ...counts };
}

// parseNumstat sums added/removed lines and counts changed files from
// `git diff --numstat` output. Binary files appear as "-\t-\t<path>".
export function parseNumstat(numstat) {
  let filesChanged = 0;
  let linesAdded = 0;
  let linesRemoved = 0;
  for (const line of String(numstat || "").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) {
      continue;
    }
    const parts = trimmed.split("\t");
    if (parts.length < 3) {
      continue;
    }
    filesChanged += 1;
    const added = Number(parts[0]);
    const removed = Number(parts[1]);
    if (Number.isFinite(added)) {
      linesAdded += added;
    }
    if (Number.isFinite(removed)) {
      linesRemoved += removed;
    }
  }
  return { filesChanged, linesAdded, linesRemoved };
}

// buildPrompt mirrors daytona-task-runner.ts buildPrompt (default mode): a
// focused instruction to implement the single child task in the worktree.
function buildPrompt(request, task, worktree) {
  return [
    "You are implementing one child task from a Loom epic runner workflow.",
    "Repository cwd: " + worktree,
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

// loadTask resolves task context via @loom/sdk/runner when available, falling
// back to the request input. The SDK is imported lazily so this module is
// loadable (and the runner testable) outside the bundled context.
async function loadTask(request, logs) {
  try {
    const { TaskRunClient } = await import("@loom/sdk/runner");
    const client = TaskRunClient.fromEnv();
    const task = await client.getTask();
    logs.push("loaded Loom task context through @loom/sdk/runner");
    return task;
  } catch (error) {
    logs.push("warning: task context lookup failed: " + errorMessage(error));
    return request.input && typeof request.input === "object" ? request.input : null;
  }
}

async function requestPayload(ctx) {
  if (ctx && ctx.payload && typeof ctx.payload === "object") {
    return ctx.payload;
  }
  if (ctx && ctx.request && typeof ctx.request === "object") {
    return ctx.request;
  }
  try {
    const { TaskRunClient } = await import("@loom/sdk/runner");
    return TaskRunClient.fromEnv().request();
  } catch {
    try {
      return JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}");
    } catch {
      return {};
    }
  }
}

function failed(errorClass, message, info) {
  const logs = info.logs || [];
  return {
    status: "failed",
    exitCode: 1,
    errorClass,
    errorMessage: textTail(message),
    logs: logs.concat([errorClass + ": " + message]).join("\n") + "\n",
    runtimeMetadata: stringMetadata({
      task_runner: "local-task-runner",
      runtime_strategy: info.backend ? "local-cli-" + info.backend : "local",
      runner: (info.request && (info.request.runner || info.request.runner_ref)) || "local-task-runner",
      backend: info.backend,
      task_id: info.taskId,
      repo_head_before: info.headBefore,
      phase: errorClass,
    }),
  };
}

function stringMetadata(values = {}) {
  const out = {};
  for (const [key, value] of Object.entries(values || {})) {
    if (value === undefined || value === null || value === "") {
      continue;
    }
    out[key] = typeof value === "string" ? value : String(value);
  }
  return out;
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
