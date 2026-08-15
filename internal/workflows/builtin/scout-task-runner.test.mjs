import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { after, afterEach, beforeEach, describe, it } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";

// The builtin source imports @flue/runtime for its default workflow
// declaration. Stage the source beside a declaration-only stub so this leaf's
// direct node:test suite does not depend on a materialized workflow bundle.
const moduleStageRoot = fs.mkdtempSync(path.join(os.tmpdir(), "scout-runner-stage-"));
const builtinSourceDir = path.dirname(fileURLToPath(import.meta.url));
const flueStub = path.join(moduleStageRoot, "node_modules", "@flue", "runtime");
fs.mkdirSync(flueStub, { recursive: true });
fs.writeFileSync(
  path.join(flueStub, "package.json"),
  JSON.stringify({ name: "@flue/runtime", type: "module", main: "index.js" }),
);
fs.writeFileSync(
  path.join(flueStub, "index.js"),
  "export const defineAgent = (fn) => ({ __agent: fn });\nexport const defineWorkflow = (definition) => definition;\n",
);
const stagedSource = path.join(moduleStageRoot, "workflows", "scout-task-runner.ts");
const stagedConverter = path.join(moduleStageRoot, "lib", "transcript-convert.ts");
fs.mkdirSync(path.dirname(stagedSource), { recursive: true });
fs.mkdirSync(path.dirname(stagedConverter), { recursive: true });
fs.copyFileSync(
  path.join(builtinSourceDir, "scout-task-runner.ts"),
  stagedSource,
);
fs.copyFileSync(path.join(builtinSourceDir, "transcript-convert.ts"), stagedConverter);
const {
  backendArgs,
  resolveBackend,
  resolveWorkspaceRoot,
  run,
  scoutFencedContent,
} = await import(pathToFileURL(stagedSource).href);
const { transcriptEntriesForBackend } = await import(pathToFileURL(stagedConverter).href);

after(() => {
  fs.rmSync(moduleStageRoot, { recursive: true, force: true });
});

// A fake backend CLI: consumes the stdin prompt, writes the JSON in
// FAKE_RESULT_JSON to FAKE_RESULT_TARGET (the result file path the leaf's
// prompt names), then exits FAKE_EXIT_CODE. Lets the analyze phase run
// end-to-end without a real backend installed.
const FAKE_CLI = `#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
let stdin = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => { stdin += chunk; });
process.stdin.on("end", () => {
  if (process.env.FAKE_STDIN_TARGET) {
    fs.writeFileSync(process.env.FAKE_STDIN_TARGET, stdin);
  }
  if (process.env.FAKE_RESULT_TARGET && process.env.FAKE_RESULT_JSON) {
    fs.mkdirSync(path.dirname(process.env.FAKE_RESULT_TARGET), { recursive: true });
    fs.writeFileSync(process.env.FAKE_RESULT_TARGET, process.env.FAKE_RESULT_JSON);
  }
  if (process.env.FAKE_STDOUT_CHARS) {
    process.stdout.write("stdout-start\\n" + "x".repeat(Number(process.env.FAKE_STDOUT_CHARS)) + "\\nstdout-end\\n");
  } else if (process.env.FAKE_STDOUT) {
    process.stdout.write(process.env.FAKE_STDOUT);
  }
  if (process.env.FAKE_STDERR_CHARS) {
    process.stderr.write("stderr-start\\n" + "y".repeat(Number(process.env.FAKE_STDERR_CHARS)) + "\\nstderr-end\\n");
  } else if (process.env.FAKE_STDERR) {
    process.stderr.write(process.env.FAKE_STDERR);
  }
  process.stdout.write(JSON.stringify({ type: "done" }) + "\\n");
  process.exitCode = Number(process.env.FAKE_EXIT_CODE || "0");
});
`;

const MUTATED_ENV = [
  "LOOM_WORKSPACE_RUNTIME_DIR",
  "LOOM_WORKTREE_PATH",
  "LOOM_TASK_RUNNER_BACKEND",
  "LOOM_CODEX_BIN",
  "FAKE_RESULT_TARGET",
  "FAKE_RESULT_JSON",
  "FAKE_STDIN_TARGET",
  "FAKE_EXIT_CODE",
  "FAKE_STDOUT",
  "FAKE_STDERR",
  "FAKE_STDOUT_CHARS",
  "FAKE_STDERR_CHARS",
];

let savedEnv;
let workRoot;

