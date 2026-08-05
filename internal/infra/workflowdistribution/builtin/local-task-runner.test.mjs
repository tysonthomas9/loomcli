import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, it } from "node:test";

import {
  applyRolePolicy,
  backendArgs,
  parseNumstat,
  parseRepoSlug,
  parseStreamJSONTranscript,
  redactSecretsInText,
  resolveBackend,
  resolveBinary,
  run,
  scrubToken,
  taskUsageFromEntries,
} from "./local-task-runner.ts";

// A fake backend CLI that (optionally) writes a file into its cwd, emits
// stream-json on stdout, then exits with FAKE_EXIT_CODE. It lets the runner be
// exercised end-to-end without a real backend installed.
const FAKE_CLI = `#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
const exit = Number(process.env.FAKE_EXIT_CODE || "0");
// Write into the CLI's cwd (the worktree the runner executes in — the isolated
// worktree when one is set up, the host worktree in the fallback path). A bare
// FAKE_WRITE_FILE name is resolved against cwd; an absolute path is honored.
if (process.env.FAKE_WRITE_FILE) {
  const target = path.isAbsolute(process.env.FAKE_WRITE_FILE)
    ? process.env.FAKE_WRITE_FILE
    : path.join(process.cwd(), process.env.FAKE_WRITE_FILE);
  fs.writeFileSync(target, "hello from fake backend\\n");
}
if (process.env.FAKE_WRITE_SECRET_FILE) {
  const target = path.isAbsolute(process.env.FAKE_WRITE_SECRET_FILE)
    ? process.env.FAKE_WRITE_SECRET_FILE
    : path.join(process.cwd(), process.env.FAKE_WRITE_SECRET_FILE);
  fs.writeFileSync(target, String(process.env.LOOM_TASK_RUN_LEASE_TOKEN || "") + "\\n");
}
if (process.env.FAKE_COMMIT_CHANGES === "1") {
  const { execFileSync } = await import("node:child_process");
  execFileSync("git", ["add", "-A"], { cwd: process.cwd() });
  execFileSync("git", ["-c", "user.email=fake@example.test", "-c", "user.name=Fake Backend", "commit", "--no-verify", "-m", "fake backend committed"], { cwd: process.cwd() });
}
if (process.env.FAKE_TASK_OUTCOME && process.env.LOOM_TASK_OUTCOME_FILE) {
  fs.writeFileSync(process.env.LOOM_TASK_OUTCOME_FILE, process.env.FAKE_TASK_OUTCOME);
}
if (process.env.FAKE_STREAM_ERROR) {
  process.stdout.write(JSON.stringify({ type: "error", error: { message: process.env.FAKE_STREAM_ERROR } }) + "\\n");
  process.exit(exit);
}
if (process.env.FAKE_ECHO_SECRET) {
  process.stdout.write(JSON.stringify({ type: "item.completed", item: { id: "message-" + process.env.FAKE_ECHO_SECRET, type: "agent_message", text: "stdout-secret " + process.env.FAKE_ECHO_SECRET } }) + "\\n");
  process.stdout.write(JSON.stringify({ type: "item.completed", item: { id: "command-" + process.env.FAKE_ECHO_SECRET, type: "command_execution", command: "echo " + process.env.FAKE_ECHO_SECRET, aggregated_output: "", exit_code: 0, status: "completed" } }) + "\\n");
  process.stderr.write("stderr-secret " + process.env.FAKE_ECHO_SECRET + "\\n");
}
// Claude-shaped event (matched by the claude parser):
process.stdout.write(JSON.stringify({ type: "assistant", message: { content: [{ type: "text", text: "did the work" }] } }) + "\\n");
// Real codex exec --json shape: agent output nested under item.completed.
process.stdout.write(JSON.stringify({ type: "thread.started", thread_id: "t1" }) + "\\n");
process.stdout.write(JSON.stringify({ type: "item.completed", item: { id: "i1", type: "command_execution", command: "ls", aggregated_output: "out\\n", exit_code: 0, status: "completed" } }) + "\\n");
process.stdout.write(JSON.stringify({ type: "item.completed", item: { id: "i2", type: "agent_message", text: "did the work" } }) + "\\n");
// Optional terminal usage event (codex turn.completed shape), gated so the
// existing transcript-shape tests stay unaffected.
if (process.env.FAKE_USAGE_TOKENS) {
  process.stdout.write(JSON.stringify({ type: "turn.completed", usage: { input_tokens: 123, output_tokens: 45, cached_input_tokens: 6 } }) + "\\n");
}
process.exit(exit);
`;

// A fake codex CLI that reads its stdin to completion and writes what it read to
// FAKE_STDIN_FILE. Proves the runner closes/writes the child's stdin and that
// codex (now a stdin-prompt backend) receives the prompt over stdin. It then
// emits the real codex item.completed shape so the transcript is exercised too.
const FAKE_STDIN_CLI = `#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
let buf = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => { buf += chunk; });
process.stdin.on("end", () => {
  if (process.env.FAKE_STDIN_FILE) {
    fs.writeFileSync(process.env.FAKE_STDIN_FILE, buf);
  }
  if (process.env.FAKE_WRITE_FILE) {
    const target = path.isAbsolute(process.env.FAKE_WRITE_FILE)
      ? process.env.FAKE_WRITE_FILE
      : path.join(process.cwd(), process.env.FAKE_WRITE_FILE);
    fs.writeFileSync(target, "hello from fake stdin backend\\n");
  }
  if (process.env.FAKE_TASK_OUTCOME && process.env.LOOM_TASK_OUTCOME_FILE) {
    fs.writeFileSync(process.env.LOOM_TASK_OUTCOME_FILE, process.env.FAKE_TASK_OUTCOME);
  }
  process.stdout.write(JSON.stringify({ type: "item.completed", item: { type: "agent_message", text: "read the stdin" } }) + "\\n");
  process.exit(Number(process.env.FAKE_EXIT_CODE || "0"));
});
`;

let tmpRoot;
let worktree;
let binDir;
let fakeBin;
let fakeStdinBin;
const savedEnv = {};
const ENV_KEYS = [
  "LOOM_TASK_RUNNER_BACKEND",
  "LOOM_WORKTREE_PATH",
  "LOOM_CODEX_BIN",
  "LOOM_CLAUDE_BIN",
  "LOOM_GEMINI_BIN",
  "LOOM_OPENCODE_BIN",
  "LOOM_CURSOR_BIN",
  "LOOM_LOCALDOGFOOD_BIN",
  "LOOM_TASK_RUN_REQUEST_JSON",
  "FAKE_EXIT_CODE",
  "FAKE_WRITE_FILE",
  "FAKE_WRITE_SECRET_FILE",
  "FAKE_GIT_FAIL_BINARY_DIFF",
  "FAKE_GIT_WRITE_SECRET_AFTER_CHECKOUT",
  "FAKE_GIT_MOVE_HEAD_BEFORE_PUSH",
  "FAKE_GIT_MOVE_HEAD_MARKER",
  "FAKE_GIT_ADVANCE_REMOTE_BEFORE_PUSH",
  "FAKE_GIT_REMOTE_BARE",
  "FAKE_GIT_REMOTE_BRANCH",
  "FAKE_GIT_REMOTE_HEAD_MARKER",
  "FAKE_COMMIT_CHANGES",
  "REAL_GIT_BIN",
  "FAKE_STREAM_ERROR",
  "FAKE_ECHO_SECRET",
  "FAKE_STDIN_FILE",
  "FAKE_USAGE_TOKENS",
  "FAKE_TASK_OUTCOME",
  "FLEET_DB_URL",
  "LOOM_FLEET_DB_URL",
  "GITHUB_TOKEN",
  "GH_TOKEN",
  "LOOM_TASK_RUN_STACKED",
  "LOOM_TASK_RUN_STACK_ID",
  "LOOM_TASK_RUN_OUTPUT_BRANCH",
  "LOOM_TASK_RUN_BASE_REF",
  "LOOM_MAX_BUDGET_USD",
  "LOOM_AGENT_EFFORT",
  "LOOM_CLAUDE_EFFORT",
  "LOOM_OPENCODE_MODEL",
  "LOOM_READ_ONLY",
  "LOOM_ALLOWED_TOOLS",
  "LOOM_DENIED_TOOLS",
  "LOOM_COST_PER_MTOK_INPUT",
  "LOOM_COST_PER_MTOK_OUTPUT",
  "LOOM_TASK_RUNNER_STREAM_STDERR",
  "LOOM_TASK_RUN_LEASE_TOKEN",
  "LOOM_TASK_RUN_PROMPT",
  "LOOM_TASK_RUN_REPOSITORY_REMOTE_URL",
];

// PATH is mutated by some tests; save/restore it separately so git stays callable.
let savedPath;

function setEnv(key, value) {
  if (value === undefined) {
    delete process.env[key];
  } else {
    process.env[key] = value;
  }
}

