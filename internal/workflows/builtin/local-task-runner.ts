import { execFile } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { defineAgent, defineWorkflow } from "@flue/runtime";

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
// loom's local/agent tooling uses (claude/codex/opencode/gemini/cursor) directly.
// The checked-in local-mode harness additionally supplies `localdogfood`, a
// deterministic executable backend used to prove orchestration without model
// auth or spend. It follows the same fail-closed binary/exit-code contract,
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
  localdogfood: "localdogfood", // deterministic executable shipped only by local-mode
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
  // A workflow can also supply the codex prompt as DATA via the task-run Input
  // (input.taskPrompt), mirroring daytona-task-runner — this is what makes
  // "prompt = data, brain stays custom" true on the default process deployment.
  // Precedence: the daemon-leaf env override (its exact composed prompt) wins,
  // then input.taskPrompt (custom workflows), else this runner's generic prompt.
  const inputPrompt = stringValue(inputValue(request, "taskPrompt"));
  const prompt = typeof promptOverride === "string" && promptOverride.trim() !== ""
    ? promptOverride
    : inputPrompt.trim() !== ""
      ? inputPrompt
      : buildPrompt(request, task, execWorktree);
  const args = backendArgs(backend, execWorktree, prompt);
  const usesStdinPrompt = backendUsesStdinPrompt(backend);

  const openPR = booleanValue(inputValue(request, "openPullRequest"));
  const deliveryMode = stringValue(inputValue(request, "deliveryMode"));
  const originRemoteUrl = await resolveOriginRemoteUrl(execWorktree);
  const filesystemOrigin = isFilesystemOrigin(originRemoteUrl);
  const localBranchRequested = deliveryMode === "local-branch";
  // Local-branch delivery is intentionally narrow: it is allowed only for local
  // filesystem remotes, where an exact scanned commit SHA is pushed to the bare
  // repo used by local-mode. An explicit deliveryMode on a GitHub/http/ssh origin
  // does NOT activate this path; those deployments keep the existing patch-back
  // or PR behavior instead of silently inventing a "local" publish.
  const localBranchDelivery = !stacked && filesystemOrigin && localBranchRequested;
  if (localBranchRequested && !filesystemOrigin) {
    logs.push("local-branch delivery requested but origin is not a filesystem path; keeping existing delivery behavior");
  }
  let exitCode;
  let stdout = "";
  let stderr = "";
  let patchInfo;
  let prInfo = null;
  let stackInfo = null;
  let localBranchInfo = null;
  let prFailure = null;
  // A backend may discover that the reviewed design is fundamentally invalid.
  // It cannot mutate Work Item lifecycle while the typed TaskRun generation is
  // live, and free-form stdout is not a trustworthy terminal protocol. Give it
  // a per-invocation, runner-owned file channel instead; only the exact schema
  // validated below can alter the TaskRun disposition.
  const taskOutcomeChannel = createTaskOutcomeChannel();
  let taskOutcome = null;
  try {
    let result;
    try {
      result = await execBackend(binary, args, {
        cwd: execWorktree,
        input: usesStdinPrompt ? prompt : undefined,
        live: true,
        env: { ...process.env, LOOM_TASK_OUTCOME_FILE: taskOutcomeChannel.file },
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
    taskOutcome = readTaskOutcome(taskOutcomeChannel.file);
    if (taskOutcome) {
      logs.push(taskOutcome.invalid
        ? "task outcome rejected: " + taskOutcome.invalid
        : "task outcome reported: " + taskOutcome.disposition);
    }

    logs.push(`${backend} CLI exit=${exitCode}`);
    if (stdout.trim()) {
      logs.push(redactRunnerText(textTail(stdout, 4000)));
    }
    if (stderr.trim()) {
      logs.push("stderr:\n" + redactRunnerText(textTail(stderr, 2000)));
    }

    patchInfo = await capturePatch(execWorktree, baseRef);
    if (patchInfo.captureFailed || (patchInfo.gitBacked && await capturedChangesContainRunnerSecret(execWorktree, baseRef, patchInfo.patch))) {
      logs.push(patchInfo.captureFailed
        ? "captured changes rejected because inspection did not complete"
        : "captured changes rejected because they contain an inherited credential");
      const failureClass = patchInfo.captureFailed ? "local_patch_capture_failed" : "local_patch_contains_credential";
      const failureMessage = patchInfo.captureFailed
        ? "captured changes could not be inspected completely and were not published or persisted"
        : "captured changes contain an inherited credential and were not published or persisted";
      if (stacked) {
        await discardRejectedStackedChanges(execWorktree, baseRef);
      }
      return failed(failureClass, failureMessage, {
        taskRunId,
        taskId,
        backend,
        request,
        logs,
        headBefore,
      });
    }

    // Local filesystem-origin delivery: commit (if needed) in the isolated
    // worktree and push the task branch to the ACTUAL origin remote. This path
    // gives the local review lane a real ref to inspect without pretending a
    // GitHub PR exists.
    if (localBranchDelivery && exitCode === 0) {
      if (!isolated) {
        prFailure = { class: "local_branch_worktree_missing", message: "local-branch delivery requires a git isolated worktree" };
      } else if (patchInfo.filesChanged === 0) {
        logs.push("local-branch: the agent produced no changes; no branch pushed");
      } else {
        const branch = localBranchName(taskId, taskRunId);
        const title = stringValue(inputValue(request, "branchTitle"))
          || stringValue(inputValue(request, "prTitle"))
          || stringValue((task && (task.title || task.name)) || ("Loom task " + (taskId || taskRunId)));
        try {
          localBranchInfo = await deliverLocalBranch({ worktreePath: execWorktree, baseRef, branch, title });
          logs.push("pushed local branch " + localBranchInfo.branch + " @ " + localBranchInfo.head.slice(0, 12));
        } catch (error) {
          prFailure = isRunnerCredentialDeliveryError(error)
            ? { class: "local_patch_contains_credential", message: "captured changes contain an inherited credential and were not published" }
            : { class: "local_branch_push_failed", message: "failed to push local branch: " + errorMessage(error) };
        }
      }
    // Stacked delivery: commit in place and push the canonical branch on the
    // predecessor base. No PR is opened here — the post-drain reconcile does it.
    } else if (stacked && exitCode === 0) {
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
          const title = stringValue(inputValue(request, "prTitle")) || stringValue((task && (task.title || task.name)) || ("Loom task " + (taskId || taskRunId)));
          try {
            stackInfo = await deliverStackBranch({ worktreePath: execWorktree, baseRef, token, owner: slug.owner, repo: slug.repo, branch: stackBranch, title });
            logs.push("pushed stack branch " + stackInfo.branch + " @ " + stackInfo.head.slice(0, 12));
          } catch (error) {
            prFailure = isRunnerCredentialDeliveryError(error)
              ? { class: "local_patch_contains_credential", message: "captured changes contain an inherited credential and were not published" }
              : { class: "stack_push_failed", message: "failed to push stack branch: " + errorMessage(error) };
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
          const title = stringValue(inputValue(request, "prTitle")) || stringValue((task && (task.title || task.name)) || ("Loom task " + (taskId || taskRunId)));
          const prBody = stringValue(inputValue(request, "prBody")) || ("Automated change by the Loom local-task-runner (" + backend + "). Task " + (taskId || taskRunId) + ".");
          try {
            prInfo = await deliverPullRequest({ isolatedPath: isolated.path, deliveryBaseRef: baseRef, token, owner: slug.owner, repo: slug.repo, base, branch, title, body: prBody });
            logs.push("opened pull request " + prInfo.url);
          } catch (error) {
            prFailure = isRunnerCredentialDeliveryError(error)
              ? { class: "local_patch_contains_credential", message: "captured changes contain an inherited credential and were not published" }
              : { class: "github_pr_failed", message: "failed to open pull request: " + errorMessage(error) };
          }
        }
      }
    }
  } finally {
    if (isolated) {
      await removeIsolatedWorktree(worktree, isolated.path, logs);
    }
    cleanupTaskOutcomeChannel(taskOutcomeChannel);
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
  const acceptedTaskOutcome = taskOutcome && !taskOutcome.invalid && exitCode === 0 && !streamFailure
    ? taskOutcome
    : null;
  if (taskOutcome && !taskOutcome.invalid && !acceptedTaskOutcome) {
    logs.push("task outcome ignored because the backend did not finish cleanly");
  }

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
    repo_head_after: localBranchInfo ? localBranchInfo.head : stackInfo ? stackInfo.head : patchInfo.head,
    files_changed: String(patchInfo.filesChanged),
    lines_added: String(patchInfo.linesAdded),
    lines_removed: String(patchInfo.linesRemoved),
    cli_exit_code: String(exitCode),
  });
  if (streamFailure) {
    metadata.stream_error = redactRunnerText(streamFailure);
  }
  if (acceptedTaskOutcome) {
    metadata.task_outcome = acceptedTaskOutcome.disposition;
    metadata.task_outcome_summary = redactRunnerText(acceptedTaskOutcome.summary);
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
  } else if (localBranchInfo) {
    metadata.delivery = "local_branch";
    metadata.local_branch = localBranchInfo.branch;
    metadata.head_sha = localBranchInfo.head;
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

  if (taskOutcome && taskOutcome.invalid) {
    return {
      status: "failed",
      exitCode: 1,
      errorClass: "local_task_outcome_invalid",
      errorMessage: redactRunnerText(taskOutcome.invalid),
      logs: renderLogs(logs),
      logsRef: "logs://" + taskRunId,
      ...taskUsage,
      transcript_entries: transcriptEntries,
      patch: patchInfo.patch,
      base_ref: baseRef,
      patch_base_ref: baseRef,
      runtimeMetadata: { ...metadata, phase: "local_task_outcome_invalid" },
    };
  }

  if (acceptedTaskOutcome && acceptedTaskOutcome.disposition === "needs_revision") {
    const needsRevision = {
      // Intentional cancellation is terminal and non-retryable at the worker:
      // the backend ran successfully, but the implementation TaskRun must not
      // close a Work Item whose design needs another planner pass.
      status: "cancelled",
      exitCode: 0,
      errorClass: "task_needs_revision",
      errorMessage: redactRunnerText(acceptedTaskOutcome.summary),
      logs: renderLogs(logs),
      logsRef: "logs://" + taskRunId,
      ...taskUsage,
      transcript_entries: transcriptEntries,
      runtimeMetadata: { ...metadata, phase: "needs_revision" },
    };
    if (!(prInfo || stackInfo || localBranchInfo || stacked)) {
      needsRevision.patch = patchInfo.patch;
      needsRevision.base_ref = baseRef;
      needsRevision.patch_base_ref = baseRef;
    }
    return needsRevision;
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
      errorMessage: redactRunnerText(failureMessage),
      logs: renderLogs(logs),
      logsRef: "logs://" + taskRunId,
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
    logs: renderLogs(logs),
    logsRef: "logs://" + taskRunId,
    ...taskUsage,
    transcript_entries: transcriptEntries,
    runtimeMetadata: metadata,
  };
  if (prInfo || stackInfo || localBranchInfo || stacked) {
    // PR / stacked / local-branch mode: the published ref IS the delivery (and
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

const TASK_OUTCOME_MAX_BYTES = 16 * 1024;
const TASK_OUTCOME_SUMMARY_MAX_CHARS = 2000;

// createTaskOutcomeChannel allocates outside the repository so the protocol
// file can never leak into a patch/commit. The child receives only the exact
// file path via LOOM_TASK_OUTCOME_FILE; the runner owns validation and cleanup.
function createTaskOutcomeChannel() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "loom-task-outcome-"));
  return { dir, file: path.join(dir, "outcome.json") };
}