beforeEach(() => {
  savedEnv = {};
  for (const name of MUTATED_ENV) {
    savedEnv[name] = process.env[name];
    delete process.env[name];
  }
  workRoot = fs.mkdtempSync(path.join(os.tmpdir(), "scout-runner-test-"));
});

afterEach(() => {
  for (const name of MUTATED_ENV) {
    if (savedEnv[name] === undefined) {
      delete process.env[name];
    } else {
      process.env[name] = savedEnv[name];
    }
  }
  fs.rmSync(workRoot, { recursive: true, force: true });
});

function makeDir(...segments) {
  const dir = path.join(workRoot, ...segments);
  fs.mkdirSync(dir, { recursive: true });
  return dir;
}

function makeGitRepo(name) {
  const dir = makeDir(name);
  execFileSync("git", ["init", "-q"], { cwd: dir });
  fs.writeFileSync(path.join(dir, "README.md"), "# " + name + "\n");
  execFileSync("git", ["-c", "user.email=t@example.test", "-c", "user.name=T", "add", "-A"], { cwd: dir });
  execFileSync("git", ["-c", "user.email=t@example.test", "-c", "user.name=T", "commit", "-q", "-m", "init"], { cwd: dir });
  return dir;
}

function installFakeBackend(root) {
  const bin = path.join(workRoot, "fake-codex");
  fs.writeFileSync(bin, FAKE_CLI);
  fs.chmodSync(bin, 0o755);
  process.env.LOOM_TASK_RUNNER_BACKEND = "codex";
  process.env.LOOM_CODEX_BIN = bin;
  process.env.LOOM_WORKSPACE_RUNTIME_DIR = root;
}

function analysisOf(result) {
  assert.equal(result.status, "completed", JSON.stringify(result));
  return JSON.parse(result.runtimeMetadata.scout_analysis);
}

describe("resolveWorkspaceRoot placement guard", () => {
  it("refuses when nothing anchors the workspace root", () => {
    const got = resolveWorkspaceRoot({});
    assert.equal(got.ok, false);
  });

  it('refuses the "." runtime-dir fallback', () => {
    const got = resolveWorkspaceRoot({ LOOM_WORKSPACE_RUNTIME_DIR: "." });
    assert.equal(got.ok, false);
    assert.match(got.reason, /"\." fallback/);
  });

  it('refuses a "." worktree path', () => {
    const got = resolveWorkspaceRoot({ LOOM_WORKTREE_PATH: "." });
    assert.equal(got.ok, false);
  });

  it("refuses a worktree fallback that is itself a git checkout", () => {
    const repo = makeGitRepo("solo");
    const got = resolveWorkspaceRoot({ LOOM_WORKTREE_PATH: repo });
    assert.equal(got.ok, false);
    assert.match(got.reason, /repo checkout/);
  });

  it("accepts an explicit runtime dir", () => {
    const dir = makeDir("ws");
    const got = resolveWorkspaceRoot({ LOOM_WORKSPACE_RUNTIME_DIR: dir });
    assert.equal(got.ok, true);
    assert.equal(got.path, dir);
  });

  it("accepts a non-repo worktree fallback", () => {
    const dir = makeDir("ws2");
    const got = resolveWorkspaceRoot({ LOOM_WORKTREE_PATH: dir });
    assert.equal(got.ok, true);
  });
});