function installGitFaultWrapper() {
  const dir = fs.mkdtempSync(path.join(tmpRoot, "git-fault-wrapper-"));
  const realGit = execFileSync("/usr/bin/env", ["which", "git"], {
    env: { ...process.env, PATH: savedPath },
  }).toString().trim();
  const wrapper = path.join(dir, "git");
  fs.writeFileSync(wrapper, [
    "#!/usr/bin/env node",
    "import { spawnSync } from \"node:child_process\";",
    "import fs from \"node:fs\";",
    "import path from \"node:path\";",
    "const args = process.argv.slice(2);",
    "if (process.env.FAKE_GIT_FAIL_BINARY_DIFF === \"1\" && args.includes(\"diff\") && args.includes(\"--binary\")) process.exit(2);",
    "if (process.env.FAKE_GIT_MOVE_HEAD_BEFORE_PUSH === \"1\" && args.includes(\"push\")) {",
    "  const c = args.indexOf(\"-C\");",
    "  const cwd = c >= 0 && args[c + 1] ? args[c + 1] : process.cwd();",
    "  fs.writeFileSync(path.join(cwd, \"post-scan-head-move.txt\"), \"must not be published\\n\");",
    "  let mutation = spawnSync(process.env.REAL_GIT_BIN, [\"-C\", cwd, \"add\", \"-A\"], { stdio: \"inherit\", env: process.env });",
    "  if (mutation.error || mutation.status !== 0) process.exit(mutation.status == null ? 1 : mutation.status);",
    "  mutation = spawnSync(process.env.REAL_GIT_BIN, [\"-C\", cwd, \"-c\", \"user.email=fault@example.test\", \"-c\", \"user.name=Fault Wrapper\", \"commit\", \"--no-verify\", \"-m\", \"post-scan head move\"], { stdio: \"inherit\", env: process.env });",
    "  if (mutation.error || mutation.status !== 0) process.exit(mutation.status == null ? 1 : mutation.status);",
    "  const moved = spawnSync(process.env.REAL_GIT_BIN, [\"-C\", cwd, \"rev-parse\", \"HEAD\"], { encoding: \"utf8\", env: process.env });",
    "  if (moved.error || moved.status !== 0) process.exit(moved.status == null ? 1 : moved.status);",
    "  if (process.env.FAKE_GIT_MOVE_HEAD_MARKER) fs.writeFileSync(process.env.FAKE_GIT_MOVE_HEAD_MARKER, moved.stdout.trim() + \"\\n\");",
    "}",
    "if (process.env.FAKE_GIT_ADVANCE_REMOTE_BEFORE_PUSH === \"1\" && args.includes(\"push\")) {",
    "  const bare = process.env.FAKE_GIT_REMOTE_BARE;",
    "  const branch = process.env.FAKE_GIT_REMOTE_BRANCH;",
    "  const ref = \"refs/heads/\" + branch;",
    "  const current = spawnSync(process.env.REAL_GIT_BIN, [\"--git-dir\", bare, \"rev-parse\", ref], { encoding: \"utf8\", env: process.env });",
    "  if (current.error || current.status !== 0) process.exit(current.status == null ? 1 : current.status);",
    "  const tree = spawnSync(process.env.REAL_GIT_BIN, [\"--git-dir\", bare, \"rev-parse\", current.stdout.trim() + \"^{tree}\"], { encoding: \"utf8\", env: process.env });",
    "  if (tree.error || tree.status !== 0) process.exit(tree.status == null ? 1 : tree.status);",
    "  const commitEnv = { ...process.env, GIT_AUTHOR_NAME: \"Race\", GIT_AUTHOR_EMAIL: \"race@example.test\", GIT_COMMITTER_NAME: \"Race\", GIT_COMMITTER_EMAIL: \"race@example.test\" };",
    "  const moved = spawnSync(process.env.REAL_GIT_BIN, [\"--git-dir\", bare, \"commit-tree\", tree.stdout.trim(), \"-p\", current.stdout.trim(), \"-m\", \"concurrent remote advance\"], { encoding: \"utf8\", env: commitEnv });",
    "  if (moved.error || moved.status !== 0) process.exit(moved.status == null ? 1 : moved.status);",
    "  const update = spawnSync(process.env.REAL_GIT_BIN, [\"--git-dir\", bare, \"update-ref\", ref, moved.stdout.trim(), current.stdout.trim()], { stdio: \"inherit\", env: process.env });",
    "  if (update.error || update.status !== 0) process.exit(update.status == null ? 1 : update.status);",
    "  if (process.env.FAKE_GIT_REMOTE_HEAD_MARKER) fs.writeFileSync(process.env.FAKE_GIT_REMOTE_HEAD_MARKER, moved.stdout.trim() + \"\\n\");",
    "}",
    "const result = spawnSync(process.env.REAL_GIT_BIN, args, { stdio: \"inherit\", env: process.env });",
    "if (result.error || result.status !== 0) process.exit(result.status == null ? 1 : result.status);",
    "if (process.env.FAKE_GIT_WRITE_SECRET_AFTER_CHECKOUT === \"1\" && args.includes(\"checkout\") && args.includes(\"-B\")) {",
    "  const c = args.indexOf(\"-C\");",
    "  const cwd = c >= 0 && args[c + 1] ? args[c + 1] : process.cwd();",
    "  fs.writeFileSync(path.join(cwd, \"delayed-credential.txt\"), String(process.env.LOOM_TASK_RUN_LEASE_TOKEN || \"\") + \"\\n\");",
    "}",
    "",
  ].join("\n"), { mode: 0o755 });
  process.env.REAL_GIT_BIN = realGit;
  process.env.PATH = dir + path.delimiter + savedPath;
}

beforeEach(() => {
  savedPath = process.env.PATH;
  for (const key of ENV_KEYS) {
    savedEnv[key] = process.env[key];
    delete process.env[key];
  }
  tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-local-runner-"));
  worktree = path.join(tmpRoot, "worktree");
  fs.mkdirSync(worktree, { recursive: true });
  execFileSync("git", ["init", "-q"], { cwd: worktree });
  execFileSync("git", ["config", "user.email", "t@example.test"], { cwd: worktree });
  execFileSync("git", ["config", "user.name", "Test"], { cwd: worktree });
  fs.writeFileSync(path.join(worktree, "README.md"), "base\n");
  execFileSync("git", ["add", "."], { cwd: worktree });
  execFileSync("git", ["commit", "-q", "-m", "init"], { cwd: worktree });

  binDir = path.join(tmpRoot, "bin");
  fs.mkdirSync(binDir, { recursive: true });
  fakeBin = path.join(binDir, "fake-backend.mjs");
  fs.writeFileSync(fakeBin, FAKE_CLI, { mode: 0o755 });
  fakeStdinBin = path.join(binDir, "fake-stdin-backend.mjs");
  fs.writeFileSync(fakeStdinBin, FAKE_STDIN_CLI, { mode: 0o755 });

  // Provide a request via env (no @loom/sdk in this context, so the runner
  // falls back to LOOM_TASK_RUN_REQUEST_JSON).
  setEnv("LOOM_TASK_RUN_REQUEST_JSON", JSON.stringify({
    task_run_id: "tr-1",
    task_id: "T-1",
    runner: "local-task-runner",
    workspace_key: "ws",
    input: { title: "Do the thing" },
  }));
});

afterEach(() => {
  for (const key of ENV_KEYS) {
    setEnv(key, savedEnv[key]);
  }
  process.env.PATH = savedPath;
  try {
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  } catch {
    // best-effort cleanup
  }
});