function cleanupTaskOutcomeChannel(channel) {
  if (!channel || !channel.dir) return;
  try {
    fs.rmSync(channel.dir, { recursive: true, force: true });
  } catch {
    // Best-effort cleanup: the directory contains no credentials or repo data.
  }
}

// readTaskOutcome is deliberately a closed enum. Unknown/malformed content is
// an explicit fail-closed result, while absence means ordinary task completion.
function readTaskOutcome(filePath) {
  let stat;
  try {
    stat = fs.lstatSync(filePath);
  } catch (err) {
    if (err && err.code === "ENOENT") return null;
    return { invalid: "cannot inspect LOOM_TASK_OUTCOME_FILE: " + errorMessage(err) };
  }
  if (!stat.isFile() || stat.isSymbolicLink()) {
    return { invalid: "LOOM_TASK_OUTCOME_FILE must be a regular file" };
  }
  if (stat.size < 2 || stat.size > TASK_OUTCOME_MAX_BYTES) {
    return { invalid: "LOOM_TASK_OUTCOME_FILE size must be between 2 and " + TASK_OUTCOME_MAX_BYTES + " bytes" };
  }
  let value;
  try {
    value = JSON.parse(fs.readFileSync(filePath, "utf8"));
  } catch (err) {
    return { invalid: "LOOM_TASK_OUTCOME_FILE is not valid JSON: " + errorMessage(err) };
  }
  if (!value || typeof value !== "object" || Array.isArray(value)
      || value.version !== 1 || value.disposition !== "needs_revision") {
    return { invalid: "task outcome must be {version:1, disposition:\"needs_revision\", summary:string}" };
  }
  const keys = Object.keys(value).sort();
  if (keys.join(",") !== "disposition,summary,version") {
    return { invalid: "task outcome contains unsupported fields" };
  }
  const summary = typeof value.summary === "string" ? value.summary.trim() : "";
  if (!summary || summary.length > TASK_OUTCOME_SUMMARY_MAX_CHARS) {
    return { invalid: "task outcome summary must contain 1-" + TASK_OUTCOME_SUMMARY_MAX_CHARS + " characters" };
  }
  return { disposition: "needs_revision", summary };
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
    case "localdogfood":
      // The deterministic local-mode executable selects planner/coder behavior
      // from a marker in the prompt delivered over stdin. Keeping the prompt out
      // of argv matches the headless codex/OpenCode path and avoids shell parsing.
      return ["invoke"];
    default:
      return [prompt];
  }
}

