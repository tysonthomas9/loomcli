import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { defineAgent, defineWorkflow } from "@flue/runtime";

const CODEX = process.env.LOOM_CODEX_BIN || "codex";
const TRANSCRIPT_PROMPT_LIMIT = 12000;
const TRANSCRIPT_MESSAGE_LIMIT = 8000;
const SECRET_ENV_NAME = /(?:^|_)(?:TOKEN|API_KEY|SECRET|PASSWORD|CREDENTIAL|PRIVATE_KEY|SIGNING_KEY|ACCESS_KEY)$/i;
const SECRET_PATTERNS = [
  /-----BEGIN[A-Z ]*PRIVATE KEY-----[\s\S]*?-----END[A-Z ]*PRIVATE KEY-----/g,
  /AKIA[0-9A-Z]{16}/g,
  /x-access-token:[^@\s]+@/gi,
  /Bearer\s+[A-Za-z0-9._~+/=-]{8,}/gi,
  /gh[pousr]_[A-Za-z0-9]{30,}/g,
  /github_pat_[A-Za-z0-9_]{60,}/g,
  /sk-(?:ant-)?[A-Za-z0-9_-]{20,}/g,
  /AIza[0-9A-Za-z_-]{35}/g,
  /xox[baprs]-[A-Za-z0-9-]{10,}/g,
  /eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}/g,
];

// Flue HEAD requires every workflow module to default-export defineWorkflow();
// keep the named run export (invoker/shim path) AND add the flue-native default
// export so the bundled-runner dispatch (fork server.mjs, FLUE_CLI_NAME) works.
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

export async function run(ctx = {}) {
  const request = requestPayload(ctx);
  const taskRunId = String(request.task_run_id || request.taskRunId || process.env.LOOM_TASK_RUN_ID || "review");
  const input = request.input || {};
  const diff = String(input.diff || "");
  const rubric = String(input.rubric || "Review the diff for correctness, security, and clarity.");
  if (!diff.trim()) {
    const message = "review task input carried no diff";
    return failed("empty_diff", message, taskRunId, request, canonicalTranscriptEntries(taskRunId, {
      status: "failed",
      result: message,
    }));
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
    const message = "codex exec failed: " + errorMessage(err);
    return failed("codex_exec_failed", message, taskRunId, request, canonicalTranscriptEntries(taskRunId, {
      prompt,
      status: "failed",
      result: message,
    }));
  }

  let findings;
  let rawFindings = "";
  try {
    rawFindings = fs.readFileSync(outPath, "utf8");
    findings = parseFindings(rawFindings);
  } catch (err) {
    const message = "could not parse codex findings: " + errorMessage(err);
    return failed("codex_no_findings", message, taskRunId, request, canonicalTranscriptEntries(taskRunId, {
      prompt,
      assistant: rawFindings,
      status: "failed",
      result: message,
    }));
  }

  const transcriptEntries = canonicalTranscriptEntries(taskRunId, {
    prompt,
    assistant: rawFindings,
    status: "completed",
  });
  return {
    status: "completed",
    exitCode: 0,
    logsRef: "logs://" + taskRunId,
    transcript_entries: transcriptEntries,
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

function failed(errorClass, message, taskRunId, request = {}, transcriptEntries = []) {
  return {
    status: "failed",
    exitCode: 1,
    errorClass,
    errorMessage: redactTranscriptText(message, TRANSCRIPT_MESSAGE_LIMIT),
    logsRef: "logs://" + taskRunId,
    ...(transcriptEntries.length > 0 ? { transcript_entries: transcriptEntries } : {}),
    runtimeMetadata: {
      task_runner: "github-review-task-runner",
      runtime_strategy: "codex-review",
      runner: String(request.runner || "github-review-task-runner"),
    },
  };
}

// canonicalTranscriptEntries returns the smallest faithful transcript for this
// non-streaming Codex invocation: the exact review prompt (bounded and
// credential-redacted), Codex's output-last-message, and an honest terminal
// result. HostBridge persists this snake_case field as the task transcript.
function canonicalTranscriptEntries(taskRunId, { prompt = "", assistant = "", status, result = "" } = {}) {
  const timestamp = new Date().toISOString();
  const entries = [{
    timestamp,
    role: "system",
    type: "session_meta",
    text: "codex-review session for " + redactTranscriptText(taskRunId, 200),
  }];
  if (String(prompt).trim()) {
    entries.push({
      timestamp,
      role: "user",
      type: "text",
      text: redactTranscriptText(prompt, TRANSCRIPT_PROMPT_LIMIT),
    });
  }
  if (String(assistant).trim()) {
    entries.push({
      timestamp,
      role: "assistant",
      type: "text",
      text: redactTranscriptText(assistant, TRANSCRIPT_MESSAGE_LIMIT),
    });
  }
  entries.push({
    timestamp,
    role: "system",
    type: "result",
    text: redactTranscriptText(result || status || "completed", TRANSCRIPT_MESSAGE_LIMIT),
  });
  return entries.map((entry, index) => ({ seq: index + 1, ...entry }));
}

function redactTranscriptText(value, limit) {
  let text = String(value || "");
  const secretValues = [...new Set(Object.entries(process.env)
    .filter(([name, secret]) => SECRET_ENV_NAME.test(name) && secret && String(secret).length >= 8)
    .map(([, secret]) => String(secret)))]
    .sort((a, b) => b.length - a.length);
  for (const secret of secretValues) {
    text = text.split(secret).join("REDACTED");
  }
  for (const pattern of SECRET_PATTERNS) {
    pattern.lastIndex = 0;
    text = text.replace(pattern, "REDACTED");
  }
  if (text.length <= limit) return text;
  return text.slice(0, limit) + "\n...[transcript truncated]";
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
