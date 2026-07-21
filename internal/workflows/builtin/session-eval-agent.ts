import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// Flue HEAD requires every workflow module to default-export a
// defineWorkflow() definition; a bare `export function run` no longer
// normalizes (same preamble as epic-runner.ts). This orchestrator is not an
// LLM agent — it drives the loom driver SDK — so the bound agent is a
// credential-free stub and the invocation payload arrives via the launcher
// env, not flue's input channel.
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: builtinInvokePayload() })),
});

function builtinInvokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD || process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}';
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

// Flue HEAD rejects undefined/function/symbol/bigint in workflow results;
// round-trip through JSON so optional undefined fields are dropped instead.
function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

// session-eval-agent: cron-triggered one-pass session evaluation.
//
// The workflow is fired by the cron.session-eval-agent trigger binding. It
// runs a single deterministic linear pass:
//
//   1. read backend/model config once in the orchestrator;
//   2. preflight the configured backend through a deterministic TaskRun;
//   3. ask the driver API for the current prompt-version candidate batch;
//   4. for each session, fetch the canonical transcript, render the complete
//      judge input without condensation, run one deterministic judge TaskRun,
//      validate its JSON, then put either a done or failed eval metric;
//   5. complete with batch counters.
//
// Re-entry is deterministic: the preflight and judge taskRunIds are derived
// from the driver run id plus stable labels, and the candidate loop is
// sequential. Per-session failures are isolated so one bad transcript or judge
// result does not abort the batch; backend preflight/config failures happen
// before candidate selection and stamp nothing.

// Judge identity constants. Changing DEFAULT_JUDGE_MODEL changes judge
// identity and must be paired with a PROMPT_VERSION bump.
const PROMPT_VERSION = "v1";
const DEFAULT_JUDGE_MODEL = "gpt-5.6-sol";
const SESSION_EVAL_TASK_RUNNER = "session-eval-task-runner";
const SCORE_KEYS = ["outcome_success", "instruction_adherence", "efficiency", "tool_use_quality"];
const IMPROVEMENT_KEYS = ["harness", "linter", "prompt", "skill"];

export async function run(ctx) {
  const loom = createLoomDriverClient({ input: ctx.payload || {} });
  const backend = stringValue(process.env.LOOM_EVAL_BACKEND || "codex") || "codex";
  const model = stringValue(process.env.LOOM_EVAL_MODEL || DEFAULT_JUDGE_MODEL) || DEFAULT_JUDGE_MODEL;

  if (backend !== "codex") {
    return loom.failed({
      summary: "session eval backend " + backend + " is not supported",
      errorClass: "eval_backend_unsupported",
    });
  }

  const preflight = await runPreflightTask(loom, backend);
  if (!preflight.ok) {
    return loom.failed({
      summary: preflight.summary,
      errorClass: "eval_backend_unavailable",
    });
  }

  let batch;
  try {
    batch = await loom.evals.listUnevaluated({ promptVersion: PROMPT_VERSION });
  } catch (err) {
    return loom.failed({
      summary: "list eval candidates failed: " + errorMessage(err),
      errorClass: connectorErrorClass(err, "eval_candidate_list_failed"),
    });
  }

  const sessions = Array.isArray(batch && batch.sessions) ? batch.sessions : [];
  if (sessions.length === 0) {
    return loom.completed({ summary: "no eval candidates", evaluated: 0 });
  }

  let evaluated = 0;
  let failed = 0;
  let skipped = 0;

  for (const candidate of sessions) {
    const sessionId = candidateSessionId(candidate);
    if (!sessionId) {
      skipped++;
      continue;
    }

    let transcript;
    try {
      transcript = await loom.evals.getTranscript({ sessionId, promptVersion: PROMPT_VERSION });
    } catch (err) {
      if (isTranscriptFetchFailedError(err)) {
        // Already stamped eval_status=failed server-side by the op.
        failed++;
        continue;
      }
      // Transient driver-op errors are deliberately not stamped: the next
      // cron tick should be able to retry the same still-eligible session.
      skipped++;
      continue;
    }

    const entries = Array.isArray(transcript && transcript.entries) ? transcript.entries : [];
    const judgeInput = renderJudgeInput(candidate, entries);
    const judge = await runJudgeTask(loom, backend, model, sessionId, judgeInput);
    if (!judge.ok) {
      if (judge.errorClass === "eval_backend_unsupported") {
        return loom.failed({
          summary: "judge task rejected backend " + backend,
          errorClass: "eval_backend_unsupported",
        });
      }
      const errorClass = judge.errorClass === "transcript_too_large" ? "transcript_too_large" : "judge_error";
      await putFailedMetric(loom, sessionId, errorClass, judge.judgeSessionId);
      failed++;
      continue;
    }

    const parsed = parseEvalResult(judge.evalResult);
    const valid = validateEvalResult(parsed);
    if (!valid.ok) {
      await putFailedMetric(loom, sessionId, "judge_error", judge.judgeSessionId);
      failed++;
      continue;
    }

    const cost = normalizeEvalCost(judge.evalCost);
    const put = await putDoneMetric(loom, sessionId, parsed, judge.judgeModel || model, cost, judge.judgeSessionId);
    if (put === "done") {
      evaluated++;
    } else if (put === "rejected") {
      // The server permanently rejected the payload (validation): stamp
      // failed so the session is not re-judged every tick for this version.
      await putFailedMetric(loom, sessionId, "judge_error", judge.judgeSessionId);
      failed++;
    } else {
      // Transient put failure: leave unstamped for the next tick to retry.
      skipped++;
    }
  }

  return loom.completed({
    summary: "session eval batch complete: evaluated=" + evaluated + " failed=" + failed + " skipped=" + skipped,
    evaluated,
    failed,
    skipped,
  });
}