// backendUsesStdinPrompt reports whether the prompt is delivered over stdin
// rather than as a positional argument. OpenCode (harnessInvocation.Prompt) and
// headless codex (trailing "-") both read the prompt from stdin.
function backendUsesStdinPrompt(backend) {
  return backend === "opencode" || backend === "codex" || backend === "localdogfood";
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
      // Signal backend activity on OUR stderr so, when the daemon leaf runs this
      // runner (Phase U), the supervisor's output-timeout watchdog — which stats the
      // agent log mtime — sees per-turn activity. Never tee the backend bytes themselves:
      // a credential may be split across stream chunks, which makes per-chunk redaction
      // unsafe. The persisted result carries the redacted diagnostic tail instead.
      let lastSignalAt = 0;
      const signalActivity = () => {
        const now = Date.now();
        if (lastSignalAt === 0 || now - lastSignalAt >= 1000) {
          lastSignalAt = now;
          process.stderr.write("[loom task-runner] backend activity\n");
        }
      };
      if (child.stdout) child.stdout.on("data", signalActivity);
      if (child.stderr) child.stderr.on("data", signalActivity);
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
  const secrets = runnerSecretValues(env);
  const redact = (value) => redactSecretsInText(secrets.length ? scrubToken(value, ...secrets) : value);
  return entries.map((entry) => redactStructuredRunnerValue(entry, redact));
}

function redactStructuredRunnerValue(value, redact) {
  if (typeof value === "string") {
    return redact(value);
  }
  if (Array.isArray(value)) {
    return value.map((item) => redactStructuredRunnerValue(item, redact));
  }
  if (value && typeof value === "object") {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      Object.defineProperty(out, redact(key), {
        value: redactStructuredRunnerValue(item, redact),
        enumerable: true,
        configurable: true,
        writable: true,
      });
    }
    return out;
  }
  return value;
}

// runnerSecretValues returns exact credential values inherited by the backend.
// Exact-value scrubbing complements entropy/pattern detection and covers Loom's
// run-scoped tokens even when a deterministic test token has deliberately low
// entropy. Sorting longest-first avoids exposing a suffix when values overlap.
function runnerSecretValues(env = process.env) {
  const secretName = /(?:^|_)(?:TOKEN|API_KEY|SECRET|PASSWORD|CREDENTIAL|PRIVATE_KEY|SIGNING_KEY|ACCESS_KEY)$/i;
  return [...new Set(Object.entries(env || {})
    .filter(([name, value]) => secretName.test(name) && value && String(value).length >= 8)
    .map(([, value]) => String(value)))]
    .sort((a, b) => b.length - a.length);
}

function redactRunnerText(value, env = process.env) {
  const secrets = runnerSecretValues(env);
  const text = secrets.length ? scrubToken(value, ...secrets) : String(value || "");
  return redactSecretsInText(text);
}

function renderLogs(logs, env = process.env) {
  return redactRunnerText((logs || []).join("\n") + "\n", env);
}

async function capturedChangesContainRunnerSecret(worktree, base, patch, env = process.env) {
  const secrets = runnerSecretValues(env);
  if (secrets.length === 0) {
    return false;
  }
  const capturedPatch = String(patch || "");
  if (secrets.some((secret) => capturedPatch.includes(secret))) {
    return true;
  }

  let changed;
  try {
    const range = base ? [base] : [];
    const result = await execBackend("git", ["-C", worktree, "diff", "--name-only", "-z", ...range, "--", "."], { cwd: worktree });
    if (result.code !== 0) {
      return true;
    }
    changed = result.stdout.split("\0").filter(Boolean);
  } catch {
    return true;
  }

  const root = path.resolve(worktree);
  for (const relative of changed) {
    const target = path.resolve(root, relative);
    if (target !== root && !target.startsWith(root + path.sep)) {
      return true;
    }
    let stat;
    try {
      stat = fs.lstatSync(target);
    } catch (error) {
      if (error && error.code === "ENOENT") {
        continue;
      }
      return true;
    }
    if (stat.isSymbolicLink()) {
      let link;
      try {
        link = fs.readlinkSync(target);
      } catch {
        return true;
      }
      if (secrets.some((secret) => link.includes(secret))) {
        return true;
      }
      continue;
    }
    if (stat.isFile() && fileContainsRunnerSecret(target, secrets)) {
      return true;
    }
  }
  return false;
}

function fileContainsRunnerSecret(file, secrets) {
  const needles = secrets.map((secret) => Buffer.from(secret)).filter((secret) => secret.length > 0);
  if (needles.length === 0) {
    return false;
  }
  const overlap = Math.max(...needles.map((secret) => secret.length)) - 1;
  const buffer = Buffer.allocUnsafe(64 * 1024);
  let carry = Buffer.alloc(0);
  let fd;
  try {
    fd = fs.openSync(file, "r");
    for (;;) {
      const read = fs.readSync(fd, buffer, 0, buffer.length, null);
      if (read === 0) {
        return false;
      }
      const window = Buffer.concat([carry, buffer.subarray(0, read)]);
      if (needles.some((secret) => window.includes(secret))) {
        return true;
      }
      carry = overlap > 0 ? window.subarray(Math.max(0, window.length - overlap)) : Buffer.alloc(0);
    }
  } catch {
    return true;
  } finally {
    if (fd !== undefined) {
      try {
        fs.closeSync(fd);
      } catch {
        // A failed close does not change the completed scan result.
      }
    }
  }
}