describe("local-task-runner pure helpers", () => {
  it("prepends trusted role policy without changing an unconstrained prompt", () => {
    assert.equal(applyRolePolicy("Do the work"), "Do the work");
    process.env.LOOM_READ_ONLY = "1";
    process.env.LOOM_ALLOWED_TOOLS = "read, grep,read";
    process.env.LOOM_DENIED_TOOLS = "write";
    const prompt = applyRolePolicy("Do the work");
    assert.match(prompt, /REPOSITORY READ-ONLY run/);
    assert.match(prompt, /MUST save a requested design/);
    assert.match(prompt, /task-data handoff/);
    assert.match(prompt, /read, grep/);
    assert.match(prompt, /Do not use these tool categories: write/);
    assert.ok(prompt.endsWith("Do the work"));
  });

  it("resolveBackend defaults to codex", () => {
    delete process.env.LOOM_TASK_RUNNER_BACKEND;
    assert.equal(resolveBackend(), "codex");
  });

  it("resolveBackend lowercases the env value", () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "Claude";
    assert.equal(resolveBackend(), "claude");
  });

  it("backendArgs mirror the Go backend builders", () => {
    delete process.env.LOOM_OPENCODE_MODEL; // assert the no-model opencode form
    delete process.env.LOOM_MAX_BUDGET_USD; // assert the default-budget claude form
    delete process.env.LOOM_AGENT_EFFORT;
    delete process.env.LOOM_CLAUDE_EFFORT;
    // codex no longer takes the prompt positionally: trailing "-" reads it from
    // stdin (headless / no-PTY path).
    assert.deepEqual(backendArgs("codex", "/w", "P"), [
      "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "-",
    ]);
    // claude carries the default $50 budget cap (backend_claude.go parity).
    assert.deepEqual(backendArgs("claude", "/w", "P"), [
      "-p", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions", "--max-budget-usd", "50.00", "P",
    ]);
    assert.deepEqual(backendArgs("gemini", "/w", "P"), [
      "--approval-mode=yolo", "-p", "P", "-o", "stream-json",
    ]);
    assert.deepEqual(backendArgs("opencode", "/w", "P"), [
      "run", "--format", "json", "--dir", "/w",
    ]);
    assert.deepEqual(backendArgs("cursor", "/w", "P"), [
      "-p", "--output-format", "stream-json", "--force", "P",
    ]);
    assert.deepEqual(backendArgs("localdogfood", "/w", "P"), ["invoke"]);
  });

  it("claude argv carries budget+effort: default $50, override, 0 opts out, invalid falls back", () => {
    delete process.env.LOOM_AGENT_EFFORT;
    delete process.env.LOOM_CLAUDE_EFFORT;
    // explicit override formats to 2 decimals
    process.env.LOOM_MAX_BUDGET_USD = "10.5";
    assert.deepEqual(backendArgs("claude", "/w", "P").slice(-3), ["--max-budget-usd", "10.50", "P"]);
    // 0 opts out of the budget cap entirely
    process.env.LOOM_MAX_BUDGET_USD = "0";
    assert.ok(!backendArgs("claude", "/w", "P").includes("--max-budget-usd"), "0 must opt out of the budget cap");
    // invalid -> default
    process.env.LOOM_MAX_BUDGET_USD = "abc";
    assert.deepEqual(backendArgs("claude", "/w", "P").slice(-3), ["--max-budget-usd", "50.00", "P"]);
    // effort from LOOM_AGENT_EFFORT (falls back to LOOM_CLAUDE_EFFORT)
    delete process.env.LOOM_MAX_BUDGET_USD;
    process.env.LOOM_AGENT_EFFORT = "high";
    assert.deepEqual(backendArgs("claude", "/w", "P").slice(-3), ["--effort", "high", "P"]);
  });

  it("backendArgs pins the opencode model from LOOM_OPENCODE_MODEL (Go parity)", () => {
    const prev = process.env.LOOM_OPENCODE_MODEL;
    process.env.LOOM_OPENCODE_MODEL = "openai/gpt-5.4-fast";
    try {
      assert.deepEqual(backendArgs("opencode", "/w", "P"), [
        "run", "--format", "json", "--dir", "/w",
        "--model", "openai/gpt-5.4-fast",
      ]);
    } finally {
      if (prev === undefined) delete process.env.LOOM_OPENCODE_MODEL;
      else process.env.LOOM_OPENCODE_MODEL = prev;
    }
  });

  it("resolveBinary honors LOOM_<BACKEND>_BIN absolute override", () => {
    const env = { LOOM_CODEX_BIN: fakeBin };
    assert.equal(resolveBinary("codex", env), fakeBin);
  });

  it("resolveBinary returns '' when the binary is missing", () => {
    const env = { PATH: binDir };
    assert.equal(resolveBinary("codex", env), "");
  });

  it("resolveBinary finds a binary on PATH", () => {
    const aliased = path.join(binDir, "codex");
    fs.copyFileSync(fakeBin, aliased);
    fs.chmodSync(aliased, 0o755);
    assert.equal(resolveBinary("codex", { PATH: binDir }), aliased);
  });

  it("resolveBinary defaults cursor to cursor-agent, not the `cursor` IDE launcher", () => {
    const aliased = path.join(binDir, "cursor-agent");
    fs.copyFileSync(fakeBin, aliased);
    fs.chmodSync(aliased, 0o755);
    assert.equal(resolveBinary("cursor", { PATH: binDir }), aliased);
  });

  it("parseNumstat sums files and lines", () => {
    assert.deepEqual(parseNumstat("3\t1\ta.txt\n0\t2\tb.txt\n"), {
      filesChanged: 2,
      linesAdded: 3,
      linesRemoved: 3,
    });
  });

  it("parseNumstat treats binary (- -) entries as one changed file, 0 lines", () => {
    assert.deepEqual(parseNumstat("-\t-\timg.png\n"), {
      filesChanged: 1,
      linesAdded: 0,
      linesRemoved: 0,
    });
  });

  it("parseStreamJSONTranscript extracts claude assistant text", () => {
    const line = JSON.stringify({ type: "assistant", message: { content: [{ type: "text", text: "hi" }] } });
    const entries = parseStreamJSONTranscript("claude", line + "\n");
    assert.equal(entries[0].type, "session_meta");
    assert.ok(entries.some((e) => e.role === "assistant" && e.text === "hi"));
  });

  it("parseStreamJSONTranscript handles the real codex item.completed shape", () => {
    // Real `codex exec --json` nests agent output under item.completed; the
    // simplified flat fake event never matched the original outer-type parser,
    // which is why the dropped-output gap went unnoticed.
    const lines = [
      JSON.stringify({ type: "thread.started", thread_id: "abc" }),
      JSON.stringify({ type: "turn.started" }),
      JSON.stringify({
        type: "item.completed",
        item: { id: "i1", type: "command_execution", command: "ls", aggregated_output: "x", exit_code: 0, status: "completed" },
      }),
      JSON.stringify({
        type: "item.completed",
        item: { id: "i2", type: "agent_message", text: "did the work" },
      }),
      JSON.stringify({ type: "turn.completed", usage: {} }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("codex", lines + "\n");
    assert.equal(entries[0].type, "session_meta");
    // agent_message -> assistant/text
    assert.ok(
      entries.some((e) => e.role === "assistant" && e.type === "text" && e.text === "did the work"),
      "expected an assistant text entry from item.completed agent_message",
    );
    // command_execution -> assistant/tool_use (shell)
    const toolUse = entries.find((e) => e.type === "tool_use");
    assert.ok(toolUse, "expected a tool_use entry from item.completed command_execution");
    assert.equal(toolUse.role, "assistant");
    assert.equal(toolUse.tool_name, "shell");
    assert.equal(toolUse.tool_input.command, "ls");
    assert.equal(toolUse.output, "x");
  });

  it("parseStreamJSONTranscript surfaces codex reasoning items", () => {
    const line = JSON.stringify({ type: "item.completed", item: { type: "reasoning", text: "thinking" } });
    const entries = parseStreamJSONTranscript("codex", line + "\n");
    assert.ok(entries.some((e) => e.role === "assistant" && e.type === "reasoning" && e.text === "thinking"));
  });

  it("parseStreamJSONTranscript still supports the flat codex fallback shape", () => {
    const line = JSON.stringify({ type: "agent_message", text: "flat output" });
    const entries = parseStreamJSONTranscript("codex", line + "\n");
    assert.ok(entries.some((e) => e.role === "assistant" && e.type === "text" && e.text === "flat output"));
  });

  it("parseStreamJSONTranscript parses cursor assistant text + tool_call events", () => {
    const lines = [
      JSON.stringify({ type: "assistant", message: { role: "assistant", content: [{ type: "text", text: "creating it" }] } }),
      JSON.stringify({
        type: "tool_call",
        subtype: "completed",
        call_id: "tc1",
        tool_call: { editToolCall: { args: { path: "hello.md" }, result: { success: { message: "wrote hello.md" } } }, toolCallId: "tc1" },
      }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("cursor", lines + "\n");
    assert.equal(entries[0].type, "session_meta");
    assert.ok(entries.some((e) => e.role === "assistant" && e.type === "text" && e.text === "creating it"));
    const tool = entries.find((e) => e.type === "tool_use");
    assert.ok(tool, "expected a tool_use entry from the cursor tool_call event");
    assert.equal(tool.tool_name, "edit");
    assert.equal(tool.tool_input.path, "hello.md");
    assert.ok(String(tool.output).includes("wrote hello.md"));
  });

  it("parseStreamJSONTranscript parses opencode text + tool_use events", () => {
    const lines = [
      JSON.stringify({ type: "text", part: { type: "text", text: "done" } }),
      JSON.stringify({
        type: "tool_use",
        part: { type: "tool", tool: "apply_patch", callID: "c1", state: { status: "completed", input: { patchText: "X" }, output: "Success" } },
      }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("opencode", lines + "\n");
    assert.equal(entries[0].type, "session_meta");
    assert.ok(entries.some((e) => e.role === "assistant" && e.type === "text" && e.text === "done"));
    const tool = entries.find((e) => e.type === "tool_use");
    assert.ok(tool, "expected a tool_use entry from the opencode tool_use event");
    assert.equal(tool.tool_name, "apply_patch");
    assert.equal(tool.tool_input.patchText, "X");
    assert.equal(tool.output, "Success");
  });

  it("parseStreamJSONTranscript records codex file_change, dedupes item.started, and emits usage", () => {
    const lines = [
      JSON.stringify({ type: "item.started", item: { type: "command_execution", command: "cat hello.md" } }),
      JSON.stringify({ type: "item.completed", item: { type: "file_change", changes: [{ path: "hello.md", kind: "add" }], status: "completed" } }),
      JSON.stringify({ type: "item.completed", item: { type: "command_execution", command: "cat hello.md", aggregated_output: "hi\n", exit_code: 0, status: "completed" } }),
      JSON.stringify({ type: "turn.completed", usage: { input_tokens: 100, output_tokens: 20, reasoning_output_tokens: 5 } }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("codex", lines + "\n");
    // file_change is recorded as an apply_patch tool_use
    const fc = entries.find((e) => e.type === "tool_use" && e.tool_name === "apply_patch");
    assert.ok(fc, "expected file_change to produce an apply_patch tool_use");
    assert.equal(fc.tool_input.changes[0].path, "hello.md");
    assert.equal(fc.tool_input.changes[0].kind, "add");
    // the cat command appears exactly once (item.started deduped), output preserved incl. newline
    const shells = entries.filter((e) => e.type === "tool_use" && e.tool_name === "shell");
    assert.equal(shells.length, 1, "item.started must not duplicate the shell call");
    assert.equal(shells[0].output, "hi\n");
    // terminal usage entry
    const result = entries.find((e) => e.type === "result");
    assert.ok(result && /in=100/.test(result.text) && /out=20/.test(result.text), "expected a usage result entry");
  });

  it("parseStreamJSONTranscript captures claude tool_result outputs + thinking", () => {
    const lines = [
      JSON.stringify({ type: "assistant", message: { content: [{ type: "thinking", thinking: "let me plan" }, { type: "tool_use", id: "t1", name: "Read", input: { file_path: "hello.md" } }] } }),
      JSON.stringify({ type: "user", message: { content: [{ type: "tool_result", tool_use_id: "t1", content: "1\thi from claude\n" }] } }),
      JSON.stringify({ type: "result", subtype: "success", is_error: false, num_turns: 3, total_cost_usd: 0.18, usage: { input_tokens: 8000, output_tokens: 300 } }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("claude", lines + "\n");
    // thinking -> reasoning
    assert.ok(entries.some((e) => e.type === "reasoning" && e.text === "let me plan"));
    // tool_result (the read-back content) is captured as a tool/output entry
    const tr = entries.find((e) => e.type === "tool_result");
    assert.ok(tr, "expected a tool_result entry");
    assert.equal(tr.role, "tool");
    assert.equal(tr.tool_use_id, "t1");
    assert.ok(tr.output.includes("hi from claude"), "the read-back content must survive");
    // result entry with usage + cost
    const result = entries.find((e) => e.type === "result");
    assert.ok(result && /cost=0.18/.test(result.text) && /turns=3/.test(result.text));
  });

  it("parseStreamJSONTranscript merges cursor started+completed (keeps read input + invocation order)", () => {
    const lines = [
      JSON.stringify({ type: "tool_call", subtype: "started", call_id: "e1", tool_call: { editToolCall: { args: { path: "hello.md", streamContent: "hi\n" } }, toolCallId: "e1" } }),
      JSON.stringify({ type: "tool_call", subtype: "started", call_id: "r1", tool_call: { readToolCall: { args: { path: "hello.md" } }, toolCallId: "r1" } }),
      JSON.stringify({ type: "tool_call", subtype: "completed", call_id: "r1", tool_call: { readToolCall: { result: { error: { errorMessage: "File not found" } } }, toolCallId: "r1" } }),
      JSON.stringify({ type: "tool_call", subtype: "completed", call_id: "e1", tool_call: { editToolCall: { result: { success: { message: "wrote" } } }, toolCallId: "e1" } }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("cursor", lines + "\n");
    const tools = entries.filter((e) => e.type === "tool_use");
    // exactly two tool entries (merged, not duplicated by started/completed)
    assert.equal(tools.length, 2);
    // invocation order: edit issued first, then read (even though read completed first)
    assert.equal(tools[0].tool_name, "edit");
    assert.equal(tools[1].tool_name, "read");
    // the read's input path (only on the started event) is preserved
    assert.equal(tools[1].tool_input.path, "hello.md");
    // results merged onto the right calls; the failed read is marked, the edit is not
    assert.ok(String(tools[0].output).includes("wrote"));
    assert.ok(!tools[0].output.startsWith("[error] "), "successful edit must not be marked");
    assert.ok(String(tools[1].output).includes("File not found"));
    assert.ok(tools[1].output.startsWith("[error] "), "failed read must be marked [error]");
  });

  it("parseStreamJSONTranscript keeps opencode error tool states (marked, not dropped) + accumulates usage", () => {
    const lines = [
      JSON.stringify({ type: "tool_use", part: { type: "tool", tool: "bash", callID: "b1", state: { status: "error", input: { command: "false" }, error: "exit 1" } } }),
      JSON.stringify({ type: "step_finish", part: { reason: "tool-calls", tokens: { input: 100, output: 10, reasoning: 7, cache: { read: 50 } }, cost: 0.01 } }),
      JSON.stringify({ type: "step_finish", part: { reason: "stop", tokens: { input: 20, output: 5, reasoning: 0, cache: { read: 50 } }, cost: 0.02 } }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("opencode", lines + "\n");
    // failed tool is present, not dropped, and marked
    const tool = entries.find((e) => e.type === "tool_use");
    assert.ok(tool, "error tool state must not be dropped");
    assert.ok(tool.output.startsWith("[error] "), "error tool must be marked");
    // usage: input/output/reasoning/cost summed, cache_read taken latest (not doubled)
    const result = entries.find((e) => e.type === "result");
    const usage = JSON.parse(result.output);
    assert.equal(usage.input_tokens, 120);
    assert.equal(usage.output_tokens, 15);
    assert.equal(usage.reasoning_tokens, 7);
    assert.equal(usage.cache_read_tokens, 50, "cache_read must be latest, not summed");
  });

  it("parseStreamJSONTranscript surfaces an opencode fatal stream error", () => {
    const entries = parseStreamJSONTranscript("opencode", JSON.stringify({ type: "error", error: { message: "model overloaded" } }) + "\n");
    const result = entries.find((e) => e.type === "result");
    assert.ok(result && result.text.startsWith("failed: model overloaded"), "error event must mark the run failed");
  });

  it("parseStreamJSONTranscript accepts cursor usage in snake_case too", () => {
    const entries = parseStreamJSONTranscript("cursor", JSON.stringify({ type: "result", is_error: false, usage: { input_tokens: 11, output_tokens: 2 } }) + "\n");
    const result = entries.find((e) => e.type === "result");
    assert.ok(result && /in=11/.test(result.text) && /out=2/.test(result.text), "snake_case usage must not be silently dropped");
  });

  it("parseStreamJSONTranscript parses gemini candidates text + usageMetadata usage", () => {
    const lines = [
      JSON.stringify({ candidates: [{ content: { parts: [{ text: "gemini says hi" }] } }] }),
      JSON.stringify({ candidates: [{ content: { parts: [{ text: " and more" }] } }], usageMetadata: { promptTokenCount: 200, candidatesTokenCount: 40 } }),
    ].join("\n");
    const entries = parseStreamJSONTranscript("gemini", lines + "\n");
    const texts = entries.filter((e) => e.type === "text").map((e) => e.text);
    assert.deepEqual(texts, ["gemini says hi", " and more"]);
    // Google-native usageMetadata maps to top-level token fields (was zero before).
    const usage = taskUsageFromEntries(entries);
    assert.equal(usage.input_tokens, 200);
    assert.equal(usage.output_tokens, 40);
  });

  it("parseStreamJSONTranscript accepts gemini OpenAI-compatible usage too", () => {
    const entries = parseStreamJSONTranscript("gemini", JSON.stringify({ type: "result", usage: { input_tokens: 12, output_tokens: 3 } }) + "\n");
    const usage = taskUsageFromEntries(entries);
    assert.equal(usage.input_tokens, 12);
    assert.equal(usage.output_tokens, 3);
  });

  it("parseStreamJSONTranscript never emits a non-RFC3339 timestamp (bad stamp cannot poison the decode)", () => {
    const entries = parseStreamJSONTranscript("claude", JSON.stringify({ type: "user", timestamp: "not-a-date", message: { content: [{ type: "tool_result", tool_use_id: "t", content: "x" }] } }) + "\n");
    for (const e of entries) {
      assert.ok(!Number.isNaN(new Date(e.timestamp).getTime()), `entry timestamp must be valid RFC3339, got ${e.timestamp}`);
    }
  });

  it("taskUsageFromEntries surfaces codex usage as top-level token fields", () => {
    const entries = parseStreamJSONTranscript("codex", JSON.stringify({ type: "turn.completed", usage: { input_tokens: 100, output_tokens: 20, cached_input_tokens: 30, reasoning_output_tokens: 5 } }) + "\n");
    const usage = taskUsageFromEntries(entries);
    assert.equal(usage.input_tokens, 100);
    assert.equal(usage.output_tokens, 20);
    assert.equal(usage.cache_read_tokens, 30);
    // reasoning/duration/turns have no fleet-db TaskRun column -> not surfaced top-level
    assert.equal(usage.reasoning_tokens, undefined);
  });

  it("taskUsageFromEntries maps claude cost_usd to estimated_cost_usd", () => {
    const entries = parseStreamJSONTranscript("claude", JSON.stringify({ type: "result", is_error: false, total_cost_usd: 0.18, usage: { input_tokens: 8000, output_tokens: 300, cache_read_input_tokens: 12, cache_creation_input_tokens: 7 } }) + "\n");
    const usage = taskUsageFromEntries(entries);
    assert.equal(usage.input_tokens, 8000);
    assert.equal(usage.output_tokens, 300);
    assert.equal(usage.cache_read_tokens, 12);
    assert.equal(usage.cache_write_tokens, 7);
    assert.equal(usage.estimated_cost_usd, 0.18);
  });

  it("taskUsageFromEntries returns {} when no usage was reported", () => {
    // minimal/gemini fallback (and early failures) have no terminal result entry,
    // so there is nothing to surface — the runner must spread an empty object.
    assert.deepEqual(taskUsageFromEntries([{ seq: 1, role: "system", type: "session_meta", text: "x" }]), {});
    assert.deepEqual(taskUsageFromEntries([]), {});
    assert.deepEqual(taskUsageFromEntries(null), {});
  });

  it("cost is sourced only from the backend CLI — passthrough incl. 0, never estimated", () => {
    // claude/opencode report a cost the CLI computed -> surfaced verbatim.
    const withCost = [{ seq: 1, role: "system", type: "result", output: JSON.stringify({ input_tokens: 10, output_tokens: 2, cost_usd: 0.0723 }) }];
    assert.equal(taskUsageFromEntries(withCost).estimated_cost_usd, 0.0723);
    // opencode on a subscription reports a LEGITIMATE 0 -> kept as 0, not fabricated up.
    const zeroCost = [{ seq: 1, role: "system", type: "result", output: JSON.stringify({ input_tokens: 10, output_tokens: 2, cost_usd: 0 }) }];
    assert.equal(taskUsageFromEntries(zeroCost).estimated_cost_usd, 0);
    // codex/cursor/gemini report tokens but NO cost -> estimated_cost_usd left unset
    // (unknown). We never invent a token x rate number for an unpriceable model.
    const noCost = [{ seq: 1, role: "system", type: "result", output: JSON.stringify({ input_tokens: 17603, output_tokens: 37 }) }];
    const u = taskUsageFromEntries(noCost);
    assert.equal(u.input_tokens, 17603);
    assert.equal("estimated_cost_usd" in u, false);
  });

  it("scrubToken masks multiple secrets", () => {
    assert.equal(scrubToken("a S1 b S2 c", "S1", "S2"), "a *** b *** c");
    assert.equal(scrubToken("https://x-access-token:abc123@github.com/o/r"), "https://x-access-token:***@github.com/o/r");
  });

  it("redactSecretsInText redacts high-entropy tokens and known secret shapes, keeps prose/hex", () => {
    // high-entropy random token (no known prefix) -> entropy layer
    assert.ok(redactSecretsInText("key Zq7Xr9Tn2Kp5Wm8Lv3Bc6Df1Hg4Js0Ku5Pl8Qa2Mn6").includes("REDACTED"));
    // known shapes -> pattern layer (low entropy but unambiguous)
    assert.ok(redactSecretsInText("AKIAIOSFODNN7EXAMPLE").includes("REDACTED"), "AWS key");
    assert.ok(redactSecretsInText("ghp_" + "a".repeat(36)).includes("REDACTED"), "GitHub token");
    // ordinary prose is untouched (no >=10-char high-entropy segment)
    assert.equal(redactSecretsInText("the quick brown fox jumps over the lazy dog"), "the quick brown fox jumps over the lazy dog");
    // a git SHA is hex (<=16 symbols => entropy <= 4.0) and must NOT be redacted
    const sha = "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0";
    assert.equal(redactSecretsInText("commit " + sha), "commit " + sha);
  });

  it("parseRepoSlug parses ssh, https, token-embedded, and owner/repo forms", () => {
    assert.deepEqual(parseRepoSlug("git@github.com:owner/repo.git"), { owner: "owner", repo: "repo" });
    assert.deepEqual(parseRepoSlug("https://github.com/owner/repo.git"), { owner: "owner", repo: "repo" });
    assert.deepEqual(parseRepoSlug("https://github.com/owner/repo"), { owner: "owner", repo: "repo" });
    assert.deepEqual(
      parseRepoSlug("https://x-access-token:TOK@github.com/owner/repo.git"),
      { owner: "owner", repo: "repo" },
    );
    assert.deepEqual(parseRepoSlug("owner/repo"), { owner: "owner", repo: "repo" });
    assert.deepEqual(parseRepoSlug("owner/repo.git"), { owner: "owner", repo: "repo" });
  });

  it("parseRepoSlug returns null for junk / non-repo input", () => {
    assert.equal(parseRepoSlug(""), null);
    assert.equal(parseRepoSlug(undefined), null);
    assert.equal(parseRepoSlug("not a url"), null);
    assert.equal(parseRepoSlug("https://example.com/owner/repo.git"), null);
  });

  it("scrubToken masks the bare token and the x-access-token URL form", () => {
    const scrubbed = scrubToken(
      "fatal: https://x-access-token:ghp_SECRET@github.com/o/r.git failed",
      "ghp_SECRET",
    );
    // Neither the bare token nor the credential-bearing URL form may survive.
    assert.ok(!scrubbed.includes("ghp_SECRET"), "bare token must be masked");
    assert.ok(
      !scrubbed.includes("x-access-token:ghp_SECRET@"),
      "x-access-token:<token>@ URL form must be masked",
    );
    assert.ok(scrubbed.includes("***"), "masked text should contain the *** placeholder");

    // A string without the token is unchanged, except the x-access-token:...@
    // URL form is still masked even when the token argument does not match.
    assert.equal(scrubToken("plain error, nothing secret here", "ghp_SECRET"), "plain error, nothing secret here");
    assert.equal(
      scrubToken("remote: https://x-access-token:OTHER@github.com/o/r.git", "ghp_SECRET"),
      "remote: https://x-access-token:***@github.com/o/r.git",
    );
  });
});

describe("local-task-runner fail-closed classes", () => {
  it("rejects and discards changes made by a read-only role", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.LOOM_READ_ONLY = "1";
    process.env.FAKE_WRITE_FILE = "read-only-violation.txt";

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_read_only_violation");
    assert.equal(out.patch, undefined);
    assert.ok(!fs.existsSync(path.join(worktree, "read-only-violation.txt")));
    assert.ok(out.transcript_entries.some((entry) => entry.role === "assistant"));
  });

  it("fails with local_backend_unsupported for an unknown backend", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "frobnicate";
    process.env.LOOM_WORKTREE_PATH = worktree;
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_backend_unsupported");
    assert.equal(out.exitCode, 1);
  });

  it("fails with local_worktree_missing when LOOM_WORKTREE_PATH is unset", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_worktree_missing");
  });

  it("fails with local_worktree_missing when the worktree does not exist", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = path.join(tmpRoot, "nope");
    const out = await run();
    assert.equal(out.errorClass, "local_worktree_missing");
  });

  it("fails with local_backend_unavailable when the CLI is not on PATH", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.PATH = binDir; // no codex binary here
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_backend_unavailable");
    assert.equal(out.transcript_entries, undefined, "pre-spawn failures have no model transcript");
  });

  it("fails with local_agent_failed when the CLI exits nonzero", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "3";
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_agent_failed");
    assert.equal(out.exitCode, 3);
    assert.equal(out.runtimeMetadata.cli_exit_code, "3");
  });

  it("fails closed when opencode emits a fatal stream error despite exit 0", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "opencode";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_OPENCODE_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_STREAM_ERROR = "model rejected request";
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_agent_failed");
    assert.equal(out.exitCode, 1);
    assert.equal(out.runtimeMetadata.cli_exit_code, "0");
    assert.equal(out.runtimeMetadata.stream_error, "model rejected request");
    assert.match(out.errorMessage, /model rejected request/);
  });

  it("accepts the typed needs_revision outcome without completing the Work Item", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_TASK_OUTCOME = JSON.stringify({
      version: 1,
      disposition: "needs_revision",
      summary: "The reviewed design references an API that no longer exists.",
    });
    const out = await run();
    assert.equal(out.status, "cancelled");
    assert.equal(out.exitCode, 0);
    assert.equal(out.errorClass, "task_needs_revision");
    assert.equal(out.runtimeMetadata.task_outcome, "needs_revision");
    assert.equal(out.runtimeMetadata.phase, "needs_revision");
    assert.match(out.errorMessage, /API that no longer exists/);
  });

  it("rejects a triaged outcome unless the trusted request selected bug-triage mode", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_TASK_OUTCOME = JSON.stringify({
      version: 1,
      disposition: "triaged",
      summary: "P2 parser regression reproduced.",
      priority: 2,
      labels: ["triaged", "parser"],
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.exitCode, 1);
    assert.equal(out.errorClass, "local_task_outcome_invalid");
    assert.match(out.errorMessage, /requires taskOutcomeMode="bug-triage"/);
    assert.equal(out.runtimeMetadata.task_outcome, undefined);
  });

  it("accepts only triaged disposition when the trusted request selected bug-triage mode", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_TASK_OUTCOME = JSON.stringify({
      version: 1,
      disposition: "needs_revision",
      summary: "Attempt to requeue a read-only bug investigation.",
    });
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-triage-no-requeue",
      task_id: "BUG-1",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { taskPrompt: "Triage the bug.", taskOutcomeMode: "bug-triage" },
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.exitCode, 1);
    assert.equal(out.errorClass, "local_task_outcome_invalid");
    assert.match(out.errorMessage, /disposition must be "triaged"/);
    assert.equal(out.runtimeMetadata.task_outcome, undefined);
  });

  for (const [name, edit, message] of [
    ["extra field", (outcome) => { outcome.status = "review"; }, /unsupported fields/],
    ["fractional priority", (outcome) => { outcome.priority = 1.5; }, /priority must be an integer/],
    ["out-of-range priority", (outcome) => { outcome.priority = 5; }, /priority must be an integer/],
    ["non-array labels", (outcome) => { outcome.labels = "triaged"; }, /labels must be an array/],
    ["too many labels", (outcome) => { outcome.labels = Array.from({ length: 8 }, (_, i) => "label-" + i); }, /at most 7/],
    ["duplicate labels", (outcome) => { outcome.labels = ["parser", "parser"]; }, /must be unique/],
    ["untrimmed label", (outcome) => { outcome.labels = [" triaged"]; }, /trimmed strings/],
    ["needs-revision routing label", (outcome) => { outcome.labels = ["needs-revision"]; }, /host-owned workflow labels/],
    ["review-cycle routing label", (outcome) => { outcome.labels = ["review-cycle:9"]; }, /host-owned workflow labels/],
    ["Loom-owned namespace", (outcome) => { outcome.labels = ["loom:quarantined"]; }, /host-owned workflow labels/],
    ["fixed host marker", (outcome) => { outcome.labels = ["triaged"]; }, /host-owned workflow labels/],
    ["normalization collision", (outcome) => { outcome.labels = ["Parser Bug", "parser-bug"]; }, /unique after safe normalization/],
    ["oversized summary", (outcome) => { outcome.summary = "x".repeat(2001); }, /summary must contain 1-2000/],
  ]) {
    it(`fails closed for triaged outcome with ${name}`, async () => {
      const outcome = {
        version: 1,
        disposition: "triaged",
        summary: "P2 parser regression reproduced.",
        priority: 2,
        labels: ["triaged", "parser"],
      };
      edit(outcome);
      process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
      process.env.LOOM_WORKTREE_PATH = worktree;
      process.env.LOOM_CODEX_BIN = fakeBin;
      process.env.FAKE_TASK_OUTCOME = JSON.stringify(outcome);
      process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
        task_run_id: "tr-triage-invalid",
        task_id: "BUG-1",
        runner: "local-task-runner",
        workspace_key: "ws",
        input: { taskPrompt: "Triage the bug.", taskOutcomeMode: "bug-triage" },
      });

      const out = await run();

      assert.equal(out.status, "failed");
      assert.equal(out.exitCode, 1);
      assert.equal(out.errorClass, "local_task_outcome_invalid");
      assert.match(out.errorMessage, message);
      assert.equal(out.runtimeMetadata.task_outcome, undefined);
    });
  }

  it("does not accept a needs_revision outcome from a backend that exits nonzero", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "7";
    process.env.FAKE_TASK_OUTCOME = JSON.stringify({
      version: 1,
      disposition: "needs_revision",
      summary: "marker written before the backend crashed",
    });
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.exitCode, 7);
    assert.equal(out.errorClass, "local_agent_failed");
    assert.equal(out.runtimeMetadata.task_outcome, undefined);
  });

  it("does not accept a needs_revision outcome from a fatal stream result", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "opencode";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_OPENCODE_BIN = fakeBin;
    process.env.FAKE_STREAM_ERROR = "provider rejected the turn";
    process.env.FAKE_TASK_OUTCOME = JSON.stringify({
      version: 1,
      disposition: "needs_revision",
      summary: "untrusted after fatal stream error",
    });
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_agent_failed");
    assert.equal(out.runtimeMetadata.task_outcome, undefined);
    assert.match(out.errorMessage, /provider rejected the turn/);
  });

  it("preserves a nonzero backend failure when the outcome file contains partial JSON", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "9";
    process.env.FAKE_TASK_OUTCOME = "{";

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.exitCode, 9);
    assert.equal(out.errorClass, "local_agent_failed");
    assert.equal(out.runtimeMetadata.phase, "local_agent_failed");
    assert.equal(out.runtimeMetadata.task_outcome, undefined);
    assert.match(out.errorMessage, /exited with code 9/);
  });

  it("preserves a fatal stream failure when the outcome file is malformed", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "opencode";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_OPENCODE_BIN = fakeBin;
    process.env.FAKE_STREAM_ERROR = "provider stream terminated";
    process.env.FAKE_TASK_OUTCOME = JSON.stringify({
      version: 1,
      disposition: "triaged",
      summary: "partial result",
      priority: 99,
      labels: [],
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.exitCode, 1);
    assert.equal(out.errorClass, "local_agent_failed");
    assert.equal(out.runtimeMetadata.phase, "local_agent_failed");
    assert.equal(out.runtimeMetadata.task_outcome, undefined);
    assert.match(out.errorMessage, /provider stream terminated/);
  });

  for (const [name, value] of [
    ["malformed JSON", "{"],
    ["unknown disposition", JSON.stringify({ version: 1, disposition: "complete", summary: "no" })],
    ["extra field", JSON.stringify({ version: 1, disposition: "needs_revision", summary: "reason", status: "open" })],
    ["empty summary", JSON.stringify({ version: 1, disposition: "needs_revision", summary: "" })],
  ]) {
    it(`fails closed for a ${name} task outcome`, async () => {
      process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
      process.env.LOOM_WORKTREE_PATH = worktree;
      process.env.LOOM_CODEX_BIN = fakeBin;
      process.env.FAKE_TASK_OUTCOME = value;
      const out = await run();
      assert.equal(out.status, "failed");
      assert.equal(out.exitCode, 1);
      assert.equal(out.errorClass, "local_task_outcome_invalid");
      assert.equal(out.runtimeMetadata.phase, "local_task_outcome_invalid");
    });
  }
});

