import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { after, afterEach, before, beforeEach, describe, it } from "node:test";

import {
  backendArgs,
  parseNumstat,
  parseRepoSlug,
  resolveBackend,
  resolveBinary,
  run,
  scrubToken,
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
if (process.env.FAKE_STREAM_ERROR) {
  process.stdout.write(JSON.stringify({ type: "error", error: { message: process.env.FAKE_STREAM_ERROR } }) + "\\n");
  process.exit(exit);
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
  "LOOM_TASK_RUN_REQUEST_JSON",
  "FAKE_EXIT_CODE",
  "FAKE_WRITE_FILE",
  "FAKE_STREAM_ERROR",
  "FAKE_STDIN_FILE",
  "FAKE_USAGE_TOKENS",
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
  "LOOM_COST_PER_MTOK_INPUT",
  "LOOM_COST_PER_MTOK_OUTPUT",
  "LOOM_TASK_RUNNER_STREAM_STDERR",
  "LOOM_TASK_RUN_PROMPT",
  "LOOM_TASK_RUN_API_URL",
  "LOOM_WORKSPACE",
  "LOOM_TASK_RUN_ID",
  "LOOM_TASK_ID",
  "LOOM_TASK_RUN_NODE_ID",
  "LOOM_TASK_RUN_LEASE_ID",
  "LOOM_TASK_RUN_LEASE_TOKEN",
  "LOOM_TASK_RUN_FENCING_TOKEN",
];

// PATH is mutated by some tests; save/restore it separately so git stays callable.
let savedPath;
let taskRunAPIURL;
let savedFetch;

before(() => {
  savedFetch = globalThis.fetch;
  globalThis.fetch = async (url, init = {}) => {
    const requestURL = new URL(url);
    const raw = String(init.body || "");
    let body = {};
    try {
      body = raw ? JSON.parse(raw) : {};
    } catch {
      // Transcript uploads are NDJSON rather than a single JSON object.
    }
    const reply = (payload) => {
      return new Response(JSON.stringify(payload), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    };
    if (requestURL.pathname.endsWith("/task-get")) return reply({ task: { id: "T-1" } });
    if (requestURL.pathname.endsWith("/session-open")) return reply({ sessionId: "tr-1-a1-agent", attempt: 1 });
    if (requestURL.pathname.endsWith("/artifact-declare")) return reply({ artifactId: body.artifactId || "transcript-tr-1-a1-agent", type: "agent-transcript", durableStatus: "declared" });
    if (requestURL.pathname.includes("/artifacts/") && requestURL.pathname.endsWith("/content")) return reply({ artifactId: "transcript-tr-1-a1-agent", durableStatus: "uploaded" });
    if (requestURL.pathname.endsWith("/artifact-finalize")) return reply({ artifactId: body.artifactId || "transcript-tr-1-a1-agent", durableStatus: "finalized" });
    if (requestURL.pathname.endsWith("/session-close")) return reply({ sessionId: "tr-1-a1-agent", status: "completed" });
    if (requestURL.pathname.endsWith("/heartbeat")) return reply({ taskRunId: "tr-1", status: "running" });
    return new Response("not found", { status: 404 });
  };
  taskRunAPIURL = "http://task-run-api.test";
});

after(() => {
  globalThis.fetch = savedFetch;
});

function setEnv(key, value) {
  if (value === undefined) {
    delete process.env[key];
  } else {
    process.env[key] = value;
  }
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
  setEnv("LOOM_TASK_RUN_API_URL", taskRunAPIURL);
  setEnv("LOOM_WORKSPACE", "ws");
  setEnv("LOOM_TASK_RUN_ID", "tr-1");
  setEnv("LOOM_TASK_ID", "T-1");
  setEnv("LOOM_TASK_RUN_NODE_ID", "node-1");
  setEnv("LOOM_TASK_RUN_LEASE_ID", "lease-1");
  setEnv("LOOM_TASK_RUN_LEASE_TOKEN", "lease-token");
  setEnv("LOOM_TASK_RUN_FENCING_TOKEN", "1");
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

  it("scrubToken masks multiple secrets", () => {
    assert.equal(scrubToken("a S1 b S2 c", "S1", "S2"), "a *** b *** c");
    assert.equal(scrubToken("https://x-access-token:abc123@github.com/o/r"), "https://x-access-token:***@github.com/o/r");
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
});

describe("local-task-runner success", () => {
  it("completes when the CLI exits 0 and leaves transcript capture to agent.exec", async () => {
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
    assert.equal(out.transcript_entries, undefined, "the leaf must not duplicate the agent.exec transcript");
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

  it("live-streams backend output to stderr under LOOM_TASK_RUNNER_STREAM_STDERR (daemon-leaf watchdog feed)", async () => {
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
    // The fake backend's stream-json (which contains "did the work") must be teed to
    // stderr live so the supervisor output-timeout watchdog sees per-turn activity.
    assert.ok(chunks.join("").includes("did the work"), "backend output must be teed to stderr when streaming is enabled");
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

  it("delegates unrecognized stream-json transcript fallback to agent.exec", async () => {
    process.env.LOOM_TASK_RUNNER_BACKEND = "gemini";
    process.env.LOOM_WORKTREE_PATH = worktree;
    process.env.LOOM_GEMINI_BIN = fakeBin;
    process.env.FAKE_EXIT_CODE = "0";
    const out = await run();
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.runtime_strategy, "local-cli-gemini");
    assert.equal(out.transcript_entries, undefined);
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
    assert.equal(out.transcript_entries, undefined, "agent.exec owns the transcript artifact");
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