// committedChangesContainRunnerSecret scans the exact Git objects that would
// be published, not the mutable worktree. The earlier worktree/patch scan
// catches ordinary output before commit; this second boundary closes the race
// where a detached backend child or Git clean filter changes the staged tree
// between capture and branch delivery. Both sides of each changed path are
// inspected so a deletion cannot publish a credential in a removed line.
async function committedChangesContainRunnerSecret(worktree, base, head = "HEAD", extraSecrets = [], env = process.env) {
  const secrets = [...new Set([
    ...runnerSecretValues(env),
    ...extraSecrets.map((value) => String(value || "")).filter((value) => value.length >= 8),
  ])].sort((a, b) => b.length - a.length);
  if (secrets.length === 0) {
    return false;
  }
  try {
    const patch = await execBackend("git", ["-C", worktree, "diff", "--no-ext-diff", "--binary", "--no-renames", base, head, "--", "."], { cwd: worktree });
    if (patch.code !== 0 || secrets.some((secret) => patch.stdout.includes(secret))) {
      return true;
    }
    const names = await execBackend("git", ["-C", worktree, "diff", "--no-renames", "--name-only", "-z", base, head, "--", "."], { cwd: worktree });
    if (names.code !== 0) {
      return true;
    }
    for (const relative of names.stdout.split("\0").filter(Boolean)) {
      for (const revision of [base, head]) {
        const entry = await execBackend("git", ["-C", worktree, "ls-tree", "-z", revision, "--", relative], { cwd: worktree });
        if (entry.code !== 0) {
          return true;
        }
        if (entry.stdout === "") {
          continue;
        }
        const object = await execBackend("git", ["-C", worktree, "cat-file", "-p", `${revision}:./${relative}`], { cwd: worktree });
        if (object.code !== 0 || secrets.some((secret) => object.stdout.includes(secret))) {
          return true;
        }
      }
    }
    return false;
  } catch {
    return true;
  }
}

async function secureCommitForDelivery({ git, worktreePath, baseRef, title, extraSecrets = [] }) {
  const requestedBase = stringValue(baseRef) || "HEAD";
  const scanBase = stringValue((await git("rev-parse", requestedBase)).stdout).trim();
  await git("add", "-A");
  const staged = await capturePatch(worktreePath, scanBase);
  if (staged.captureFailed || await capturedChangesContainRunnerSecret(worktreePath, scanBase, staged.patch)) {
    await discardRejectedCredentialChanges(git, scanBase);
    throw runnerCredentialDeliveryError(staged.captureFailed
      ? "captured changes could not be inspected completely"
      : "captured changes contain an inherited credential");
  }
  // A backend is allowed to commit its own output. In that case `git add -A`
  // leaves the index equal to HEAD even though HEAD differs from scanBase. Reuse
  // that commit instead of attempting an empty runner commit. Conversely, never
  // turn a truly empty run into a branch publication if the pre-delivery state
  // changed after the caller's initial patch capture.
  const stagedNames = await git("diff", "--cached", "--name-only", "-z", "--", ".");
  let head = stringValue((await git("rev-parse", "HEAD")).stdout).trim();
  if (stagedNames.stdout !== "") {
    await git("-c", "user.email=loom@example.test", "-c", "user.name=Loom", "commit", "--no-verify", "-m", redactRunnerText(title));
    head = stringValue((await git("rev-parse", "HEAD")).stdout).trim();
  } else if (head === scanBase || staged.filesChanged === 0) {
    throw new Error("no changes to deliver");
  }
  if (await committedChangesContainRunnerSecret(worktreePath, scanBase, head, extraSecrets)) {
    await discardRejectedCredentialChanges(git, scanBase);
    throw runnerCredentialDeliveryError("committed changes contain an inherited credential");
  }
  return head;
}

function branchPushRefspec(head, branch) {
  return head + ":refs/heads/" + branch;
}

async function discardRejectedCredentialChanges(git, base) {
  try {
    await git("reset", "--hard", base);
  } catch {
    // Publication still fails closed even if best-effort local cleanup fails.
  }
  try {
    await git("clean", "-fd");
  } catch {
    // The caller returns a credential-specific failure and never pushes.
  }
}

async function discardRejectedStackedChanges(worktree, base) {
  const resetBase = stringValue(base) || "HEAD";
  try {
    await execBackend("git", ["-C", worktree, "reset", "--hard", resetBase], { cwd: worktree });
    await execBackend("git", ["-C", worktree, "clean", "-fd"], { cwd: worktree });
  } catch {
    // The failed TaskRun never returns or publishes the rejected patch.
  }
}

function runnerCredentialDeliveryError(message) {
  const error = new Error(message);
  error.code = "LOCAL_PATCH_CONTAINS_CREDENTIAL";
  return error;
}

function isRunnerCredentialDeliveryError(error) {
  return error && error.code === "LOCAL_PATCH_CONTAINS_CREDENTIAL";
}