describe("write phase", () => {
  function writeRun(root, input) {
    process.env.LOOM_WORKSPACE_RUNTIME_DIR = root;
    return run({ payload: { task_run_id: "task-run-w", input: { phase: "write", ...input } } });
  }

  it("fails closed with scout_placement_refused on the fallback", async () => {
    process.env.LOOM_WORKSPACE_RUNTIME_DIR = ".";
    const result = await run({ payload: { task_run_id: "t", input: { phase: "write", agentsMd: "x" } } });
    assert.equal(result.status, "failed");
    assert.equal(result.errorClass, "scout_placement_refused");
  });

  it("first generation creates fenced agents.md and a journal", async () => {
    const root = makeDir("ws");
    const result = await writeRun(root, {
      agentsMd: "# Workspace\n\noverview here",
      historyEntry: {
        timestamp: "2026-08-14T00:00:00Z",
        driverRunId: "run-1",
        repos: [{ name: "loomcli", sha: "abc1234def5678", branch: "main" }],
        created: [{ id: "ISSUE-1", title: "Do the thing" }],
        skipped: [{ title: "Old idea", reason: "duplicate of ISSUE-9" }],
        warnings: [],
      },
    });
    assert.equal(result.status, "completed");
    assert.equal("logsRef" in result, false);
    assert.equal("transcript_entries" in result, false);
    const write = JSON.parse(result.runtimeMetadata.scout_write);
    assert.equal(write.agentsMdMode, "created");
    assert.equal(write.historyMode, "created");

    const agents = fs.readFileSync(path.join(root, "agents.md"), "utf8");
    assert.ok(agents.includes("<!-- scout:begin -->"));
    assert.ok(agents.includes("<!-- scout:end -->"));
    assert.ok(agents.includes("overview here"));
    assert.equal(scoutFencedContent(agents).includes("overview here"), true);

    const history = fs.readFileSync(path.join(root, "history.md"), "utf8");
    assert.ok(history.includes("## Run 2026-08-14T00:00:00Z (driver run run-1)"));
    assert.ok(history.includes("- loomcli @ abc1234def56 (branch main)"));
    assert.ok(history.includes("- ISSUE-1 — Do the thing"));
    assert.ok(history.includes("- Old idea — duplicate of ISSUE-9"));
    assert.ok(history.includes("Warnings:\n- none"));
  });

  it("regenerates only inside the fences, preserving human edits byte-identically", async () => {
    const root = makeDir("ws");
    await writeRun(root, { agentsMd: "first generation", historyEntry: {} });
    const generated = fs.readFileSync(path.join(root, "agents.md"), "utf8");
    const humanTop = "# My notes\n\nkeep this exactly.\n\n";
    const humanBottom = "\n## Appendix\n\nalso mine.\n";
    fs.writeFileSync(path.join(root, "agents.md"), humanTop + generated + humanBottom);

    const result = await writeRun(root, { agentsMd: "second generation", historyEntry: {} });
    const write = JSON.parse(result.runtimeMetadata.scout_write);
    assert.equal(write.agentsMdMode, "fences-replaced");
    const updated = fs.readFileSync(path.join(root, "agents.md"), "utf8");
    assert.ok(updated.startsWith(humanTop));
    assert.ok(updated.endsWith(humanBottom));
    assert.ok(updated.includes("second generation"));
    assert.equal(updated.includes("first generation"), false);
  });

  it("appends a fenced block to a human-owned file without fences", async () => {
    const root = makeDir("ws");
    fs.writeFileSync(path.join(root, "agents.md"), "# Handwritten\n\nhuman only\n");
    const result = await writeRun(root, { agentsMd: "scout content", historyEntry: {} });
    const write = JSON.parse(result.runtimeMetadata.scout_write);
    assert.equal(write.agentsMdMode, "fences-appended");
    const updated = fs.readFileSync(path.join(root, "agents.md"), "utf8");
    assert.ok(updated.startsWith("# Handwritten\n\nhuman only\n"));
    assert.ok(updated.indexOf("<!-- scout:begin -->") > updated.indexOf("human only"));
  });

  it("appends run sections and journals zero-repo runs", async () => {
    const root = makeDir("ws");
    await writeRun(root, { agentsMd: "", historyEntry: { timestamp: "2026-08-14T00:00:00Z", driverRunId: "run-1" } });
    const result = await writeRun(root, {
      agentsMd: "",
      historyEntry: { timestamp: "2026-08-21T00:00:00Z", driverRunId: "run-2", nothingToAnalyze: true },
    });
    const write = JSON.parse(result.runtimeMetadata.scout_write);
    assert.equal(write.agentsMdMode, "unchanged");
    assert.equal(write.historyMode, "appended");
    const history = fs.readFileSync(path.join(root, "history.md"), "utf8");
    assert.ok(history.indexOf("run-1") < history.indexOf("run-2"));
    assert.ok(history.includes("Nothing to analyze: the workspace has no attached repos."));
    assert.equal(fs.existsSync(path.join(root, "agents.md")), false);
  });
});