describe("local-task-runner success", () => {
  it("appends the authoritative bug-triage contract and emits bounded string metadata", async () => {
    const promptFile = path.join(tmpRoot, "bug-triage-prompt.txt");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeStdinBin;
    process.env.LOOM_READ_ONLY = "1";
    process.env.FAKE_STDIN_FILE = promptFile;
    process.env.FAKE_TASK_OUTCOME = JSON.stringify({
      version: 1,
      disposition: "triaged",
      summary: "P4: no actionable defect was reproduced.",
      priority: 4,
      labels: ["Parser Regression", "café"],
    });
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-bug-triage",
      task_id: "BUG-1",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        taskPrompt: "Inspect the assigned bug and mutate it directly.",
        taskOutcomeMode: "bug-triage",
      },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    assert.equal(out.exitCode, 0);
    assert.equal(out.runtimeMetadata.task_outcome, "triaged");
    assert.equal(out.runtimeMetadata.triage_summary, "P4: no actionable defect was reproduced.");
    assert.equal(out.runtimeMetadata.triage_priority, "4");
    assert.equal(
      out.runtimeMetadata.triage_labels_json,
      JSON.stringify(["triaged", "triage:parser-regression", "triage:cafe"]),
    );
    const deliveredPrompt = fs.readFileSync(promptFile, "utf8");
    assert.ok(deliveredPrompt.indexOf("Inspect the assigned bug") < deliveredPrompt.indexOf("Authoritative Loom TaskRun bug-triage handoff"));
    assert.match(deliveredPrompt, /Do not call raw Loom\/Fleet HTTP\s+APIs/);
    assert.match(deliveredPrompt, /sole write allowed by this read-only run/);
    assert.match(deliveredPrompt, /LOOM_TASK_OUTCOME_FILE/);
    assert.match(deliveredPrompt, /safely namespaces accepted labels under\s+`triage:`/);
    assert.equal(out.runtimeMetadata.files_changed, "0");
  });

  it("runs the deterministic localdogfood executable with the role prompt on stdin", async () => {
    const promptFile = path.join(tmpRoot, "localdogfood-prompt.txt");
    process.env.LOOM_TASK_RUNNER_BACKEND = "localdogfood";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_LOCALDOGFOOD_BIN = fakeStdinBin;
    process.env.FAKE_STDIN_FILE = promptFile;
    process.env.FAKE_WRITE_FILE = "local-mode-agent-output.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-localdogfood",
      task_id: "T-localdogfood",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { taskPrompt: "LOOM_LOCALDOGFOOD_PHASE=task\nMake the deterministic fixture change." },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    assert.equal(out.exitCode, 0);
    assert.equal(out.runtimeMetadata.backend, "localdogfood");
    assert.equal(out.runtimeMetadata.runtime_strategy, "local-cli-localdogfood");
    assert.equal(
      fs.readFileSync(promptFile, "utf8"),
      "LOOM_LOCALDOGFOOD_PHASE=task\nMake the deterministic fixture change.",
    );
    assert.ok(out.patch.includes("local-mode-agent-output.txt"));
    assert.ok(out.transcript_entries.some((entry) => entry.role === "assistant"));
  });

  it("completes when the CLI exits 0 and captures the patch + transcript", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    // Bare name => the fake CLI writes into its cwd (the isolated worktree).
    process.env.FAKE_WRITE_FILE = "new-file.txt";

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.exitCode, 0);
    assert.equal(out.runtimeMetadata.task_runner, "local-task-runner");
    assert.equal(out.runtimeMetadata.runtime_strategy, "local-cli-codex");
    assert.equal(out.runtimeMetadata.backend, "codex");
    // The fake CLI created a new file: patch + files_changed must reflect it.
    assert.ok(out.patch.includes("new-file.txt"), "patch should include the new file");
    assert.equal(out.runtimeMetadata.files_changed, "1");
    assert.ok(Array.isArray(out.transcript_entries) && out.transcript_entries.length >= 1);
    assert.ok(out.transcript_entries.some((e) => e.role === "assistant" && e.text === "did the work"));
  });

  it("surfaces top-level token usage on the completed result (Go bridge ingests these)", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_USAGE_TOKENS = "1";

    const out = await run();
    assert.equal(out.status, "completed");
    // The token counts the parser computed must now ride at the TOP LEVEL (not just
    // embedded in the transcript), where internal/driver/task_bridge.go reads them
    // into the fleet-db TaskRun. Before this fix local-CLI runs reported zero usage.
    assert.equal(out.input_tokens, 123);
    assert.equal(out.output_tokens, 45);
    assert.equal(out.cache_read_tokens, 6);
    // codex reports NO cost in its CLI output, so we never fabricate one: tokens
    // ride at the top level but estimated_cost_usd is left unset (cost is sourced
    // only from the backend CLI, never a price-table estimate).
    assert.ok(out.estimated_cost_usd == null, "codex must not get a fabricated cost");
  });

  it("signals backend activity on stderr under LOOM_TASK_RUNNER_STREAM_STDERR (daemon-leaf watchdog feed)", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.LOOM_TASK_RUNNER_STREAM_STDERR = "1";
    const chunks = [];
    const orig = process.stderr.write.bind(process.stderr);
    process.stderr.write = (c) => { chunks.push(typeof c === "string" ? c : c.toString()); return true; };
    let out;
    try {
      out = await run();
    } finally {
      process.stderr.write = orig;
    }
    assert.equal(out.status, "completed");
    // The watchdog needs activity, not unredacted backend bytes. A fixed signal
    // cannot leak a credential that spans arbitrary stream chunk boundaries.
    assert.ok(chunks.join("").includes("backend activity"), "backend activity must reach stderr when streaming is enabled");
    assert.ok(!chunks.join("").includes("did the work"), "raw backend output must never be teed to stderr");
  });

  it("redacts an exact TaskRun lease canary from logs, transcript, metadata, and live stderr", async () => {
    const canary = "lease-canary-low-entropy-00000000";
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_ECHO_SECRET = canary;
    process.env.LOOM_TASK_RUN_LEASE_TOKEN = canary;
    process.env.LOOM_TASK_RUNNER_STREAM_STDERR = "1";
    const chunks = [];
    const orig = process.stderr.write.bind(process.stderr);
    process.stderr.write = (c) => { chunks.push(typeof c === "string" ? c : c.toString()); return true; };
    let out;
    try {
      out = await run();
    } finally {
      process.stderr.write = orig;
    }

    assert.equal(out.status, "completed");
    assert.ok(!JSON.stringify(out).includes(canary), "persisted TaskRun result must not contain the raw lease token");
    assert.ok(out.logs.includes("REDACTED") || out.logs.includes("***"), "redacted diagnostics should remain useful");
    assert.ok(!chunks.join("").includes(canary), "serve-facing live stderr must not contain the raw lease token");
    const secretTool = out.transcript_entries.find((entry) => entry.tool_name === "shell" && entry.tool_input?.command?.includes("***"));
    assert.ok(secretTool, "nested transcript tool input must retain a redacted command");
  });

  it("fails closed before persisting or publishing a patch containing an inherited credential", async () => {
    const canary = "lease-canary-patch-000000000000";
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_SECRET_FILE = "credential.txt";
    process.env.LOOM_TASK_RUN_LEASE_TOKEN = canary;

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_patch_contains_credential");
    assert.equal(out.patch, undefined, "rejected credential patch must not be persisted");
    assert.ok(!JSON.stringify(out).includes(canary), "rejected result must not echo the credential");
    assert.ok(
      out.transcript_entries.some((entry) => entry.role === "assistant" && entry.text === "did the work"),
      "post-model patch rejection must preserve the redacted model transcript",
    );
  });

  it("redacts a lease canary echoed through a terminal stream error", async () => {
    const canary = "lease-canary-error-000000000000";
    process.env.LOOM_TASK_RUNNER_BACKEND = "opencode";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_OPENCODE_BIN = fakeBin;
    process.env.FAKE_STREAM_ERROR = "provider echoed " + canary;
    process.env.LOOM_TASK_RUN_LEASE_TOKEN = canary;

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_agent_failed");
    assert.ok(!JSON.stringify(out).includes(canary), "error details and metadata must not contain the raw lease token");
    assert.ok(JSON.stringify(out).includes("***") || JSON.stringify(out).includes("REDACTED"));
  });

  it("does NOT tee backend output to stderr without the stream flag (driver path unaffected)", async () => {
    delete process.env.LOOM_TASK_RUNNER_STREAM_STDERR;
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    const chunks = [];
    const orig = process.stderr.write.bind(process.stderr);
    process.stderr.write = (c) => { chunks.push(typeof c === "string" ? c : c.toString()); return true; };
    let out;
    try {
      out = await run();
    } finally {
      process.stderr.write = orig;
    }
    assert.equal(out.status, "completed");
    assert.ok(!chunks.join("").includes("did the work"), "backend output must NOT be teed to stderr without the flag");
  });

  it("falls back to a minimal transcript when stream-json yields no recognized entries (gemini)", async () => {
    // gemini is a stream-json backend now, but this fake CLI emits codex-shaped
    // events the gemini parser does not recognize -> graceful minimal fallback
    // (session_meta + user prompt + assistant stdout tail), so evidence is preserved.
    process.env.LOOM_TASK_RUNNER_BACKEND = "gemini";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_GEMINI_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.runtime_strategy, "local-cli-gemini");
    assert.equal(out.transcript_entries.length, 3);
    assert.equal(out.transcript_entries[1].role, "user");
  });

  it("delivers the prompt to codex over stdin (and closes stdin)", async () => {
    // Proves execBackend writes/closes the child's stdin and that codex is now a
    // stdin-prompt backend: the fake CLI reads stdin to EOF and records it. If
    // stdin were left open, the read would never end and this would hang.
    const stdinFile = path.join(tmpRoot, "stdin-capture.txt");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeStdinBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_STDIN_FILE = stdinFile;
    process.env.FAKE_WRITE_FILE = "stdin-marker.txt";

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.exitCode, 0);
    // The runner must have written the prompt to the child's stdin.
    assert.ok(fs.existsSync(stdinFile), "fake codex CLI should have read its stdin to EOF");
    const captured = fs.readFileSync(stdinFile, "utf8");
    assert.ok(captured.length > 0, "prompt delivered over stdin should be non-empty");
    assert.ok(
      captured.includes("implementing one child task"),
      "stdin should carry the built prompt",
    );
    // And the codex item.completed transcript still flows through.
    assert.ok(out.transcript_entries.some((e) => e.role === "assistant" && e.text === "read the stdin"));
  });

  it("uses LOOM_TASK_RUN_PROMPT verbatim over buildPrompt (daemon-leaf prompt fidelity)", async () => {
    const stdinFile = path.join(tmpRoot, "stdin-override.txt");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeStdinBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_STDIN_FILE = stdinFile;
    process.env.LOOM_TASK_RUN_PROMPT = "OVERRIDE-PROMPT-XYZ from the daemon leaf";

    const out = await run();
    assert.equal(out.status, "completed");
    const captured = fs.readFileSync(stdinFile, "utf8");
    assert.ok(captured.includes("OVERRIDE-PROMPT-XYZ"), "stdin should carry the override prompt verbatim");
    assert.ok(!captured.includes("implementing one child task"), "buildPrompt must be bypassed when the override is set");
  });

  it("never emits a synthetic 'Completed by the built-in local task runner.' transcript", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    const out = await run();
    const text = JSON.stringify(out);
    assert.ok(!text.includes("Completed by the built-in local task runner"));
  });
});