// deliverPullRequest commits the isolated worktree's changes onto a branch,
// pushes it (token supplied via env-backed credential helper, never in argv),
// and opens (or finds) a PR. Returns { url, number, branch }.
async function deliverPullRequest({ isolatedPath, deliveryBaseRef, token, owner, repo, base, branch, title, body }) {
  const git = async (...args) => {
    const r = await execBackend("git", ["-C", isolatedPath, "-c", "core.hooksPath=/dev/null", ...args], { cwd: isolatedPath });
    if (r.code !== 0) {
      throw new Error("git " + args[0] + " failed: " + textTail(r.stderr || r.stdout, 400));
    }
    return r;
  };
  await git("checkout", "-b", branch);
  const head = await secureCommitForDelivery({ git, worktreePath: isolatedPath, baseRef: deliveryBaseRef, title, extraSecrets: [token] });
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
      "push", "--no-verify", "--force",
      "https://github.com/" + owner + "/" + repo + ".git",
      branchPushRefspec(head, branch),
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
async function deliverStackBranch({ worktreePath, baseRef, token, owner, repo, branch, title }) {
  const git = async (...args) => {
    const r = await execBackend("git", ["-C", worktreePath, "-c", "core.hooksPath=/dev/null", ...args], { cwd: worktreePath });
    if (r.code !== 0) {
      throw new Error("git " + args[0] + " failed: " + textTail(r.stderr || r.stdout, 400));
    }
    return r;
  };
  const head = await secureCommitForDelivery({ git, worktreePath, baseRef, title, extraSecrets: [token] });
  // Push token via an env-backed credential helper (never in argv), token-free
  // remote URL — same hardening as deliverPullRequest. Each task owns its
  // canonical branch, so --force is safe (only this task pushes loom/stack/.../<task>).
  const pushRes = await execBackend(
    "git",
    [
      "-C", worktreePath,
      "-c", "credential.helper=",
      "-c", 'credential.helper=!f() { echo username=x-access-token; echo "password=$LOOM_PR_GIT_PASSWORD"; }; f',
      "push", "--no-verify", "--force",
      "https://github.com/" + owner + "/" + repo + ".git",
      branchPushRefspec(head, branch),
    ],
    { cwd: worktreePath, env: { ...process.env, LOOM_PR_GIT_PASSWORD: token, GIT_TERMINAL_PROMPT: "0" } },
  );
  if (pushRes.code !== 0) {
    throw new Error("git push failed: " + scrubToken(textTail(pushRes.stderr || pushRes.stdout, 400), token));
  }
  return { branch, head };
}

// deliverLocalBranch is the local-mode counterpart to PR delivery: the task's
// isolated worktree becomes loom/<task>, and the branch is pushed to the repo's
// actual `origin` remote. There is deliberately no owner/repo parsing or GitHub
// URL construction here; activation is gated before this helper on a filesystem
// origin, so `git push origin ...` is the correct primitive.
async function deliverLocalBranch({ worktreePath, baseRef, branch, title }) {
  const git = async (...args) => {
    const r = await execBackend("git", ["-C", worktreePath, "-c", "core.hooksPath=/dev/null", ...args], { cwd: worktreePath });
    if (r.code !== 0) {
      throw new Error("git " + args[0] + " failed: " + textTail(r.stderr || r.stdout, 400));
    }
    return r;
  };
  await git("checkout", "-B", branch);
  const head = await secureCommitForDelivery({ git, worktreePath, baseRef, title });
  // Each task owns its canonical loom/<task> branch. Rework runs start from the
  // host base again, so their isolated commits can diverge from the previous
  // pushed branch; a non-force push would reject and loop failed rework forever.
  const pushRes = await execBackend(
    "git",
    ["-C", worktreePath, "push", "--no-verify", "--force", "origin", branchPushRefspec(head, branch)],
    { cwd: worktreePath, env: { ...process.env, GIT_TERMINAL_PROMPT: "0" } },
  );
  if (pushRes.code !== 0) {
    throw new Error("git push failed: " + textTail(pushRes.stderr || pushRes.stdout, 400));
  }
  return { branch, head };
}

function localBranchName(taskId, taskRunId) {
  const raw = stringValue(taskId || taskRunId || "task");
  const safe = raw.replace(/[^A-Za-z0-9_.-]/g, "-").replace(/^-+|-+$/g, "") || "task";
  return "loom/" + safe;
}

async function resolveOriginRemoteUrl(worktree) {
  try {
    const r = await execBackend("git", ["-C", worktree, "remote", "get-url", "origin"], { cwd: worktree });
    return r.code === 0 ? stringValue(r.stdout).trim() : "";
  } catch {
    return "";
  }
}

function isFilesystemOrigin(url) {
  const text = stringValue(url).trim();
  return text.startsWith("/") || text.startsWith("file://");
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
  const gitBacked = head !== "" || stringValue(base) !== "";
  let intentFailed = false;
  try {
    const intent = await execBackend("git", ["-C", worktree, "add", "-N", "--", "."], { cwd: worktree });
    intentFailed = intent.code !== 0;
  } catch {
    intentFailed = true;
  }
  const range = base ? [base] : [];
  let patch = "";
  let patchFailed = false;
  try {
    const diff = await execBackend("git", ["-C", worktree, "diff", "--binary", ...range, "--", "."], { cwd: worktree });
    if (diff.code === 0) {
      patch = diff.stdout;
    } else {
      patchFailed = true;
    }
  } catch {
    patchFailed = true;
  }
  let stat = "";
  let statFailed = false;
  try {
    const diffStat = await execBackend("git", ["-C", worktree, "diff", "--numstat", ...range, "--", "."], { cwd: worktree });
    if (diffStat.code === 0) {
      stat = diffStat.stdout;
    } else {
      statFailed = true;
    }
  } catch {
    statFailed = true;
  }
  const counts = parseNumstat(stat);
  return {
    head,
    patch,
    ...counts,
    gitBacked,
    captureFailed: gitBacked && (intentFailed || patchFailed || statFailed),
  };
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

// parseStreamJSONTranscript turns a backend's stream-json stdout into canonical
// Loom transcript entries. codex, claude, cursor, and opencode emit JSON-per-line
// event streams. It parses every line into an event array, then a per-backend
// transform builds faithful entries: assistant/user text, reasoning, tool CALLS
// AND tool RESULTS, plus a terminal `result` entry carrying status + token usage.
// Entry fields match sessions/transcript.Event exactly (note: tool output is the
// `output` field, not `tool_output`); lines that do not parse as JSON are ignored
// (the raw stdout is still preserved in logs).
export function parseStreamJSONTranscript(backend, stdout) {
  const events = [];
  for (const line of String(stdout || "").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) {
      continue;
    }
    const start = trimmed.indexOf("{");
    if (start < 0) {
      continue;
    }
    try {
      events.push(JSON.parse(trimmed.slice(start)));
    } catch {
      // non-JSON line; preserved in logs, ignored for the transcript
    }
  }
  const build =
    backend === "claude" ? claudeTranscript
      : backend === "cursor" ? cursorTranscript
        : backend === "opencode" ? opencodeTranscript
          : backend === "gemini" ? geminiTranscript
            : codexTranscript;
  const fallbackTs = new Date().toISOString();
  // Backends stamp only some events (e.g. claude stamps user/tool_result events
  // but not assistant ones). Resolve each entry's own timestamp, then forward-fill
  // gaps with the last real timestamp (leading with the first real one) so the
  // sequence stays monotonic — mixing real and parse-time stamps would scramble
  // any timestamp-ordered view of the transcript.
  const resolved = build(events).map((entry) => ({ entry, ts: toISO(entry.timestamp) }));
  const firstReal = (resolved.find((item) => item.ts) || {}).ts || fallbackTs;
  const entries = [{ seq: 1, timestamp: firstReal, ...sessionMetaEntry(backend) }];
  let seq = 2;
  let cursorTs = firstReal;
  for (const { entry, ts } of resolved) {
    // Advance only forward (ISO strings compare chronologically): gaps and any
    // rare out-of-order stamp inherit the last value, keeping it non-decreasing.
    if (ts && ts > cursorTs) {
      cursorTs = ts;
    }
    const { timestamp, ...rest } = entry;
    entries.push({ seq: seq++, timestamp: cursorTs, ...rest });
  }
  return entries;
}

// taskUsageFromEntries recovers the token usage the terminal `result` entry
// carries (resultEntry serializes the parsed usage object into its `output`
// field) and maps it onto the snake_case top-level token/cost fields the Go
// host-bridge reads from the runner result (internal/driver/task_bridge.go) and
// persists to the fleet-db TaskRun. The daytona runner surfaces the same shape
// via @loom/sdk/runtime-adapters' flueUsageToTaskUsage; the local runner is kept
// loadable without the SDK (see loadTask), so it maps here instead. Returns {}
// when no usage was reported (minimal/gemini fallback, or an early failure).
export function taskUsageFromEntries(entries) {
  if (!Array.isArray(entries)) {
    return {};
  }
  let usage = null;
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    if (entry && entry.type === "result" && entry.output) {
      try {
        usage = JSON.parse(entry.output);
      } catch {
        usage = null;
      }
      break;
    }
  }
  if (!usage || typeof usage !== "object") {
    return {};
  }
  const out = {};
  const set = (key, value) => {
    const num = Number(value);
    if (Number.isFinite(num)) {
      out[key] = num;
    }
  };
  set("input_tokens", usage.input_tokens);
  set("output_tokens", usage.output_tokens);
  set("cache_read_tokens", usage.cache_read_tokens);
  set("cache_write_tokens", usage.cache_write_tokens);
  set("estimated_cost_usd", usage.cost_usd != null ? usage.cost_usd : usage.estimated_cost_usd);
  return out;
}

