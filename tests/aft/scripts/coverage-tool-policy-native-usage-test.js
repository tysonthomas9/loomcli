// Offline contract for the exact Node program used inside the owned container.
// Fake responses exercise its parsing and ownership checks, not a live model.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const shell = fs.readFileSync(path.join(__dirname, "coverage-tool-policy-stack.sh"), "utf8");
const usageCase = shell.split("  native-usage)\n")[1].split("  native-tool)\n")[0];
const source = usageCase.match(/node -e '\n([\s\S]*?)\n    ' "\$2"/);
assert.ok(source, "native usage program was not found in the stack helper");
const toolCase = shell.split("  native-tool)\n")[1].split("  git-state)\n")[0];
const toolSource = toolCase.match(/node -e '\n([\s\S]*?)\n    ' "\$2"/);
assert.ok(toolSource, "native tool program was not found in the stack helper");

async function probe(overrides = {}, options = {}) {
  const repo = "/root/.loom/workspaces/LOCALMODE/source-repo";
  const row = {
    agent_id: "agt_owned", name: "aft-lead-validation-cov-policy-usage", repo,
    harness: "opencode", native_id: "ses_owned", native_root: "root_owned",
    worktree_path: "/root/.loom/worktrees/source-repo/agt_owned",
  };
  const registration = { url: "http://127.0.0.1:4096/", password: "test-only", pid: 777,
    ...options.registration };
  const responses = {
    "/api/info": { pid: 777 },
    "/api/session/ses_owned": { data: { id: "ses_owned", metadata: { agent_id: "agt_owned" },
      location: { directory: row.worktree_path } } },
    "/api/session/ses_owned/message": { data: [{ id: "msg_1", type: "assistant",
      time: { completed: 1 }, tokens: { input: 3, output: 2, reasoning: 1,
        cache: { read: 4, write: 5 } }, cost: 0.25 }] },
    ...overrides,
  };
  let output = "";
  const sandbox = {
    require(name) {
      if (name === "node:fs") return { readFileSync: () => JSON.stringify(registration) };
      if (name === "node:path") return path;
      if (name === "node:sqlite") return { DatabaseSync: class {
        prepare(sql) { return { get: () => sql.includes("agent_native_sessions") ? { owned: 1 } : row }; }
      } };
      throw Error(`unexpected import ${name}`);
    },
    process: { argv: ["node", "agt_owned", "validation", repo],
      env: { LOOM_CONFIG_DIR: "/root/.loom", ...options.env },
      stdout: { write: s => { output += s; } }, exitCode: 0 },
    console: { error: () => {} },
    fetch: async url => ({ ok: true, json: async () => responses[url.pathname] }),
    URL, Buffer, AbortSignal,
  };
  await vm.runInNewContext(source[1], sandbox);
  return { output, exitCode: sandbox.process.exitCode };
}

test("owned native usage reads top-level info and wrapped session/messages", async () => {
  const result = await probe();
  assert.equal(result.exitCode, 0);
  const actual = JSON.parse(result.output);
  assert.equal(actual.agent_id, "agt_owned");
  assert.equal(actual.native_id, "ses_owned");
  assert.deepEqual(JSON.parse(JSON.stringify(actual.steps)), [{ itemID: "msg_1",
    inputTokens: 3, outputTokens: 3, cacheReadTokens: 4, cacheWriteTokens: 5,
    costUsd: 0.25 }]);
});

for (const [name, change] of [
  ["wrong service PID", { "/api/info": { pid: 778 } }],
  ["wrong native session", { "/api/session/ses_owned": { data: { id: "ses_foreign" } } }],
  ["missing session data", { "/api/session/ses_owned": {} }],
  ["empty native steps", { "/api/session/ses_owned/message": { data: [] } }],
]) {
  test(`native usage rejects ${name}`, async () => {
    const result = await probe(change);
    assert.equal(result.exitCode, 1);
    assert.equal(result.output, "");
  });
}