describe("local-task-runner isolated worktree", () => {
  it("fails closed when the Git binary patch cannot be captured completely", async () => {
    installGitFaultWrapper();
    process.env.FAKE_GIT_FAIL_BINARY_DIFF = "1";
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "capture-failure.txt";

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_patch_capture_failed");
    assert.equal(out.patch, undefined, "an incompletely inspected patch must not be persisted");
    assert.ok(
      out.transcript_entries.some((entry) => entry.role === "assistant" && entry.text === "did the work"),
      "post-model patch capture failure must preserve model evidence",
    );
  });

  it("runs the CLI in an isolated worktree, leaving the host clean and returning base_ref=HEAD", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    // Bare name => the fake CLI writes into its cwd, which must be the isolated
    // worktree, NOT the host worktree.
    process.env.FAKE_WRITE_FILE = "isolated-file.txt";

    const head = execFileSync("git", ["rev-parse", "HEAD"], { cwd: worktree }).toString().trim();

    const out = await run();
    assert.equal(out.status, "completed");
    // Top-level base_ref must equal the host repo HEAD sha so the host-bridge
    // can patch-back onto the (clean) host worktree.
    assert.equal(out.base_ref, head, "top-level base_ref should equal host HEAD");
    assert.equal(out.patch_base_ref, head);
    assert.equal(out.runtimeMetadata.base_ref, head);
    assert.ok(
      out.runtimeMetadata.exec_worktree_path.startsWith(path.dirname(worktree) + path.sep),
      "isolated worktree should be created as a sibling of the host repo",
    );
    assert.notEqual(out.runtimeMetadata.exec_worktree_path, worktree);

    // The host worktree must be CLEAN — the file was created in the isolated
    // worktree, not the host. (git add -N runs in the isolated worktree only.)
    const status = execFileSync("git", ["status", "--porcelain"], { cwd: worktree }).toString();
    assert.equal(status.trim(), "", "host worktree should be left clean");
    assert.ok(!fs.existsSync(path.join(worktree, "isolated-file.txt")), "the new file must NOT exist in the host worktree");

    // The isolated worktree must have been cleaned up: only the host remains.
    const worktrees = execFileSync("git", ["worktree", "list"], { cwd: worktree })
      .toString()
      .split("\n")
      .filter((line) => line.trim().length > 0);
    assert.equal(worktrees.length, 1, "isolated worktree should be removed (only host remains)");

    // The returned patch (captured in the isolated worktree) mentions the file.
    assert.ok(out.patch.includes("isolated-file.txt"), "patch should reference the file created in the isolated worktree");
  });

  it("falls back to in-place execution with empty base_ref when the worktree is not a git repo", async () => {
    const nonGit = path.join(tmpRoot, "non-git");
    fs.mkdirSync(nonGit, { recursive: true });

    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = nonGit;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "in-place.txt";

    const out = await run();
    assert.equal(out.status, "completed", "non-git fallback should still complete");
    // No git HEAD => in-place execution, empty base_ref (patch-back not possible).
    assert.equal(out.base_ref, "", "base_ref should be empty in the non-git fallback");
    assert.equal(out.patch_base_ref, "");
    // The CLI ran in place: exec_worktree_path is the host (non-git) directory,
    // and the file was written there.
    assert.equal(out.runtimeMetadata.exec_worktree_path, nonGit);
    assert.ok(fs.existsSync(path.join(nonGit, "in-place.txt")), "in-place run should write into the host directory");
  });
});