// NOTE: per-backend price-table estimation (DEFAULT_PRICING / resolvePricing /
// estimateCostUSD) was removed 2026-06-23. Cost is now sourced ONLY from the
// backend CLI's own reporting (see taskUsageFromEntries + the run() cost note):
// estimating tokens x a per-backend rate was inaccurate (a single backend runs
// many models at different prices — e.g. claude ran Opus, not the Sonnet rate the
// table assumed) and codex/cursor/gemini expose no cost to anchor it.

function streamFailureMessage(backend, stdout) {
  if (backend !== "opencode") {
    return "";
  }
  for (const line of String(stdout || "").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) {
      continue;
    }
    const start = trimmed.indexOf("{");
    if (start < 0) {
      continue;
    }
    let event;
    try {
      event = JSON.parse(trimmed.slice(start));
    } catch {
      continue;
    }
    if (!event || typeof event !== "object" || event.type !== "error") {
      continue;
    }
    const msg = rawString(event.error && event.error.message)
      || rawString(event.error && event.error.data && event.error.data.message)
      || rawString(event.message);
    return msg || "opencode reported an error";
  }
  return "";
}

// rawString preserves the exact value (incl. newlines); use for text/output
// content where fidelity matters. stringValue (trimmed) is for names/ids.
function rawString(value) {
  return value === undefined || value === null ? "" : String(value);
}

// toISO normalizes a per-event timestamp: epoch-ms number -> ISO, ISO string
// passthrough, else "" (caller falls back to parse time).
function toISO(ts) {
  if (typeof ts === "number" && Number.isFinite(ts)) {
    return new Date(ts).toISOString();
  }
  if (typeof ts === "string" && ts.trim()) {
    // Normalize to strict RFC3339 — Go unmarshals transcript.Event.Timestamp as
    // time.Time, so a single non-RFC3339 string would fail the whole result
    // decode and turn a successful run into a task failure. Reject unparseable.
    const parsed = new Date(ts);
    return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
  }
  return "";
}

// normalizeUsage keeps only finite numeric token/cost fields; null if empty.
function normalizeUsage(fields) {
  const out = {};
  for (const [key, value] of Object.entries(fields)) {
    if (value == null) {
      continue;
    }
    const num = Number(value);
    if (Number.isFinite(num)) {
      out[key] = num;
    }
  }
  return Object.keys(out).length ? out : null;
}

// accumulateUsage sums per-call usage objects, for backends that report usage per
// step (e.g. opencode step_finish) so the terminal entry is the session total.
function accumulateUsage(prev, summed, latest) {
  const out = { ...(prev || {}) };
  for (const [key, value] of Object.entries(summed)) {
    if (value == null) {
      continue;
    }
    const num = Number(value);
    if (Number.isFinite(num)) {
      out[key] = (out[key] || 0) + num;
    }
  }
  // `latest` fields (e.g. cache_read, the same cached prompt re-read each step) are
  // taken from the most recent step rather than summed, to avoid double-counting.
  for (const [key, value] of Object.entries(latest || {})) {
    if (value == null) {
      continue;
    }
    const num = Number(value);
    if (Number.isFinite(num)) {
      out[key] = num;
    }
  }
  return Object.keys(out).length ? out : null;
}

// sessionMetaEntry builds the canonical session_meta head — the one definition of
// the #5d transcript-vocabulary contract shared by every producer path here.
function sessionMetaEntry(backend, label) {
  return {
    role: "system",
    type: "session_meta",
    text: `local-cli-${backend} session` + (label ? ` for ${label}` : ""),
  };
}

// ensureSessionMetaLead prepends a session_meta entry unless the transcript already
// leads with one. Its timestamp mirrors the first real entry so it sorts to the head.
function ensureSessionMetaLead(entries, backend, label) {
  if (entries[0]?.type === "session_meta") return entries;
  const meta = sessionMetaEntry(backend, label);
  const firstTs = entries.find((e) => e && e.timestamp)?.timestamp;
  if (firstTs) meta.timestamp = firstTs;
  return [meta, ...entries];
}

// resultEntry is the terminal transcript entry: completion status + token usage.
// transcript.Event has no structured usage field, so the readable summary goes in
// `text` and the raw object in `output`.
function resultEntry(status, usage, timestamp) {
  const bits = [];
  if (status) {
    bits.push(status);
  }
  if (usage) {
    const parts = [];
    const labels = {
      input_tokens: "in", output_tokens: "out", cache_read_tokens: "cache_read", cache_write_tokens: "cache_write",
      reasoning_tokens: "reasoning", cost_usd: "cost", duration_ms: "duration_ms", num_turns: "turns",
    };
    for (const [key, label] of Object.entries(labels)) {
      if (usage[key] != null) {
        parts.push(`${label}=${usage[key]}`);
      }
    }
    if (parts.length) {
      bits.push(parts.join(" "));
    }
  }
  const entry = { role: "system", type: "result", text: bits.join(" | ") || "completed" };
  if (usage) {
    entry.output = JSON.stringify(usage);
  }
  if (timestamp) {
    entry.timestamp = timestamp;
  }
  return entry;
}

// toolResultText flattens a tool_result content payload (string or content[]).
function toolResultText(content) {
  if (typeof content === "string") {
    return content;
  }
  if (Array.isArray(content)) {
    return content
      .map((block) => (typeof block === "string" ? block : rawString(block && block.text)))
      .filter(Boolean)
      .join("\n");
  }
  return rawString(content);
}

