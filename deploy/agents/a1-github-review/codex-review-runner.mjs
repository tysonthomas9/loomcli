#!/usr/bin/env node
// Codex-backed task runner for the A1 github-review-agent (live e2e).
//
// The loom task bridge invokes this per review TaskRun with the request JSON
// on LOOM_TASK_RUN_REQUEST_JSON (and stdin). The request's `input` carries
// { kind, repo, prNumber, headSha, baseRef, diff, rubric } — the payload that
// now survives to the runner (loomcli@170ec1fb). This wrapper:
//   1. builds a review prompt from rubric + diff,
//   2. runs the user's authenticated codex CLI non-interactively, read-only,
//      with an --output-schema pinning the findings shape,
//   3. emits the findings JSON on runtimeMetadata.review_findings — exactly
//      where the workflow's extractFindings() reads it.
// No secrets are read or printed: codex uses its own ambient login; the
// GitHub credential never touches this process (the workflow posts via the
// connector, server-side).
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const CODEX = process.env.LOOM_CODEX_BIN || "codex";

function fail(errorClass, msg, taskRunId) {
  process.stdout.write(JSON.stringify({
    status: "failed", exitCode: 1, errorClass, errorMessage: msg,
    logsRef: "logs://" + (taskRunId || "review"),
  }) + "\n");
  process.exit(0); // terminal result reported on stdout; non-zero would mask it
}

const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}");
const taskRunId = request.task_run_id || request.taskRunId || "review";
const input = request.input || {};
const diff = String(input.diff || "");
const rubric = String(input.rubric || "Review the diff for correctness, security, and clarity.");
if (!diff.trim()) fail("empty_diff", "review task input carried no diff", taskRunId);

const work = fs.mkdtempSync(path.join(os.tmpdir(), "loom-codex-review-"));
const schemaPath = path.join(work, "findings.schema.json");
const outPath = path.join(work, "last-message.txt");
fs.writeFileSync(schemaPath, JSON.stringify({
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
}));

const prompt = [
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

// CODEX_HOME (writable) is inherited from the process env — the container
// run mounts the host ~/.codex auth read-only and points CODEX_HOME at a
// writable copy, matching the proven slack-codex stack runner. In-container
// we bypass approvals/sandbox (the container IS the sandbox); a review only
// reasons over the diff in the prompt and runs no commands.
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
  fail("codex_exec_failed", "codex exec failed: " + (err && err.message), taskRunId);
}

let findings;
try {
  const raw = fs.readFileSync(outPath, "utf8").trim();
  // codex may wrap the JSON in prose/fences; extract the outermost object.
  const start = raw.indexOf("{");
  const end = raw.lastIndexOf("}");
  findings = JSON.parse(start >= 0 && end > start ? raw.slice(start, end + 1) : raw);
} catch (err) {
  fail("codex_no_findings", "could not parse codex findings: " + (err && err.message), taskRunId);
}

process.stdout.write(JSON.stringify({
  status: "completed",
  exitCode: 0,
  logsRef: "logs://" + taskRunId,
  runtimeMetadata: {
    task_runner: "codex-review",
    review_findings: JSON.stringify(findings),
  },
}) + "\n");