describe("analyze phase", () => {
  it("uses a payload-provided role prompt", async () => {
    const root = makeDir("ws");
    makeGitRepo(path.join("ws", "alpha"));
    installFakeBackend(root);
    const taskRunId = "task-run-role-prompt";
    process.env.FAKE_RESULT_TARGET = path.join(
      root,
      ".loom",
      "scout",
      taskRunId,
      "result.json",
    );
    process.env.FAKE_RESULT_JSON = JSON.stringify({
      recommendations: [],
      skipped: [],
      agentsMd: "",
    });
    process.env.FAKE_STDIN_TARGET = path.join(workRoot, "custom-prompt.txt");

    const result = await run({
      payload: {
        task_run_id: taskRunId,
        input: { phase: "analyze", role_prompt: "CUSTOM ROLE PROMPT" },
      },
    });

    assert.equal(result.status, "completed");
    assert.equal(
      fs.readFileSync(process.env.FAKE_STDIN_TARGET, "utf8"),
      "CUSTOM ROLE PROMPT",
    );
  });

  it("uses the embedded fallback when the payload role prompt is absent or empty", async () => {
    for (const rolePrompt of [undefined, "   "]) {
      const root = makeDir("ws-fallback-" + String(rolePrompt === undefined));
      makeGitRepo(path.join(path.basename(root), "alpha"));
      installFakeBackend(root);
      const taskRunId = "task-run-fallback-" + String(rolePrompt === undefined);
      process.env.FAKE_RESULT_TARGET = path.join(
        root,
        ".loom",
        "scout",
        taskRunId,
        "result.json",
      );
      process.env.FAKE_RESULT_JSON = JSON.stringify({
        recommendations: [],
        skipped: [],
        agentsMd: "",
      });
      process.env.FAKE_STDIN_TARGET = path.join(workRoot, taskRunId + ".txt");

      const result = await run({
        payload: {
          task_run_id: taskRunId,
          input: { phase: "analyze", role_prompt: rolePrompt },
        },
      });

      assert.equal(result.status, "completed");
      const prompt = fs.readFileSync(process.env.FAKE_STDIN_TARGET, "utf8");
      assert.match(prompt, /You are the Scout/);
      assert.match(prompt, /Workspace root:/);
      assert.doesNotMatch(prompt, /\{\{WORKSPACE_ROOT\}\}/);
    }
  });

  it("no-ops with nothingToAnalyze when the workspace has no repos", async () => {
    const root = makeDir("ws");
    process.env.LOOM_WORKSPACE_RUNTIME_DIR = root;
    const result = await run({ payload: { task_run_id: "t", input: { phase: "analyze" } } });
    const analysis = analysisOf(result);
    assert.equal("logsRef" in result, false);
    assert.equal(analysis.nothingToAnalyze, true);
    assert.deepEqual(analysis.recommendations, []);
    assert.ok(analysis.warnings.some((w) => w.includes("nothing to analyze")));
  });

  it("runs the backend CLI and normalizes the result (labels, priority, anchors, cap)", async () => {
    const root = makeDir("ws");
    makeGitRepo(path.join("ws", "alpha"));
    installFakeBackend(root);

    const taskRunId = "task-run-x-scout-analyze";
    process.env.FAKE_RESULT_TARGET = path.join(root, ".loom", "scout", taskRunId, "result.json");
    const recommendations = [];
    for (let i = 0; i < 7; i++) {
      recommendations.push({
        title: "Recommendation " + i,
        description: "Work.\n\n## Acceptance Criteria\n\n- done",
        rationale: "grounded",
        repo: "alpha",
        labels: i === 0 ? [] : ["recommended", "repo:alpha"],
        priority: i === 0 ? 9 : 3,
        anchors: i === 0 ? ["README.md", "missing.txt"] : ["README.md"],
      });
    }
    process.env.FAKE_RESULT_JSON = JSON.stringify({
      recommendations,
      skipped: [{ title: "Covered already", reason: "in journal" }],
      agentsMd: "# Workspace\n\ndrafted",
    });

    const result = await run({ payload: { task_run_id: taskRunId, input: { phase: "analyze" } } });
    const analysis = analysisOf(result);
    assert.equal(analysis.nothingToAnalyze, false);
    assert.equal(analysis.repos.length, 1);
    assert.equal(analysis.repos[0].name, "alpha");
    assert.ok(analysis.repos[0].sha.length >= 12);

    // Cap: 5 created candidates, overflow lands in skipped alongside the
    // model's own skip list.
    assert.equal(analysis.recommendations.length, 5);
    assert.equal(analysis.skipped.length, 3);
    assert.ok(analysis.skipped.some((s) => s.reason.includes("cap")));
    assert.ok(analysis.skipped.some((s) => s.title === "Covered already"));

    // Quarantine labels forced, priority clamped into Loom's 0-4, fabricated
    // anchor dropped with a warning.
    const first = analysis.recommendations[0];
    assert.ok(first.labels.includes("recommended"));
    assert.ok(first.labels.includes("repo:alpha"));
    assert.equal(first.priority, 4);
    assert.deepEqual(first.anchors, ["README.md"]);
    assert.ok(analysis.warnings.some((w) => w.includes("missing.txt")));

    assert.equal(analysis.agentsMd.includes("drafted"), true);
    // The scratch result dir is cleaned up after parsing.
    assert.equal(fs.existsSync(path.join(root, ".loom", "scout", taskRunId)), false);
  });

  it("returns canonical transcript_entries from the backend stream", async () => {
    const root = makeDir("ws");
    makeGitRepo(path.join("ws", "alpha"));
    installFakeBackend(root);
    const taskRunId = "task-run-canonical-transcript";
    process.env.FAKE_RESULT_TARGET = path.join(root, ".loom", "scout", taskRunId, "result.json");
    process.env.FAKE_RESULT_JSON = JSON.stringify({ recommendations: [], skipped: [], agentsMd: "" });
    process.env.FAKE_STDOUT = [
      JSON.stringify({ type: "item.started", item: { id: "i1", type: "command_execution", command: "git status" } }),
      JSON.stringify({ type: "item.completed", item: { id: "i1", type: "command_execution", command: "git status", aggregated_output: "clean\n", exit_code: 0, status: "completed" } }),
      JSON.stringify({ type: "item.completed", item: { id: "i2", type: "agent_message", text: "analysis complete" } }),
      JSON.stringify({ type: "turn.completed", usage: { input_tokens: 12, output_tokens: 3 } }),
    ].join("\n") + "\n";

    const result = await run({ payload: { task_run_id: taskRunId, input: { phase: "analyze" } } });

    assert.equal(result.status, "completed");
    assert.equal(result.transcript_entries[0].type, "session_meta");
    assert.equal(result.transcript_entries.filter((entry) => entry.tool_name === "shell").length, 1);
    assert.ok(result.transcript_entries.some((entry) => entry.type === "text" && entry.text === "analysis complete"));
    assert.ok(result.transcript_entries.some((entry) => entry.type === "result" && /in=12/.test(entry.text)));
  });

  it("fails with scout_backend_failed when the CLI exits nonzero", async () => {
    const root = makeDir("ws");
    makeGitRepo(path.join("ws", "alpha"));
    installFakeBackend(root);
    process.env.FAKE_EXIT_CODE = "3";
    const result = await run({ payload: { task_run_id: "t", input: { phase: "analyze" } } });
    assert.equal(result.status, "failed");
    assert.equal(result.errorClass, "scout_backend_failed");
    assert.equal("logsRef" in result, false);
  });

  it("keeps up to 100000 stdout bytes and 20000 stderr bytes in the AI log", async () => {
    const root = makeDir("ws");
    makeGitRepo(path.join("ws", "alpha"));
    installFakeBackend(root);
    const taskRunId = "task-run-long-ai-log";
    process.env.FAKE_RESULT_TARGET = path.join(root, ".loom", "scout", taskRunId, "result.json");
    process.env.FAKE_RESULT_JSON = JSON.stringify({
      recommendations: [],
      skipped: [],
      agentsMd: "",
    });
    process.env.FAKE_STDOUT_CHARS = "90000";
    process.env.FAKE_STDERR_CHARS = "15000";

    const result = await run({ payload: { task_run_id: taskRunId, input: { phase: "analyze" } } });

    assert.equal(result.status, "completed");
    assert.ok(result.logs.includes("stdout-start"));
    assert.ok(result.logs.includes("stdout-end"));
    assert.ok(result.logs.includes("stderr-start"));
    assert.ok(result.logs.includes("stderr-end"));
    assert.equal("transcript_entries" in result, false, "plain output remains raw-only");
  });
});