// claudeTranscript: assistant text/thinking/tool_use, user tool_result (the tool
// OUTPUTS, which claude emits as separate type:"user" events), and the terminal
// result event (status + usage + cost).
function claudeTranscript(events) {
  const out = [];
  let usage = null;
  let status = null;
  let lastTs;
  for (const event of events) {
    if (!event || typeof event !== "object") {
      continue;
    }
    const ts = event.timestamp; // claude stamps user/tool_result events
    if (ts) {
      lastTs = ts;
    }
    if (event.type === "assistant" && event.message && Array.isArray(event.message.content)) {
      for (const block of event.message.content) {
        if (!block || typeof block !== "object") {
          continue;
        }
        if (block.type === "text" && stringValue(block.text)) {
          out.push({ role: "assistant", type: "text", text: rawString(block.text), timestamp: ts });
        } else if (block.type === "thinking" && stringValue(block.thinking)) {
          out.push({ role: "assistant", type: "reasoning", text: rawString(block.thinking), timestamp: ts });
        } else if (block.type === "tool_use") {
          out.push({ role: "assistant", type: "tool_use", tool_name: stringValue(block.name), tool_use_id: stringValue(block.id), tool_input: block.input, timestamp: ts });
        }
      }
    } else if (event.type === "user" && event.message && Array.isArray(event.message.content)) {
      for (const block of event.message.content) {
        if (block && block.type === "tool_result") {
          out.push({ role: "tool", type: "tool_result", tool_use_id: stringValue(block.tool_use_id), output: toolResultText(block.content), timestamp: ts });
        }
      }
    } else if (event.type === "result") {
      usage = normalizeUsage({
        input_tokens: event.usage && event.usage.input_tokens,
        output_tokens: event.usage && event.usage.output_tokens,
        cache_read_tokens: event.usage && event.usage.cache_read_input_tokens,
        cache_write_tokens: event.usage && event.usage.cache_creation_input_tokens,
        cost_usd: event.total_cost_usd,
        duration_ms: event.duration_ms,
        num_turns: event.num_turns,
      });
      status = event.is_error ? "failed" : "completed";
    }
  }
  if (status || usage) {
    out.push(resultEntry(status, usage, lastTs));
  }
  return out;
}

// codexTranscript: codex `exec --json` nests output under item.completed
// (agent_message / reasoning / command_execution / file_change). Only the
// `item.completed` event is emitted (item.started is its in-progress duplicate),
// file_change records the edit, command_execution preserves output + exit status,
// and turn.completed yields the usage entry.
function codexTranscript(events) {
  const out = [];
  let usage = null;
  let status = null;
  for (const event of events) {
    if (!event || typeof event !== "object") {
      continue;
    }
    if (event.type === "turn.completed") {
      usage = normalizeUsage({
        input_tokens: event.usage && event.usage.input_tokens,
        output_tokens: event.usage && event.usage.output_tokens,
        cache_read_tokens: event.usage && event.usage.cached_input_tokens,
        reasoning_tokens: event.usage && event.usage.reasoning_output_tokens,
      });
      status = status || "completed";
      continue;
    }
    if (event.type === "turn.failed" || event.type === "error") {
      status = "failed";
      continue;
    }
    if (event.type === "item.started") {
      continue; // dedup: the in-progress half of an item.completed
    }
    if (event.type === "item.completed" && event.item && typeof event.item === "object") {
      const item = event.item;
      const itemType = stringValue(item.type);
      if (itemType === "agent_message" || itemType.includes("message")) {
        const text = rawString(item.text);
        if (text) {
          out.push({ role: "assistant", type: "text", text });
        }
      } else if (itemType === "reasoning") {
        const text = rawString(item.text);
        if (text) {
          out.push({ role: "assistant", type: "reasoning", text });
        }
      } else if (itemType === "command_execution") {
        const entry = { role: "assistant", type: "tool_use", tool_name: "shell", tool_input: { command: stringValue(item.command) } };
        const failed = item.exit_code != null && String(item.exit_code) !== "0";
        const output = (failed ? `[exit ${item.exit_code}]\n` : "") + rawString(item.aggregated_output);
        if (output) {
          entry.output = output;
        }
        out.push(entry);
      } else if (itemType === "file_change") {
        const changes = Array.isArray(item.changes) ? item.changes : [];
        out.push({
          role: "assistant",
          type: "tool_use",
          tool_name: "apply_patch",
          tool_input: { changes: changes.map((c) => ({ path: stringValue(c && c.path), kind: stringValue(c && c.kind) })) },
          output: changes.map((c) => `${stringValue(c && c.kind)} ${stringValue(c && c.path)}`).join("\n"),
        });
      }
      continue;
    }
    // Flat fallback (e.g. {type:"agent_message", text}).
    const type = stringValue(event.type);
    const text = rawString(event.text) || rawString(event.message) || (event.msg && rawString(event.msg.text)) || "";
    if (text && (type.includes("message") || type.includes("agent") || type.includes("assistant") || type.includes("output"))) {
      out.push({ role: "assistant", type: "text", text });
    }
  }
  if (status || usage) {
    out.push(resultEntry(status, usage));
  }
  return out;
}

// cursorTranscript maps cursor-agent `--output-format stream-json` events.
// assistant/user messages carry a claude-shaped content[] array. Tool calls split
// across a `started` event (which holds the args) and a `completed` event (which
// holds the result); they are merged by call_id so the entry keeps BOTH input and
// output and stays in invocation order. The terminal result event yields usage.
function cursorTranscript(events) {
  const out = [];
  const byCall = new Map();
  let usage = null;
  let status = null;
  let lastTs;
  for (const event of events) {
    if (!event || typeof event !== "object") {
      continue;
    }
    const ts = event.timestamp_ms;
    if (ts != null) {
      lastTs = ts;
    }
    if ((event.type === "assistant" || event.type === "user") && event.message && Array.isArray(event.message.content)) {
      const role = event.type === "user" ? "user" : "assistant";
      for (const block of event.message.content) {
        if (!block || typeof block !== "object") {
          continue;
        }
        if (block.type === "text" && stringValue(block.text)) {
          out.push({ role, type: "text", text: rawString(block.text), timestamp: ts });
        } else if (block.type === "tool_use") {
          out.push({ role: "assistant", type: "tool_use", tool_name: stringValue(block.name), tool_use_id: stringValue(block.id), tool_input: block.input, timestamp: ts });
        }
      }
    } else if (event.type === "tool_call" && event.tool_call && typeof event.tool_call === "object") {
      const tc = event.tool_call;
      const callId = stringValue(event.call_id) || stringValue(tc.toolCallId);
      let name;
      let detail;
      for (const key of Object.keys(tc)) {
        const match = /^(.+)ToolCall$/.exec(key);
        if (match && tc[key] && typeof tc[key] === "object") {
          name = match[1];
          detail = tc[key];
          break;
        }
      }
      if (!name) {
        continue;
      }
      let entry = callId ? byCall.get(callId) : undefined;
      if (!entry) {
        // first sighting (started, or completed if no started) -> invocation order
        entry = { role: "assistant", type: "tool_use", tool_name: name, tool_use_id: callId, timestamp: ts };
        if (detail.args !== undefined) {
          entry.tool_input = detail.args;
        }
        out.push(entry);
        if (callId) {
          byCall.set(callId, entry);
        }
      } else if (entry.tool_input === undefined && detail.args !== undefined) {
        entry.tool_input = detail.args; // args live only on the started event
      }
      if (event.subtype === "completed" && detail.result !== undefined) {
        const isError = detail.result && typeof detail.result === "object" && detail.result.error !== undefined;
        const body = typeof detail.result === "string" ? detail.result : JSON.stringify(detail.result);
        entry.output = (isError ? "[error] " : "") + body;
      }
    } else if (event.type === "result") {
      // cursor-agent emits camelCase usage; accept snake_case too for resilience.
      const u = event.usage || {};
      usage = normalizeUsage({
        input_tokens: u.inputTokens ?? u.input_tokens,
        output_tokens: u.outputTokens ?? u.output_tokens,
        cache_read_tokens: u.cacheReadTokens ?? u.cache_read_tokens,
        cache_write_tokens: u.cacheWriteTokens ?? u.cache_write_tokens,
        duration_ms: event.duration_ms,
      });
      status = event.is_error ? "failed" : "completed";
    }
  }
  if (status || usage) {
    out.push(resultEntry(status, usage, lastTs));
  }
  return out;
}

