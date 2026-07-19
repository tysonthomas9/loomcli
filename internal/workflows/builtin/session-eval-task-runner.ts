import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { defineAgent, defineWorkflow } from "@flue/runtime";

// Flue HEAD requires every workflow module to default-export a defineWorkflow()
// definition; a bare `export function run` no longer normalizes (same preamble
// as local-task-runner.ts). The judge leaf shells out to codex — no LLM agent
// binding — and its request arrives via the launcher env.
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: leafInvokePayload() })),
});

function leafInvokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD || process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}";
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

const CODEX = process.env.LOOM_CODEX_BIN || "codex";

export async function run(ctx = {}) {
  const request = requestPayload(ctx);
  const taskRunId = String(request.task_run_id || request.taskRunId || process.env.LOOM_TASK_RUN_ID || "session-eval");
  const input = request.input || {};
  const kind = String(input.kind || "");

  if (kind === "session_eval_preflight") {
    const available = codexAvailable();
    return {
      status: "completed",
      exitCode: 0,
      logsRef: "logs://" + taskRunId,
      runtimeMetadata: {
        task_runner: "session-eval-task-runner",
        runtime_strategy: "codex-eval-preflight",
        runner: String(request.runner || "session-eval-task-runner"),
        codex_available: String(available),
      },
    };
  }

  if (kind !== "session_eval_judge") {
    return failed("judge_error", "unknown session eval task kind " + JSON.stringify(kind), taskRunId, request, input);
  }

  const backend = String(input.backend || "");
  if (backend !== "codex") {
    return failed("eval_backend_unsupported", "session eval backend " + backend + " is not supported", taskRunId, request, input);
  }

  const model = String(input.model || "").trim();
  const judgeInput = String(input.judgeInput || "");
  if (!model) {
    return failed("judge_error", "session eval judge input missing model", taskRunId, request, input);
  }
  if (!judgeInput.trim()) {
    return failed("judge_error", "session eval judge input missing rendered transcript", taskRunId, request, input);
  }

  const work = fs.mkdtempSync(path.join(os.tmpdir(), "loom-session-eval-"));
  const schemaPath = path.join(work, "session-eval-output.schema.json");
  const outPath = path.join(work, "last-message.txt");
  fs.writeFileSync(schemaPath, JSON.stringify(outputSchema()));

  const prompt = EVAL_RUBRIC_V1 + "\n\n" + judgeInput + "\n\nReturn ONLY the JSON object matching the output schema.";
  try {
    execFileSync(CODEX, [
      "exec",
      "--skip-git-repo-check",
      "--sandbox", "read-only",
      "-C", work,
      "--model", model,
      "--output-schema", schemaPath,
      "--output-last-message", outPath,
      "-",
    ], { input: prompt, stdio: ["pipe", "pipe", "pipe"], timeout: 10 * 60 * 1000 });
  } catch (err) {
    const message = errorOutput(err);
    const errorClass = contextOverflow(message) ? "transcript_too_large" : "judge_error";
    return failed(errorClass, "codex eval judge failed: " + message, taskRunId, request, input);
  }

  let result;
  try {
    result = parseLastMessage(fs.readFileSync(outPath, "utf8"));
  } catch (err) {
    return failed("judge_error", "could not parse codex eval result: " + errorMessage(err), taskRunId, request, input);
  }

  const evalCost = zeroEvalCost();
  // codex exec with --output-last-message does not expose a stable usage
  // channel on this invocation path. Keep cost present but zeroed rather than
  // parsing fragile human output.
  return {
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + taskRunId,
    runtimeMetadata: {
      task_runner: "session-eval-task-runner",
      runtime_strategy: "codex-eval",
      runner: String(request.runner || "session-eval-task-runner"),
      eval_result: JSON.stringify(result),
      judge_model: model,
      eval_cost: JSON.stringify(evalCost),
    },
  };
}