async function runPreflightTask(loom, backend) {
  const taskRunId = deterministicTaskRunId(loom.driverRunId, "preflight");
  try {
    await loom.taskRuns.request({
      taskId: "session-eval-preflight",
      taskRunId,
      runner: SESSION_EVAL_TASK_RUNNER,
      input: { kind: "session_eval_preflight", backend },
    });
  } catch (err) {
    if (!isConflictError(err)) {
      return { ok: false, summary: "eval backend preflight request failed: " + errorMessage(err) };
    }
  }
  let run;
  try {
    run = await loom.taskRuns.await({ taskRunId });
  } catch (err) {
    return { ok: false, summary: "eval backend preflight await failed: " + errorMessage(err) };
  }
  const metadata = runtimeMetadata(run);
  const status = stringValue(run && run.status);
  if (status !== "completed") {
    return { ok: false, summary: "eval backend preflight ended " + (status || "without a terminal status") };
  }
  if (!truthyMetadata(metadata.codex_available ?? metadata.codexAvailable)) {
    return { ok: false, summary: "codex is unavailable for session eval judging" };
  }
  return { ok: true };
}

async function runJudgeTask(loom, backend, model, sessionId, judgeInput) {
  const taskRunId = deterministicTaskRunId(loom.driverRunId, "judge-" + sessionId);
  try {
    await loom.taskRuns.request({
      taskId: "session-eval-" + slug(sessionId),
      taskRunId,
      runner: SESSION_EVAL_TASK_RUNNER,
      input: {
        kind: "session_eval_judge",
        backend,
        model,
        promptVersion: PROMPT_VERSION,
        sessionId,
        judgeInput,
      },
    });
  } catch (err) {
    if (!isConflictError(err)) {
      return { ok: false, errorClass: connectorErrorClass(err, "judge_error"), summary: "judge task request failed: " + errorMessage(err) };
    }
  }

  let run;
  try {
    run = await loom.taskRuns.await({ taskRunId });
  } catch (err) {
    return { ok: false, errorClass: "judge_error", summary: "judge task await failed: " + errorMessage(err) };
  }
  const status = stringValue(run && run.status);
  const metadata = runtimeMetadata(run);
  const judgeSessionId = stringValue(metadata.judge_session_id ?? metadata.judgeSessionId);
  if (status !== "completed") {
    return {
      ok: false,
      errorClass: stringValue(run && (run.errorClass || run.error_class)) || "judge_error",
      summary: "judge task " + taskRunId + " ended " + (status || "without a terminal status"),
      judgeSessionId,
    };
  }
  return {
    ok: true,
    evalResult: metadata.eval_result ?? metadata.evalResult ?? "",
    judgeModel: stringValue(metadata.judge_model ?? metadata.judgeModel),
    judgeSessionId,
    evalCost: metadata.eval_cost ?? metadata.evalCost,
  };
}

// Returns "done" on success, "rejected" when the server permanently refused
// the payload (validation error, code "invalid"), "transient" otherwise.
async function putDoneMetric(loom, sessionId, result, judgeModel, evalCost, judgeSessionId) {
  try {
    await loom.evals.putMetric({
      sessionId,
      promptVersion: PROMPT_VERSION,
      judgeSessionId,
      status: "done",
      eval: {
        scores: result.scores,
        score_rationales: result.score_rationales,
        error_taxonomy_tags: result.error_taxonomy_tags,
        improvement_categories: result.improvement_categories,
        judge_summary: result.judge_summary,
        judge_model: judgeModel,
        eval_cost: evalCost,
      },
    });
    return "done";
  } catch (err) {
    return stringValue(err && err.code) === "invalid" ? "rejected" : "transient";
  }
}