// opencodeTranscript maps opencode `run --format json` events (JSONL):
// { type:"text", part:{text} } for assistant text and
// { type:"tool_use", part:{tool, callID, state:{status, input, output}} } for
// tools (output preserved). step_finish events carry token usage; the final one
// (reason:"stop") marks completion.
function opencodeTranscript(events) {
  const out = [];
  let usage = null;
  let status = null;
  let lastTs;
  for (const event of events) {
    if (!event || typeof event !== "object") {
      continue;
    }
    const part = event.part && typeof event.part === "object" ? event.part : event;
    const ts = (part.time && part.time.start) || event.timestamp;
    if (ts != null) {
      lastTs = ts;
    }
    if (event.type === "text") {
      const text = rawString(part.text);
      if (text) {
        out.push({ role: "assistant", type: "text", text, timestamp: ts });
      }
    } else if (event.type === "tool_use" && part.type === "tool") {
      const state = part.state && typeof part.state === "object" ? part.state : {};
      // emit terminal tool states (completed AND error); skip in-progress only,
      // so a failed tool call is never silently dropped.
      if (state.status && state.status !== "completed" && state.status !== "error") {
        continue;
      }
      const isError = state.status === "error";
      const entry = { role: "assistant", type: "tool_use", tool_name: stringValue(part.tool), tool_use_id: stringValue(part.callID), tool_input: state.input, timestamp: ts };
      const output = rawString(state.output) || rawString(state.error);
      if (output || isError) {
        entry.output = (isError ? "[error] " : "") + output;
      }
      out.push(entry);
    } else if (event.type === "step_finish") {
      const tokens = part.tokens && typeof part.tokens === "object" ? part.tokens : {};
      // opencode reports usage PER step; sum the additive fields for the session
      // total. cache_read is the same cached prompt re-read each step -> take latest.
      usage = accumulateUsage(usage, {
        input_tokens: tokens.input,
        output_tokens: tokens.output,
        reasoning_tokens: tokens.reasoning,
        cost_usd: part.cost,
      }, {
        cache_read_tokens: tokens.cache && tokens.cache.read,
      });
      if (part.reason === "stop" && !status) {
        status = "completed";
      }
    } else if (event.type === "error") {
      // opencode can exit 0 while signalling a fatal stream error; surface it so
      // the failure is visible in the transcript (parity with the Go backend's
      // extractOpenCodeStreamError). An error supersedes a later stop.
      const msg = rawString(event.error && (event.error.message || event.error)) || rawString(event.message);
      status = msg ? "failed: " + msg : "failed";
    }
  }
  if (status || usage) {
    out.push(resultEntry(status, usage, lastTs));
  }
  return out;
}

// geminiTranscript maps gemini `-o stream-json` events. Assistant text arrives as
// Google-native candidates[].content.parts[].text (GenerateContent streaming) or
// as a flat text/content/response field. Token usage rides on `usage`
// (OpenAI-compatible {input_tokens,output_tokens}) or `usageMetadata`
// ({promptTokenCount,candidatesTokenCount}) — mirrors backend_gemini.go's
// collectGeminiStreamUsage. usageMetadata is cumulative across chunks, so the last
// value wins (summing would double-count). Before this, gemini got no usage at all.
function geminiTranscript(events) {
  const out = [];
  let usage = null;
  for (const event of events) {
    if (!event || typeof event !== "object") {
      continue;
    }
    const candidates = Array.isArray(event.candidates) ? event.candidates : [];
    for (const candidate of candidates) {
      const parts = candidate && candidate.content && Array.isArray(candidate.content.parts) ? candidate.content.parts : [];
      for (const part of parts) {
        const text = rawString(part && part.text);
        if (text) {
          out.push({ role: "assistant", type: "text", text });
        }
      }
    }
    if (!candidates.length) {
      const flat = rawString(event.text) || rawString(event.content) || rawString(event.response) || (event.message && rawString(event.message.text)) || "";
      const type = stringValue(event.type);
      if (flat && (!type || type.includes("content") || type.includes("text") || type.includes("assistant") || type.includes("message") || type.includes("response"))) {
        out.push({ role: "assistant", type: "text", text: flat });
      }
    }
    if (event.usage && typeof event.usage === "object") {
      usage = normalizeUsage({
        input_tokens: event.usage.input_tokens != null ? event.usage.input_tokens : event.usage.inputTokens,
        output_tokens: event.usage.output_tokens != null ? event.usage.output_tokens : event.usage.outputTokens,
      });
    } else if (event.usageMetadata && typeof event.usageMetadata === "object") {
      usage = normalizeUsage({
        input_tokens: event.usageMetadata.promptTokenCount,
        output_tokens: event.usageMetadata.candidatesTokenCount,
      });
    }
  }
  if (usage) {
    out.push(resultEntry(null, usage));
  }
  return out;
}

// minimalTranscript is the fallback when a backend has no structured event stream,
// or when a stream-json backend emits output the parser does not recognize: a
// session_meta marker, the user prompt, and a final assistant entry carrying the
// CLI stdout tail. Real evidence, just unstructured.
function minimalTranscript(backend, label, prompt, stdout) {
  const now = new Date().toISOString();
  return [
    { seq: 1, timestamp: now, ...sessionMetaEntry(backend, label) },
    { seq: 2, timestamp: now, role: "user", type: "text", text: textTail(prompt, 4000) },
    { seq: 3, timestamp: now, role: "assistant", type: "text", text: textTail(stdout, 4000) },
  ];
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
    errorMessage: redactRunnerText(textTail(message)),
    logs: renderLogs(logs.concat([errorClass + ": " + message])),
    logsRef: "logs://" + info.taskRunId,
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