function codexAvailable() {
  try {
    execFileSync(CODEX, ["--version"], { stdio: ["ignore", "ignore", "ignore"], timeout: 10 * 1000 });
    return true;
  } catch {
    return false;
  }
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

function failed(errorClass, message, taskRunId, request = {}, input = {}) {
  return {
    status: "failed",
    exitCode: 1,
    errorClass,
    errorMessage: message,
    logsRef: "logs://" + taskRunId,
    runtimeMetadata: {
      task_runner: "session-eval-task-runner",
      runtime_strategy: "codex-eval",
      runner: String(request.runner || "session-eval-task-runner"),
      judge_model: String(input.model || ""),
    },
  };
}

function parseLastMessage(raw) {
  const text = String(raw || "").trim();
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  if (start < 0 || end <= start) {
    throw new Error("last message did not contain a JSON object");
  }
  return JSON.parse(text.slice(start, end + 1));
}

function contextOverflow(text) {
  const lower = String(text || "").toLowerCase();
  return [
    "context length",
    "context_length_exceeded",
    "maximum context",
    "prompt is too long",
    "exceeds the context window",
  ].some((needle) => lower.includes(needle));
}

function errorOutput(err) {
  const parts = [];
  if (err && err.message) {
    parts.push(err.message);
  }
  if (err && err.stdout) {
    parts.push(String(err.stdout));
  }
  if (err && err.stderr) {
    parts.push(String(err.stderr));
  }
  return parts.join("\n").trim() || String(err);
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}

function zeroEvalCost() {
  return { input_tokens: 0, output_tokens: 0, total_tokens: 0 };
}

const EVAL_RUBRIC_V1 = `You are an evaluation judge for autonomous coding-agent sessions. You will be
given (1) a SESSION RECORD header — ground truth from the harness about how
the session ended, (2) the session's complete transcript, verbatim, and (3)
the session's diff statistics (files changed, lines added/removed) — the full patch content is not included. Produce a single JSON
object matching the provided output schema. Output nothing else.

## Trust rules

- Judge artifacts, not claims. Agents routinely narrate success on failed
  work. \`outcome_success\` must rest on evidence: the diff, file contents
  shown in tool results, command output, test results. A confident closing
  summary is not evidence.
- The SESSION RECORD header is harness ground truth for how the session
  ended (status, exit_code, error_class). The transcript may end mid-run
  with no terminal marker — that means the session was killed, not that the
  work stopped cleanly. \`error_class\` may be \`Unknown\` even for kills.
- exit_code -1 plus a transcript that ends mid-action with no terminal marker is likely a platform kill (watchdog, shutdown, or backend outage — the record cannot distinguish these). Tag \`killed_or_truncated\`, and score only what the visible transcript supports; do not penalize any dimension for work the truncation hides.
- Do not run commands or read files yourself. Reason only from the provided
  material.

## Scores

Four dimensions, each an integer 0–100. They are independent: a session with
status \`completed\` can score very low on efficiency; a killed session can
still score well on tool_use_quality for the work it did do. Never let one
dimension bleed into another, and never default to the middle — commit to a
band first, then place within it. Scores of 95+ require positive evidence of
excellence, not merely the absence of problems.

Shared band meaning: 0–19 failing · 20–39 poor · 40–59 mixed · 60–79 good
with real gaps · 80–94 strong · 95–100 exemplary, with in-session proof.

**outcome_success** — did the session deliver the outcome its prompt asked
for, as evidenced by artifacts?
- 0–19: no usable progress toward the requested outcome, or net harm.
- 20–39: visible attempt; the deliverable is absent, broken, or unusable.
- 40–59: partial — a meaningful piece exists, but the core ask is unmet.
- 60–79: substantially delivered, with concrete gaps (an unmet requirement,
  no verification, a loose end the prompt asked to tie).
- 80–94: the ask is fully delivered; only minor deviations.
- 95–100: fully delivered AND verified within the session (tests run, output
  checked), with the verification visible in the transcript.

**instruction_adherence** — were the prompt's explicit instructions and
constraints followed, regardless of outcome? Count only instructions
actually present in the transcript's system/user content.
- 0–19: ignored or inverted central instructions.
- 20–39: multiple explicit instructions violated, or one central one.
- 40–59: mostly followed; at least one clear violation of a stated
  requirement or constraint.
- 60–79: followed with minor lapses (formatting, a skipped step of a stated
  procedure) but nothing the prompt marked as critical.
- 80–94: all explicit instructions followed.
- 95–100: all followed, including judgment calls that honored the prompt's
  intent where instructions were ambiguous — cite the moment.

**efficiency** — useful-work density relative to the size of the task: did
the session spend its time and tokens on work that moved the task forward?
This dimension judges the agent's own choices. Time spent obeying an
explicit instruction to wait (a mandated sleep, a required settle period) is
NOT waste here — tag it \`idle_wait\` and attribute it in improvement
insights, but do not deduct efficiency for obedience. Deduct for waits,
loops, re-reads, and detours the agent chose itself.
- 0–19: the session is dominated by waste — idle waiting, loops, repeated
  dead-end attempts (e.g. minutes-long foreground sleeps).
- 20–39: major detours or waits that dwarf the useful work.
- 40–59: noticeable waste — redundant re-reads, re-running unchanged
  commands, exploring far beyond the task's needs — but useful work
  dominates.
- 60–79: minor slack only (an extra verification pass, a small re-read).
- 80–94: tight — nearly every step advances the task.
- 95–100: tight AND well-sequenced for the task's size; no step you could
  point to and delete.

**tool_use_quality** — were tool calls well-chosen and well-formed, and were
their results actually used?
- 0–19: pervasive misuse — malformed calls, wrong tools, output ignored.
- 20–39: repeated failing calls with no adaptation, or results routinely
  misread.
- 40–59: works, but with clear misuse moments (retrying an identical failing
  call, wrong tool for the job, parsing errors shrugged off).
- 60–79: competent; occasional clumsy call or unread result.
- 80–94: consistently correct calls, errors noticed and adapted to.
- 95–100: exemplary — precise calls, failures diagnosed from tool output and
  corrected on the next step; cite an example.

## Error taxonomy tags

\`error_taxonomy_tags\`: zero or more tags, each either from this allowlist or
\`other:<snake_case>\` when nothing on the list fits. Tag only what the
transcript or header evidences; an empty array is the correct output for a
clean session. Tags record what went wrong in THIS session — do not echo the
status field as a tag.

- \`false_success_claim\` — the agent asserted completion/success that the
  artifacts contradict or cannot support.
- \`incomplete_task\` — the session ended (cleanly or not) with the core ask
  unmet.
- \`instruction_violation\` — an explicit prompt instruction or constraint was
  broken.
- \`idle_wait\` — meaningful wall-clock spent deliberately waiting (sleeps,
  polling loops) rather than working. This tag records the FACT of the
  wait, regardless of whether the task instructed it or the agent chose it;
  fault attribution belongs in improvement insights, and agent-chosen waste
  in the efficiency score.
- \`redundant_work\` — repeated or unnecessary operations at material scale
  (re-reading unchanged files, re-running identical commands).
- \`tool_misuse\` — malformed/wrong tool calls, or tool output misread or
  ignored.
- \`hallucinated_state\` — the agent referenced files, results, or prior steps
  that do not exist in the session.
- \`scope_creep\` — changes or actions beyond what the task asked for.
- \`env_or_dependency_failure\` — blocked by the environment (missing tools,
  network, permissions), not by the agent's own choices.
- \`killed_or_truncated\` — the transcript ends mid-run with no terminal
  marker (watchdog kill, timeout, crash).
- \`unsafe_operation\` — a destructive or risky action outside the task's
  mandate (mass deletion, force-push, credential exposure).
- \`verification_skipped\` — the agent declared work done without any check it
  could have run.

## Improvement insights

\`improvement_categories\` has four fixed keys. Each holds 0–3 insight
strings; an empty list is better than filler — most sessions should produce
insights in at most one or two categories. An insight must be:
(a) grounded in a specific moment of this session (cite the entry seq),
(b) actionable as a change to that category's artifact, and
(c) general — it would help future sessions, not just re-litigate this one.
Phrase each as "Change X so that Y", one sentence.

- \`harness\`: the supervisor/runtime around the agent — watchdogs, timeouts,
  tool availability, sandboxing, transcript/diff capture.
- \`linter\`: automated checks (linters, CI gates, validators) that would have
  caught a mistake this session made.
- \`prompt\`: the agent's own system/workflow prompt — instructions that were
  missing, ambiguous, or misleading for this session.
- \`skill\`: repo skills/runbooks/docs the agent lacked or misused (e.g. a
  documented procedure that would have replaced its improvisation).

## Summary

\`judge_summary\`: 2–4 sentences. What the session actually did, the decisive
evidence behind your lowest and highest scores, and any tag worth a reader's
attention. Written for a dashboard reader who has not seen the transcript.

\`score_rationales\`: for each dimension, one sentence naming the evidence
(cite entry seq numbers) that placed the score in its band.

Return ONLY the JSON object. No markdown, no commentary.
`;

function outputSchema() {
  return {
    type: "object",
    additionalProperties: false,
    required: ["scores", "score_rationales", "error_taxonomy_tags", "improvement_categories", "judge_summary"],
    properties: {
      scores: {
        type: "object",
        additionalProperties: false,
        required: ["outcome_success", "instruction_adherence", "efficiency", "tool_use_quality"],
        properties: {
          outcome_success: { type: "integer", description: "0-100 per the rubric bands" },
          instruction_adherence: { type: "integer", description: "0-100 per the rubric bands" },
          efficiency: { type: "integer", description: "0-100 per the rubric bands" },
          tool_use_quality: { type: "integer", description: "0-100 per the rubric bands" },
        },
      },
      score_rationales: {
        type: "object",
        additionalProperties: false,
        required: ["outcome_success", "instruction_adherence", "efficiency", "tool_use_quality"],
        properties: {
          outcome_success: { type: "string", description: "one sentence, cite entry seq" },
          instruction_adherence: { type: "string", description: "one sentence, cite entry seq" },
          efficiency: { type: "string", description: "one sentence, cite entry seq" },
          tool_use_quality: { type: "string", description: "one sentence, cite entry seq" },
        },
      },
      error_taxonomy_tags: {
        type: "array",
        items: { type: "string", description: "allowlist tag or other:<snake_case>" },
      },
      improvement_categories: {
        type: "object",
        additionalProperties: false,
        required: ["harness", "linter", "prompt", "skill"],
        properties: {
          harness: { type: "array", items: { type: "string" } },
          linter: { type: "array", items: { type: "string" } },
          prompt: { type: "array", items: { type: "string" } },
          skill: { type: "array", items: { type: "string" } },
        },
      },
      judge_summary: { type: "string", description: "2-4 sentences for a dashboard reader" },
    },
  };
}
