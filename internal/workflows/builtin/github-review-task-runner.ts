import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { TaskRunClient } from "@loom/sdk/runner";

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
  const invocation = await TaskRunClient.fromEnv().agent.exec({
    invocationKey: "review",
    backend: "codex",
    model: String(input.model || "unknown"),
    argv: [
      CODEX,
      "exec",
      "--json",
      "--skip-git-repo-check",
      "--dangerously-bypass-approvals-and-sandbox",
      "-C", work,
      "--output-schema", schemaPath,
      "--output-last-message", outPath,
      "-",
    ],
    stdin: prompt,
    timeoutMs: 5 * 60 * 1000,
    transcript: "stream-json",
    close: "deferred",
  });
  if (invocation.spawnError || invocation.timedOut || invocation.exitCode !== 0) {
    const message = invocationError(invocation);
    await finalizeReview(invocation, "failed", "codex exec failed: " + message, { error_class: "codex_exec_failed" });
    return failed("codex_exec_failed", "codex exec failed: " + message, taskRunId, request, invocation);
  }

  let findings;
  try {
    findings = parseFindings(fs.readFileSync(outPath, "utf8"));
  } catch (err) {
    const message = "could not parse codex findings: " + errorMessage(err);
    await finalizeReview(invocation, "failed", message, { error_class: "codex_no_findings" });
    return failed("codex_no_findings", message, taskRunId, request, invocation);
  }

  await finalizeReview(invocation, "completed", `review produced ${findings.comments?.length ?? 0} comment(s)`);
  return {
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + taskRunId,
    runtimeMetadata: {
      task_runner: "github-review-task-runner",
      runtime_strategy: "codex-review",
      runner: String(request.runner || "github-review-task-runner"),
      review_findings: JSON.stringify(findings),
      review_session_id: invocation.session.id || "",
      ...invocation.runtimeMetadata,
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

async function finalizeReview(invocation, status, summary, metadata = undefined) {
  if (typeof invocation.finalize === "function") {
    await invocation.finalize({ status, summary, metadata });
  }
}

function failed(errorClass, message, taskRunId, request = {}, invocation = null) {
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
      review_session_id: invocation?.session?.id || "",
      ...(invocation?.runtimeMetadata || {}),
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

function invocationError(invocation) {
  if (invocation.spawnError) return invocation.spawnError;
  if (invocation.timedOut) return "timed out after 300000ms";
  return [invocation.stderr, invocation.stdout].filter(Boolean).join("\n").trim() || "codex exited " + invocation.exitCode;
}