describe("local-task-runner pull-request delivery gating", () => {
  // A sanitized bin dir that exposes the fake backend + real git but NOT gh, so
  // resolveGitHubToken's `gh auth token` fallback cannot succeed even on a host
  // where gh is logged in. Combined with cleared GITHUB_TOKEN/GH_TOKEN, this
  // forces the no-credential path deterministically.
  function sanitizedBinDir() {
    const dir = fs.mkdtempSync(path.join(tmpRoot, "sanitized-bin-"));
    // node is needed to run the fake CLI shebang interpreter; symlink the real
    // node and git so the runner's spawns resolve, but never gh.
    for (const name of ["git", "node"]) {
      const real = execFileSync("bash", ["-lc", `command -v ${name}`]).toString().trim();
      if (real) {
        fs.symlinkSync(real, path.join(dir, name));
      }
    }
    return dir;
  }

  function createBareOrigin(remoteName = "origin") {
    const origin = path.join(tmpRoot, remoteName + ".git");
    execFileSync("git", ["init", "-q", "--bare", origin]);
    execFileSync("git", ["remote", "add", "origin", origin], { cwd: worktree });
    return origin;
  }

  function refSha(origin, ref) {
    return execFileSync("git", ["--git-dir", origin, "rev-parse", "--verify", ref]).toString().trim();
  }

  function refExists(origin, ref) {
    try {
      execFileSync("git", ["--git-dir", origin, "show-ref", "--verify", "--quiet", ref]);
      return true;
    } catch {
      return false;
    }
  }

  it("default (no openPullRequest) returns a top-level patch + base_ref and delivery=patch_back", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "patch-back.txt";
    // No openPullRequest in the request input => patch-back path.
    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "patch_back");
    assert.ok(typeof out.patch === "string" && out.patch.includes("patch-back.txt"), "patch-back path must return a top-level patch");
    assert.ok(out.base_ref && out.base_ref.length > 0, "patch-back path must carry base_ref for host-bridge patch-back");
    assert.equal(out.patch_base_ref, out.base_ref);
    // No PR metadata in the default path.
    assert.equal(out.runtimeMetadata.github_pr_url, undefined);
  });

  it("deliveryMode=local-branch pushes loom/<task> and returns exact review evidence", async () => {
    const origin = createBareOrigin();
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "local-branch.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-local",
      task_id: "T-LOCAL",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Local branch thing", deliveryMode: "local-branch" },
    });

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "local_branch");
    assert.equal(out.runtimeMetadata.local_branch, "loom/T-LOCAL");
    assert.equal(out.runtimeMetadata.head_sha, refSha(origin, "refs/heads/loom/T-LOCAL"));
    assert.ok(out.patch.includes("local-branch.txt"), "published local branch must retain its exact patch evidence");
    assert.ok(out.base_ref && out.base_ref.length > 0, "published evidence must retain its comparison base");
    assert.equal(out.patch_base_ref, out.base_ref);
    const files = execFileSync("git", ["--git-dir", origin, "ls-tree", "-r", "--name-only", out.runtimeMetadata.head_sha]).toString();
    assert.ok(files.includes("local-branch.txt"), "pushed branch should contain the backend change");
  });

  it("uses the host-admitted filesystem remote when the isolated repo has no origin", async () => {
    const admittedRemote = path.join(tmpRoot, "admitted-source.git");
    execFileSync("git", ["init", "-q", "--bare", admittedRemote]);
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_TASK_RUN_REPOSITORY_REMOTE_URL = admittedRemote;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "remote-less-local-branch.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-admitted-local",
      task_id: "T-ADMITTED-LOCAL",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        title: "Remote-less local branch",
        deliveryMode: "local-branch",
        requireLocalBranchDelivery: true,
      },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "local_branch");
    assert.equal(out.runtimeMetadata.local_branch, "loom/T-ADMITTED-LOCAL");
    assert.equal(
      out.runtimeMetadata.head_sha,
      refSha(admittedRemote, "refs/heads/loom/T-ADMITTED-LOCAL"),
    );
    assert.equal(
      execFileSync("git", ["remote"], { cwd: worktree }).toString().trim(),
      "",
      "trusted delivery metadata must not mutate the task worktree's remotes",
    );
  });

  it("resumes a stamped local review branch and preserves its existing implementation", async () => {
    const origin = createBareOrigin("review-resume-origin");
    const hostBase = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    const branch = "loom/T-REVIEW";
    fs.writeFileSync(path.join(worktree, "review-existing-code.txt"), "existing implementation\n");
    execFileSync("git", ["add", "review-existing-code.txt"], { cwd: worktree });
    execFileSync("git", ["commit", "-q", "-m", "existing implementation"], { cwd: worktree });
    const reviewHead = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    execFileSync("git", ["push", "-q", "origin", `${reviewHead}:refs/heads/${branch}`], { cwd: worktree });
    execFileSync("git", ["reset", "--hard", "-q", hostBase], { cwd: worktree });

    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "review-docs.md";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-review-resume",
      task_id: "T-REVIEW",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        title: "Document the reviewed implementation",
        deliveryMode: "local-branch",
        requireLocalBranchDelivery: true,
        localBranchName: branch,
        localBranchBaseRef: reviewHead,
      },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    assert.equal(out.base_ref, reviewHead, "review changes must be based on the stamped branch head");
    assert.equal(out.runtimeMetadata.delivery, "local_branch");
    assert.equal(out.runtimeMetadata.local_branch, branch);
    const published = refSha(origin, `refs/heads/${branch}`);
    assert.equal(out.runtimeMetadata.head_sha, published);
    assert.equal(
      execFileSync("git", ["--git-dir", origin, "rev-list", "--count", `${reviewHead}..${published}`]).toString().trim(),
      "1",
      "documentation delivery must extend the reviewed implementation",
    );
    const files = execFileSync("git", ["--git-dir", origin, "ls-tree", "-r", "--name-only", published]).toString();
    assert.ok(files.includes("review-existing-code.txt"), "the prior reviewed implementation must be preserved");
    assert.ok(files.includes("review-docs.md"), "the documentation agent change must be published");
    assert.ok(out.patch.includes("review-docs.md"));
  });

  it("resumes and extends a stamped branch through the host-admitted remote without origin", async () => {
    const admittedRemote = path.join(tmpRoot, "admitted-review-source.git");
    execFileSync("git", ["init", "-q", "--bare", admittedRemote]);
    const hostBase = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    const branch = "loom/T-ADMITTED-REVIEW";
    fs.writeFileSync(path.join(worktree, "reviewed-code.txt"), "reviewed implementation\n");
    execFileSync("git", ["add", "reviewed-code.txt"], { cwd: worktree });
    execFileSync("git", ["commit", "-q", "-m", "reviewed implementation"], { cwd: worktree });
    const reviewHead = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    execFileSync("git", ["push", "-q", admittedRemote, `${reviewHead}:refs/heads/${branch}`], { cwd: worktree });
    execFileSync("git", ["reset", "--hard", "-q", hostBase], { cwd: worktree });

    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_TASK_RUN_REPOSITORY_REMOTE_URL = admittedRemote;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "review-docs.md";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-admitted-review",
      task_id: "T-ADMITTED-REVIEW",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        title: "Document admitted reviewed implementation",
        deliveryMode: "local-branch",
        requireLocalBranchDelivery: true,
        localBranchName: branch,
        localBranchBaseRef: reviewHead,
      },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    assert.equal(out.base_ref, reviewHead);
    assert.equal(out.runtimeMetadata.delivery, "local_branch");
    const published = refSha(admittedRemote, `refs/heads/${branch}`);
    assert.equal(out.runtimeMetadata.head_sha, published);
    const files = execFileSync("git", ["--git-dir", admittedRemote, "ls-tree", "-r", "--name-only", published]).toString();
    assert.ok(files.includes("reviewed-code.txt"));
    assert.ok(files.includes("review-docs.md"));
    assert.equal(execFileSync("git", ["remote"], { cwd: worktree }).toString().trim(), "");
  });

  it("fails before backend execution when a stamped review branch has drifted", async () => {
    const origin = createBareOrigin("review-drift-origin");
    const hostBase = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    const branch = "loom/T-DRIFT";
    fs.writeFileSync(path.join(worktree, "first.txt"), "first\n");
    execFileSync("git", ["add", "first.txt"], { cwd: worktree });
    execFileSync("git", ["commit", "-q", "-m", "first review head"], { cwd: worktree });
    const stampedHead = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    fs.writeFileSync(path.join(worktree, "second.txt"), "second\n");
    execFileSync("git", ["add", "second.txt"], { cwd: worktree });
    execFileSync("git", ["commit", "-q", "-m", "moved review head"], { cwd: worktree });
    const movedHead = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    execFileSync("git", ["push", "-q", "origin", `${movedHead}:refs/heads/${branch}`], { cwd: worktree });
    execFileSync("git", ["reset", "--hard", "-q", hostBase], { cwd: worktree });
    const backendMarker = path.join(tmpRoot, "drift-backend-ran.txt");

    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = backendMarker;
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-review-drift",
      task_id: "T-DRIFT",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        title: "Do not overwrite a moved review branch",
        deliveryMode: "local-branch",
        requireLocalBranchDelivery: true,
        localBranchName: branch,
        localBranchBaseRef: stampedHead,
      },
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_branch_base_drift");
    assert.equal(refSha(origin, `refs/heads/${branch}`), movedHead, "the moved branch must remain untouched");
    assert.equal(fs.existsSync(backendMarker), false, "the backend must not run after a resume fence conflict");
  });

  it("does not overwrite a stamped review branch that advances after preflight", async () => {
    const origin = createBareOrigin("review-race-origin");
    const hostBase = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    const branch = "loom/T-REVIEW-RACE";
    fs.writeFileSync(path.join(worktree, "review-base.txt"), "review base\n");
    execFileSync("git", ["add", "review-base.txt"], { cwd: worktree });
    execFileSync("git", ["commit", "-q", "-m", "review race base"], { cwd: worktree });
    const stampedHead = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    execFileSync("git", ["push", "-q", "origin", `${stampedHead}:refs/heads/${branch}`], { cwd: worktree });
    execFileSync("git", ["reset", "--hard", "-q", hostBase], { cwd: worktree });

    const remoteHeadMarker = path.join(tmpRoot, "concurrent-remote-head.txt");
    installGitFaultWrapper();
    process.env.FAKE_GIT_ADVANCE_REMOTE_BEFORE_PUSH = "1";
    process.env.FAKE_GIT_REMOTE_BARE = origin;
    process.env.FAKE_GIT_REMOTE_BRANCH = branch;
    process.env.FAKE_GIT_REMOTE_HEAD_MARKER = remoteHeadMarker;
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "review-after-preflight.md";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-review-race",
      task_id: "T-REVIEW-RACE",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        title: "Do not overwrite concurrent review work",
        deliveryMode: "local-branch",
        requireLocalBranchDelivery: true,
        localBranchName: branch,
        localBranchBaseRef: stampedHead,
      },
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_branch_push_failed");
    const concurrentHead = fs.readFileSync(remoteHeadMarker, "utf8").trim();
    assert.notEqual(concurrentHead, stampedHead);
    assert.equal(
      refSha(origin, `refs/heads/${branch}`),
      concurrentHead,
      "the exact force-with-lease must preserve the concurrent remote head",
    );
  });

  it("fails before backend execution when required local-branch delivery has a network origin", async () => {
    execFileSync("git", ["remote", "add", "origin", "https://github.com/owner/repo.git"], { cwd: worktree });
    const backendMarker = path.join(tmpRoot, "network-origin-backend-ran.txt");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = backendMarker;
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-review-network-origin",
      task_id: "T-NETWORK",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        title: "Do not silently fall back",
        deliveryMode: "local-branch",
        requireLocalBranchDelivery: true,
      },
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_branch_origin_unsupported");
    assert.equal(fs.existsSync(backendMarker), false, "the backend must not run without required delivery");
  });

  it("deliveryMode=local-branch reuses backend output that is already committed", async () => {
    const origin = createBareOrigin("backend-committed-origin");
    const base = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "backend-committed.txt";
    process.env.FAKE_COMMIT_CHANGES = "1";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-backend-committed",
      task_id: "T-BACKEND-COMMITTED",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Backend committed", deliveryMode: "local-branch" },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    const published = refSha(origin, "refs/heads/loom/T-BACKEND-COMMITTED");
    assert.equal(out.runtimeMetadata.head_sha, published);
    assert.equal(execFileSync("git", ["--git-dir", origin, "rev-list", "--count", `${base}..${published}`]).toString().trim(), "1", "runner must reuse the backend commit instead of creating another commit");
    assert.equal(execFileSync("git", ["--git-dir", origin, "show", "-s", "--format=%s", published]).toString().trim(), "fake backend committed");
  });

  it("publishes the scanned immutable commit when HEAD moves after scanning", async () => {
    const origin = createBareOrigin("immutable-head-origin");
    const marker = path.join(tmpRoot, "moved-head.txt");
    installGitFaultWrapper();
    process.env.FAKE_GIT_MOVE_HEAD_BEFORE_PUSH = "1";
    process.env.FAKE_GIT_MOVE_HEAD_MARKER = marker;
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "scanned-change.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-immutable-head",
      task_id: "T-IMMUTABLE-HEAD",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Immutable head", deliveryMode: "local-branch" },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    const movedHead = fs.readFileSync(marker, "utf8").trim();
    const published = refSha(origin, "refs/heads/loom/T-IMMUTABLE-HEAD");
    assert.notEqual(movedHead, published, "fault wrapper must advance local HEAD after the credential scan");
    assert.equal(published, out.runtimeMetadata.head_sha, "remote must receive the exact scanned SHA returned by secureCommitForDelivery");
    const files = execFileSync("git", ["--git-dir", origin, "ls-tree", "-r", "--name-only", published]).toString();
    assert.ok(files.includes("scanned-change.txt"));
    assert.ok(!files.includes("post-scan-head-move.txt"), "post-scan commit must not be published");
  });

  it("rejects a credential written after initial capture before local-branch publication", async () => {
    const origin = createBareOrigin("delayed-credential-origin");
    installGitFaultWrapper();
    const canary = "lease-canary-delayed-publication-00000000";
    process.env.FAKE_GIT_WRITE_SECRET_AFTER_CHECKOUT = "1";
    process.env.LOOM_TASK_RUN_LEASE_TOKEN = canary;
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "benign-before-delivery.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-delayed-credential",
      task_id: "T-DELAYED-CREDENTIAL",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Delayed credential", deliveryMode: "local-branch" },
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_patch_contains_credential");
    assert.ok(!JSON.stringify(out).includes(canary), "the rejected result must not echo the credential");
    assert.equal(refExists(origin, "refs/heads/loom/T-DELAYED-CREDENTIAL"), false, "no credential-bearing branch may be published");
  });

  it("filesystem origin without deliveryMode stays on patch-back", async () => {
    createBareOrigin("explicit-only-origin");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "explicit-only-patch.txt";

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "patch_back");
    assert.ok(out.patch.includes("explicit-only-patch.txt"), "filesystem origins require explicit local-branch deliveryMode");
    assert.equal(out.runtimeMetadata.local_branch, undefined);
  });

  it("deliveryMode=local-branch with no changes skips the push and returns patch-back shape", async () => {
    const origin = createBareOrigin("empty-origin");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    delete process.env.FAKE_WRITE_FILE;
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-empty-local",
      task_id: "T-EMPTY",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Empty local branch thing", deliveryMode: "local-branch" },
    });

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "patch_back");
    assert.equal(out.runtimeMetadata.files_changed, "0");
    assert.equal(out.patch, "");
    assert.ok(out.base_ref && out.base_ref.length > 0);
    assert.equal(refExists(origin, "refs/heads/loom/T-EMPTY"), false);
  });

  it("required local-branch delivery fails when the backend produces no changes", async () => {
    const origin = createBareOrigin("required-empty-origin");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    delete process.env.FAKE_WRITE_FILE;
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-required-empty-local",
      task_id: "T-REQUIRED-EMPTY",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: {
        title: "Required empty local branch",
        deliveryMode: "local-branch",
        requireLocalBranchDelivery: true,
      },
    });

    const out = await run();

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_branch_delivery_missing");
    assert.equal(refExists(origin, "refs/heads/loom/T-REQUIRED-EMPTY"), false);
  });

  it("deliveryMode=local-branch force-pushes rework to the same task branch", async () => {
    const origin = createBareOrigin("rework-origin");
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";

    process.env.FAKE_WRITE_FILE = "first-rework.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-rework-1",
      task_id: "T-REWORK",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "First rework", deliveryMode: "local-branch" },
    });
    const first = await run();
    assert.equal(first.status, "completed");
    assert.equal(first.runtimeMetadata.delivery, "local_branch");
    assert.equal(first.runtimeMetadata.head_sha, refSha(origin, "refs/heads/loom/T-REWORK"));

    process.env.FAKE_WRITE_FILE = "second-rework.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-rework-2",
      task_id: "T-REWORK",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Second rework", deliveryMode: "local-branch" },
    });
    const second = await run();
    assert.equal(second.status, "completed");
    assert.equal(second.runtimeMetadata.delivery, "local_branch");
    assert.equal(second.runtimeMetadata.local_branch, "loom/T-REWORK");
    assert.equal(second.runtimeMetadata.head_sha, refSha(origin, "refs/heads/loom/T-REWORK"));
    assert.notEqual(second.runtimeMetadata.head_sha, first.runtimeMetadata.head_sha, "rework should replace the previous branch head");
    const files = execFileSync("git", ["--git-dir", origin, "ls-tree", "-r", "--name-only", second.runtimeMetadata.head_sha]).toString();
    assert.ok(files.includes("second-rework.txt"), "second push should win");
    assert.ok(!files.includes("first-rework.txt"), "old divergent branch content should be replaced");
  });

  it("local-branch delivery fails closed when pushing to the filesystem origin fails", async () => {
    const missingOrigin = path.join(tmpRoot, "missing-origin.git");
    execFileSync("git", ["remote", "add", "origin", missingOrigin], { cwd: worktree });
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "unpushable.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-local-fail",
      task_id: "T-LOCAL-FAIL",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Unpushable", deliveryMode: "local-branch" },
    });

    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "local_branch_push_failed");
    assert.equal(out.exitCode, 1);
    assert.ok(
      out.transcript_entries.some((entry) => entry.role === "assistant" && entry.text === "did the work"),
      "post-model local delivery failure must preserve model evidence",
    );
  });

  it("deliveryMode=local-branch never activates for GitHub/http origins (keeps patch-back)", async () => {
    execFileSync("git", ["remote", "add", "origin", "https://github.com/owner/repo.git"], { cwd: worktree });
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "github-origin-patch.txt";
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-github-origin",
      task_id: "T-GH",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "GitHub origin guard", deliveryMode: "local-branch" },
    });

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "patch_back");
    assert.ok(out.patch.includes("github-origin-patch.txt"), "GitHub origins must keep patch-back behavior");
    assert.ok(out.base_ref && out.base_ref.length > 0);
    assert.equal(out.runtimeMetadata.local_branch, undefined);
  });

  it("openPullRequest with no credential and gh unavailable fails closed (github_credentials_missing)", async () => {
    const binDirNoGh = sanitizedBinDir();
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "pr-change.txt";
    // Ensure no env credential and no gh on PATH (so `gh auth token` ENOENTs).
    delete process.env.GITHUB_TOKEN;
    delete process.env.GH_TOKEN;
    process.env.PATH = binDirNoGh;
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-pr",
      task_id: "T-PR",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Do the thing", openPullRequest: true, githubRepo: "owner/repo" },
    });

    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "github_credentials_missing");
    assert.equal(out.exitCode, 1);
    assert.ok(
      out.transcript_entries.some((entry) => entry.role === "assistant" && entry.text === "did the work"),
      "post-model PR delivery failure must preserve model evidence",
    );
  });

  it("openPullRequest=true but the agent produced no changes skips the PR (delivery=pull_request_skipped_no_changes)", async () => {
    // No FAKE_WRITE_FILE => the fake CLI changes nothing, so filesChanged === 0
    // and PR delivery is short-circuited BEFORE any credential/network work.
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    delete process.env.GITHUB_TOKEN;
    delete process.env.GH_TOKEN;
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-pr2",
      task_id: "T-PR2",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Do the thing", openPullRequest: true, githubRepo: "owner/repo" },
    });

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "pull_request_skipped_no_changes");
    // No changes => no top-level patch content, no PR url.
    assert.equal(out.runtimeMetadata.github_pr_url, undefined);
  });

  it("stacked mode with no credential and changes fails closed (github_credentials_missing)", async () => {
    const binDirNoGh = sanitizedBinDir();
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    process.env.FAKE_WRITE_FILE = "stack-change.txt";
    delete process.env.GITHUB_TOKEN;
    delete process.env.GH_TOKEN;
    process.env.PATH = binDirNoGh;
    // Stacked signal set by the host bridge.
    process.env.LOOM_TASK_RUN_STACKED = "1";
    process.env.LOOM_TASK_RUN_STACK_ID = "epic:E";
    process.env.LOOM_TASK_RUN_OUTPUT_BRANCH = "loom/stack/epic:E/T-S";
    process.env.LOOM_TASK_RUN_BASE_REF = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-stack",
      task_id: "T-S",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Stacked thing", stackedPullRequests: true, githubRepo: "owner/repo" },
    });

    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "github_credentials_missing");
    assert.equal(out.exitCode, 1);
    assert.ok(
      out.transcript_entries.some((entry) => entry.role === "assistant" && entry.text === "did the work"),
      "post-model stacked delivery failure must preserve model evidence",
    );
  });

  it("stacked mode with no changes records an empty unit (no branch pushed)", async () => {
    // No FAKE_WRITE_FILE => filesChanged === 0 => the empty unit is recorded and
    // no push/credential work happens. The host finalize barrier maps this to
    // NodeState=empty (decision (a): the dependent slides past it).
    process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_CODEX_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    delete process.env.GITHUB_TOKEN;
    delete process.env.GH_TOKEN;
    process.env.LOOM_TASK_RUN_STACKED = "1";
    process.env.LOOM_TASK_RUN_OUTPUT_BRANCH = "loom/stack/epic:E/T-S2";
    process.env.LOOM_TASK_RUN_BASE_REF = execFileSync("git", ["-C", worktree, "rev-parse", "HEAD"]).toString().trim();
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "tr-stack2",
      task_id: "T-S2",
      runner: "local-task-runner",
      workspace_key: "ws",
      input: { title: "Stacked empty", stackedPullRequests: true, githubRepo: "owner/repo" },
    });

    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.delivery, "pull_request_skipped_no_changes");
    // Stacked mode runs in place => no top-level patch (patch-back skipped).
    assert.equal(out.patch, undefined);
    assert.equal(out.runtimeMetadata.github_branch, undefined);
  });
  // The actual canonical-branch push (commit in place → push loom/stack/<stack>/<task>,
  // no PR) is Stage 3's LIVE verify bar (2-task local epic → 2 branches on origin,
  // B based on A) — it needs a real GitHub origin, so it is exercised via the
  // aether-test-framework local-mode run, not a unit test.
});