async function putFailedMetric(loom, sessionId, errorClass, judgeSessionId = "") {
  try {
    await loom.evals.putMetric({
      sessionId,
      promptVersion: PROMPT_VERSION,
      judgeSessionId,
      status: "failed",
      errorClass,
    });
    return true;
  } catch {
    return false;
  }
}

function renderJudgeInput(candidate, entries) {
  return [
    renderHeader(candidate),
    "=== TRANSCRIPT (" + entries.length + " canonical entries, verbatim, no truncation) ===",
    entries.map(renderEntry).join("\n\n"),
    "=== DIFF ===\n(diff stats are in the session record header; full patch content is not included in v1)",
  ].join("\n\n");
}

function renderHeader(candidate) {
  const tokenUsage = valueAt(candidate, "token_usage", "tokenUsage") || {};
  const diffStats = valueAt(candidate, "diff_stats", "diffStats") || {};
  const filesTouched = arrayValue(valueAt(diffStats, "files_touched", "filesTouched"));
  const diffPath = stringValue(valueAt(diffStats, "diff_path", "diffPath"));
  return [
    "=== SESSION RECORD (harness ground truth) ===",
    "session_id:        " + pick(candidateSessionId(candidate), "(unknown)"),
    "agent:             " + pick(valueAt(candidate, "agent_id", "agentId"), "(unknown)") + " (kind: " + pick(valueAt(candidate, "kind"), "task") + ")",
    "task:              " + pick(valueAt(candidate, "task_id", "taskId"), "(none)"),
    "final_status:      " + pick(valueAt(candidate, "status"), "(unknown)"),
    "exit_code:         " + pick(valueAt(candidate, "exit_code", "exitCode"), "(unknown)"),
    "error_class:       " + pick(valueAt(candidate, "error_class", "errorClass"), "(none recorded)"),
    "started_at:        " + pick(valueAt(candidate, "started_at", "startedAt"), "(unknown)"),
    "ended_at:          " + pick(valueAt(candidate, "ended_at", "endedAt"), "(unknown)"),
    "duration_s:        " + pick(valueAt(candidate, "duration_s", "durationS"), "(unknown)"),
    "parent_session_id: " + pick(valueAt(candidate, "parent_session_id", "parentSessionId"), "(none)"),
    "attempt:           " + pick(valueAt(candidate, "attempt"), "0"),
    "tokens:            in=" + pick(valueAt(tokenUsage, "input_tokens", "inputTokens"), "?") + " out=" + pick(valueAt(tokenUsage, "output_tokens", "outputTokens"), "?"),
    "diff_stat:         files_changed=" + pick(valueAt(diffStats, "files_changed", "filesChanged"), "?") +
      " +" + pick(valueAt(diffStats, "lines_added", "linesAdded"), "?") +
      " -" + pick(valueAt(diffStats, "lines_removed", "linesRemoved"), "?") +
      " files_touched=[" + filesTouched.join(",") + "] patch_present=" + (diffPath !== ""),
  ].join("\n");
}

function renderEntry(entry) {
  const headParts = [
    "-- entry " + valueAt(entry, "seq"),
    valueAt(entry, "timestamp") || "?",
    stringValue(valueAt(entry, "role") || "?").toUpperCase() + " " + (valueAt(entry, "type") || "?"),
  ];
  let head = headParts.join(" | ");
  const toolName = stringValue(valueAt(entry, "tool_name", "toolName"));
  const toolUseID = stringValue(valueAt(entry, "tool_use_id", "toolUseId"));
  if (toolName || toolUseID) {
    head += " [" + [toolName, toolUseID].filter(Boolean).join(" ") + "]";
  }
  const parts = [head];
  const text = valueAt(entry, "text");
  if (text) {
    parts.push(String(text));
  }
  if (hasValue(entry, "tool_input") || hasValue(entry, "toolInput")) {
    parts.push(renderPossiblyJSON(valueAt(entry, "tool_input", "toolInput")));
  }
  if (hasValue(entry, "output")) {
    parts.push(renderPossiblyJSON(valueAt(entry, "output")));
  }
  return parts.join("\n");
}