for (const url of ["http://user@127.0.0.1:4096/", "http://127.0.0.1:4096/?x=1",
  "http://127.0.0.1:4096/#x"]) {
  test(`native usage rejects unowned service URL ${url}`, async () => {
    const result = await probe({}, { registration: { url } });
    assert.equal(result.exitCode, 1);
    assert.equal(result.output, "");
  });
}

async function probeTool(overrides = {}, options = {}) {
  const repo = "/root/.loom/workspaces/LOCALMODE/source-repo";
  const row = { agent_id: "agt_owned", name: "aft-lead-validation-cov-policy",
    preset: "lead", repo, harness: "opencode", created_by_kind: "user",
    parent_agent_id: null, native_id: "ses_owned", native_root: "",
    worktree_path: "/root/.loom/worktrees/source-repo/agt_owned",
    ...options.row };
  const registration = { url: "http://127.0.0.1:4096/", password: "test-only", pid: 777,
    ...options.registration };
  const sentinel = "ghp_AFTONLYvalidationQ7mR2pK9xT4vN8cY6bL5fS3dH1jW0";
  const responses = {
    "/api/info": { pid: 777 },
    "/api/session/ses_owned": { data: { id: "ses_owned", metadata: { agent_id: "agt_owned" },
      location: { directory: row.worktree_path } } },
    "/api/session/ses_owned/message": { data: [{ id: "msg_1", type: "assistant",
      content: [{ type: "tool", id: "call_1", name: "bash",
        state: { status: "completed", input: { command: `printf SAFE # TOKEN=${sentinel}` },
          content: { text: "SAFE" } } }] }] },
    ...overrides,
  };
  let output = "";
  const sandbox = {
    require(name) {
      if (name === "node:fs") return { readFileSync: () => JSON.stringify(registration) };
      if (name === "node:path") return path;
      if (name === "node:sqlite") return { DatabaseSync: class {
        prepare(sql) { return { get: (...args) => {
          if (sql.includes("agent_native_sessions"))
            return args.join("|") === "agt_owned|opencode||ses_owned" ? { owned: 1 } : null;
          return sql.includes("FROM agents") && sql.includes("agent_id=?") ? row : null;
        } }; }
      } };
      throw Error(`unexpected import ${name}`);
    },
    process: { argv: ["node", "agt_owned", "validation", repo],
      env: { LOOM_CONFIG_DIR: "/root/.loom", ...options.env },
      stdout: { write: s => { output += s; } }, exitCode: 0 },
    console: { error: () => {} },
    fetch: async url => ({ ok: true, json: async () => responses[url.pathname] }),
    URL, Buffer, AbortSignal,
  };
  assert.ok(!toolSource[1].includes(sentinel), "fixture must not be in production probe");
  try { await vm.runInNewContext(toolSource[1], sandbox); }
  catch { sandbox.process.exitCode = 1; }
  return { output, exitCode: sandbox.process.exitCode };
}

test("owned native tool accepts empty root and wrapped session/messages", async () => {
  const result = await probeTool();
  assert.equal(result.exitCode, 0);
  assert.deepEqual(JSON.parse(JSON.stringify(JSON.parse(result.output))),
    { agent_id: "agt_owned", tool_item_ids: ["msg_1/tool/call_1"] });
  assert.ok(!result.output.includes("ghp_AFTONLY"));
});

for (const [name, changes, options] of [
  ["wrong PID", { "/api/info": { pid: 778 } }],
  ["foreign session", { "/api/session/ses_owned": { data: { id: "ses_foreign" } } }],
  ["missing session data", { "/api/session/ses_owned": {} }],
  ["foreign owner", {}, { row: { native_root: "root_foreign" } }],
  ["service URL with userinfo", {}, { registration: { url: "http://user@127.0.0.1:4096/" } }],
  ["service URL with query", {}, { registration: { url: "http://127.0.0.1:4096/?x=1" } }],
  ["service URL with fragment", {}, { registration: { url: "http://127.0.0.1:4096/#x" } }],
]) {
  test(`native tool rejects ${name}`, async () => {
    const result = await probeTool(changes, options);
    assert.equal(result.exitCode, 1);
    assert.equal(result.output, "");
  });
}