describe("canonical transcript conversion", () => {
  it("ignores malformed and plain lines while retaining recognized events", () => {
    const entries = transcriptEntriesForBackend("codex", [
      "workspace root: /tmp/ws",
      "{broken",
      JSON.stringify({ type: "item.completed", item: { type: "agent_message", text: "done" } }),
    ].join("\n"));

    assert.deepEqual(entries.map((entry) => entry.type), ["session_meta", "text"]);
    assert.equal(entries[1].text, "done");
  });

  it("dedupes item.started and preserves the completed command", () => {
    const entries = transcriptEntriesForBackend("codex", [
      JSON.stringify({ type: "item.started", item: { id: "i1", type: "command_execution", command: "git status" } }),
      JSON.stringify({ type: "item.completed", item: { id: "i1", type: "command_execution", command: "git status", aggregated_output: "clean\n", exit_code: 0 } }),
    ].join("\n"));

    const commands = entries.filter((entry) => entry.type === "tool_use" && entry.tool_name === "shell");
    assert.equal(commands.length, 1);
    assert.equal(commands[0].tool_input.command, "git status");
    assert.equal(commands[0].output, "clean\n");
  });

  it("turn.failed becomes a canonical failed result entry", () => {
    const entries = transcriptEntriesForBackend("codex", JSON.stringify({
      type: "turn.failed",
      error: { message: "model context exhausted" },
    }));

    assert.deepEqual(entries.map((entry) => entry.type), ["session_meta", "result"]);
    assert.equal(entries[1].role, "system");
    assert.equal(entries[1].text, "failed");
  });

  it("converts a supported non-codex backend stream", () => {
    const entries = transcriptEntriesForBackend("claude", [
      JSON.stringify({
        type: "assistant",
        message: { content: [{ type: "thinking", thinking: "inspect repository history" }] },
      }),
      JSON.stringify({
        type: "result",
        is_error: false,
        usage: { input_tokens: 21, output_tokens: 5 },
      }),
    ].join("\n"));

    assert.deepEqual(entries.map((entry) => entry.type), ["session_meta", "reasoning", "result"]);
    assert.equal(entries[0].text, "local-cli-claude session");
    assert.equal(entries[1].text, "inspect repository history");
    assert.match(entries[2].text, /in=21.*out=5/);
  });

  it("omits unknown-only and plain-only streams", () => {
    assert.equal(transcriptEntriesForBackend("codex", "plain output only\n"), undefined);
    assert.equal(
      transcriptEntriesForBackend("codex", JSON.stringify({ type: "future.event", payload: { value: 1 } })),
      undefined,
    );
    assert.equal(transcriptEntriesForBackend("future-backend", "{}"), undefined);
  });

  it("converts the captured real-sample shape into canonical rows", () => {
    const sample = [
      "workspace root: /root/.loom/workspaces/LOCALMODE (LOOM_WORKTREE_PATH)",
      '…\\":\\"rs_0008f76a2ac4f0d7016a7fa5a97ed8819ab6861c4972f53a23\\",\\"summary\\":[]}',
      '{"type":"item.started","item":{"id":"item_5","type":"command_execution","command":"/bin/bash -lc \\"git status\\"","status":"in_progress"}}',
      '{"type":"item.completed","item":{"id":"item_5","type":"command_execution","command":"/bin/bash -lc \\"git status\\"","aggregated_output":"clean\\n","exit_code":0,"status":"completed"}}',
      '{"type":"item.completed","item":{"id":"item_7","type":"file_change","changes":[{"path":"/root/.loom/result.json","kind":"add"}],"status":"completed"}}',
      '{"type":"item.completed","item":{"id":"item_9","type":"agent_message","text":"{\\"recommendations\\":[]}"}}',
      '{"type":"turn.completed","usage":{"input_tokens":349798,"cached_input_tokens":305920,"output_tokens":3342,"reasoning_output_tokens":1118}}',
    ].join("\n");

    const entries = transcriptEntriesForBackend("codex", sample);
    assert.deepEqual(entries.map((entry) => entry.type), ["session_meta", "tool_use", "tool_use", "text", "result"]);
    assert.equal(entries[1].tool_name, "shell");
    assert.equal(entries[2].tool_name, "apply_patch");
    assert.equal(entries[3].text, '{"recommendations":[]}');
    assert.match(entries[4].text, /in=349798.*out=3342.*cache_read=305920/);
  });
});

describe("backend invocation", () => {
  it("defaults to codex and honors LOOM_TASK_RUNNER_BACKEND", () => {
    assert.equal(resolveBackend(), "codex");
    process.env.LOOM_TASK_RUNNER_BACKEND = "claude";
    assert.equal(resolveBackend(), "claude");
  });

  it("codex args are agentic and skip the git repo check (workspace root is not a repo)", () => {
    const args = backendArgs("codex", "/ws", "prompt");
    assert.ok(args.includes("--skip-git-repo-check"));
    assert.ok(args.includes("--dangerously-bypass-approvals-and-sandbox"));
    assert.equal(args[args.length - 1], "-");
  });

  it("claude args allow tool use and carry the prompt positionally", () => {
    const args = backendArgs("claude", "/ws", "the prompt");
    assert.ok(args.includes("--dangerously-skip-permissions"));
    assert.equal(args[args.length - 1], "the prompt");
  });
});