function parseEvalResult(raw) {
  if (raw && typeof raw === "object") {
    return raw;
  }
  const text = stringValue(raw);
  if (!text) {
    return null;
  }
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function validateEvalResult(result) {
  if (!result || typeof result !== "object" || Array.isArray(result)) {
    return { ok: false, summary: "eval result must be an object" };
  }
  if (!validateScoreMap(result.scores)) {
    return { ok: false, summary: "scores are invalid" };
  }
  if (!validateStringMap(result.score_rationales, SCORE_KEYS)) {
    return { ok: false, summary: "score_rationales are invalid" };
  }
  if (!Array.isArray(result.error_taxonomy_tags)) {
    return { ok: false, summary: "error_taxonomy_tags must be an array" };
  }
  const cats = result.improvement_categories;
  if (!cats || typeof cats !== "object" || Array.isArray(cats)) {
    return { ok: false, summary: "improvement_categories must be an object" };
  }
  for (const key of IMPROVEMENT_KEYS) {
    if (!Array.isArray(cats[key])) {
      return { ok: false, summary: "improvement_categories." + key + " must be an array" };
    }
  }
  if (!stringValue(result.judge_summary)) {
    return { ok: false, summary: "judge_summary is required" };
  }
  return { ok: true };
}

function validateScoreMap(scores) {
  if (!scores || typeof scores !== "object" || Array.isArray(scores)) {
    return false;
  }
  const keys = Object.keys(scores).sort().join(",");
  if (keys !== SCORE_KEYS.slice().sort().join(",")) {
    return false;
  }
  for (const key of SCORE_KEYS) {
    const value = scores[key];
    if (!Number.isInteger(value) || value < 0 || value > 100) {
      return false;
    }
  }
  return true;
}

function validateStringMap(values, keys) {
  if (!values || typeof values !== "object" || Array.isArray(values)) {
    return false;
  }
  if (Object.keys(values).sort().join(",") !== keys.slice().sort().join(",")) {
    return false;
  }
  for (const key of keys) {
    if (!stringValue(values[key])) {
      return false;
    }
  }
  return true;
}

function normalizeEvalCost(raw) {
  let value = raw;
  if (typeof raw === "string") {
    try {
      value = JSON.parse(raw);
    } catch {
      return null;
    }
  }
  if (!value || typeof value !== "object") {
    return null;
  }
  const total = positiveInt(value.total_tokens ?? value.totalTokens);
  if (total == null) {
    return null;
  }
  return {
    total_tokens: total,
  };
}

function positiveInt(value) {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : null;
}

function runtimeMetadata(run) {
  return (run && (run.runtimeMetadata || run.runtime_metadata)) || {};
}

function isTranscriptFetchFailedError(err) {
  const text = (stringValue(err && err.code) + " " + errorMessage(err) + " " + stringValue(err && err.details && err.details.errorClass)).toLowerCase();
  return text.includes("transcript_fetch_failed");
}

function isConflictError(err) {
  switch (stringValue(err && err.code)) {
    case "conflict":
    case "already_exists":
    case "invalid_transition":
      return true;
    default:
      return false;
  }
}

function connectorErrorClass(err, fallback) {
  return stringValue(err && err.code) || fallback;
}

function candidateSessionId(candidate) {
  return stringValue(valueAt(candidate, "session_id", "sessionId"));
}

function deterministicTaskRunId(driverRunId, label) {
  return "task-run-" + slug(driverRunId || "run") + "-" + slug(label || "task");
}

function renderPossiblyJSON(value) {
  if (typeof value === "string") {
    return value;
  }
  return JSON.stringify(value, null, 1);
}

function arrayValue(value) {
  return Array.isArray(value) ? value.map((v) => stringValue(v)).filter(Boolean) : [];
}

function pick(value, fallback) {
  const text = stringValue(value);
  return text === "" ? fallback : text;
}

function valueAt(obj, ...keys) {
  if (!obj || typeof obj !== "object") {
    return undefined;
  }
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(obj, key)) {
      return obj[key];
    }
  }
  return undefined;
}

function hasValue(obj, key) {
  return Boolean(obj && typeof obj === "object" && Object.prototype.hasOwnProperty.call(obj, key) && obj[key] !== undefined && obj[key] !== null);
}

function truthyMetadata(value) {
  switch (stringValue(value).toLowerCase()) {
    case "1":
    case "true":
    case "yes":
      return true;
    default:
      return false;
  }
}

function slug(value) {
  return stringValue(value).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "item";
}

function stringValue(value) {
  return value === undefined || value === null ? "" : String(value).trim();
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}
