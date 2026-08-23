import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { defineAgent, defineWorkflow } from "@flue/runtime";

// Flue HEAD (durable-streams) requires every workflow module to default-export a
// defineWorkflow() definition; a bare `export function run` no longer normalizes
// and the bundle refuses to load at all (DEV-V5-37). This runner is not an LLM
// agent (it execFileSyncs the codex CLI), so the bound agent is a
// credential-free stub (model: false). The request arrives via env — the
// task-runner host-bridge sets LOOM_TASK_RUN_REQUEST_JSON (driver/task_bridge.go)
// — which requestPayload() already reads, so the inner run() body is unchanged.
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

function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

const CODEX = process.env.LOOM_CODEX_BIN || "codex";

export async function run(ctx = {}) {
  const request = requestPayload(ctx);
  const taskRunId = String(request.task_run_id || request.taskRunId || process.env.LOOM_TASK_RUN_ID || "review");
  const input = request.input || {};
  const diff = String(input.diff || "");
  const rubric = String(input.rubric || "Review the diff for correctness, security, and clarity.");
  if (!diff.trim()) {
    return failed("empty_diff", "review task input carried no diff", taskRunId, request);
  }

  const work = fs.mkdtempSync(path.join(os.tmpdir(), "loom-codex-review-"));
  const schemaPath = path.join(work, "findings.schema.json");
  const outPath = path.join(work, "last-message.txt");
  fs.writeFileSync(schemaPath, JSON.stringify(findingsSchema()));

  const prompt = reviewPrompt(input, rubric, diff);
  try {
    execFileSync(CODEX, [
      "exec",
      "--skip-git-repo-check",
      "--dangerously-bypass-approvals-and-sandbox",
      "-C", work,
      "--output-schema", schemaPath,
      "--output-last-message", outPath,
      "-",
    ], { input: prompt, stdio: ["pipe", "ignore", "inherit"], timeout: 5 * 60 * 1000 });
  } catch (err) {
    return failed("codex_exec_failed", "codex exec failed: " + errorMessage(err), taskRunId, request);
  }

  let findings;
  try {
    findings = parseFindings(fs.readFileSync(outPath, "utf8"));
  } catch (err) {
    return failed("codex_no_findings", "could not parse codex findings: " + errorMessage(err), taskRunId, request);
  }

  return {
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + taskRunId,
    runtimeMetadata: {
      task_runner: "github-review-task-runner",
      runtime_strategy: "codex-review",
      runner: String(request.runner || "github-review-task-runner"),
      review_findings: JSON.stringify(findings),
    },
  };
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

function failed(errorClass, message, taskRunId, request = {}) {
  return {
    status: "failed",
    exitCode: 1,
    errorClass,
    errorMessage: message,
    logsRef: "logs://" + taskRunId,
    runtimeMetadata: {
      task_runner: "github-review-task-runner",
      runtime_strategy: "codex-review",
      runner: String(request.runner || "github-review-task-runner"),
    },
  };
}

function findingsSchema() {
  return {
    type: "object",
    required: ["summary", "comments"],
    additionalProperties: false,
    properties: {
      summary: { type: "string", description: "1-3 sentence overall review summary" },
      comments: {
        type: "array",
        items: {
          type: "object",
          required: ["path", "line", "body"],
          additionalProperties: false,
          properties: {
            path: { type: "string", description: "file path from the diff" },
            line: { type: "integer", description: "line number in the new file" },
            body: { type: "string", description: "the review comment" },
          },
        },
      },
    },
  };
}

function reviewPrompt(input, rubric, diff) {
  return [
    "You are a code reviewer. Review the following pull request diff.",
    "Rubric: " + rubric,
    "Repository: " + (input.repo || "?") + "  PR #" + (input.prNumber ?? "?") + "  head " + (input.headSha || "?"),
    "",
    "Return ONLY a JSON object matching the provided output schema: a short `summary`",
    "and a `comments` array of {path, line, body} inline findings (empty array if none).",
    "Do not run any commands; reason only from the diff below.",
    "",
    "--- DIFF ---",
    diff,
  ].join("\n");
}

function parseFindings(raw) {
  const text = String(raw || "").trim();
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  return JSON.parse(start >= 0 && end > start ? text.slice(start, end + 1) : text);
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}
