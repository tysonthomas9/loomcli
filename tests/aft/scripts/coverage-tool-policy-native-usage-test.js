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

async function probe(overrides = {}) {
  const repo = "/root/.loom/workspaces/LOCALMODE/source-repo";
  const row = {
    agent_id: "agt_owned", name: "aft-lead-validation-cov-policy-usage", repo,
    harness: "opencode", native_id: "ses_owned", native_root: "root_owned",
    worktree_path: "/root/.loom/worktrees/source-repo/agt_owned",
  };
  const registration = { url: "http://127.0.0.1:4096/", password: "test-only", pid: 777 };
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
      if (name === "node:sqlite") return { DatabaseSync: class {
        prepare(sql) { return { get: () => sql.includes("agent_native_sessions") ? { owned: 1 } : row }; }
      } };
      throw Error(`unexpected import ${name}`);
    },
    process: { argv: ["node", "agt_owned", "validation", repo],
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
