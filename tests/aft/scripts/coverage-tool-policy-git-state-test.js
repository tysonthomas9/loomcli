// Offline execution of the exact owned-container Git readback, with local-only
// fixtures. No host repository or remote is contacted.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { fileURLToPath } = require("node:url");

const shell = fs.readFileSync(path.join(__dirname, "coverage-tool-policy-stack.sh"), "utf8");
const gitCase = shell.split("  git-state)\n")[1].split("  *) echo 'unknown scoped readback'")[0];
const source = gitCase.match(/node -e '\n([\s\S]*?)\n    ' "\$2"/);
assert.ok(source, "git-state Node program was not found in the stack helper");

function probe(options = {}) {
  const repo = "/root/.loom/workspaces/LOCALMODE/source-repo";
  const root = "/root/.loom/worktrees/source-repo/agt_owned";
  const seed = "/workspace/source-repo";
  const origin = "/workspace/source-repo-origin.git";
  const row = { agent_id: "agt_owned", name: "aft-review-validation-cov-policy",
    preset: "pr-review-interactive", repo, harness: "opencode",
    created_by_kind: "user", parent_agent_id: null, branch: null,
    worktree_path: root, ...options.row };
  let output = "", error = "";
  const git = (cmd, args) => {
    assert.equal(cmd, "git");
    const where = args[0] === "-C" ? args[1] : null;
    const command = where ? args.slice(2).join(" ") : args.join(" ");
    if (command === "rev-parse --show-toplevel") return root;
    if (command === "rev-parse --path-format=absolute --git-common-dir") return repo + "/.git";
    if (command === "remote get-url origin" && where === repo)
      return options.sourceOrigin || origin;
    if (command === "--git-dir " + origin + " rev-parse --is-bare-repository")
      return options.nonBare ? "false" : "true";
    if (command === "remote") return "origin";
    if (command === "remote get-url --all origin") return options.fetchURL || origin;
    if (command === "remote get-url --push --all origin") return options.pushURL || origin;
    if (command === "ls-remote -- " + origin) return "abc refs/heads/main";
    if (command === "rev-parse HEAD") return "abc";
    if (command === "status --porcelain --untracked-files=all") return "";
    if (command === "show-ref") return "abc refs/heads/main";
    throw Error("unexpected git read: " + where + " " + command);
  };
  const sandbox = {
    require(name) {
      if (name === "node:fs") return { realpathSync: p => p, existsSync: () => false };
      if (name === "node:child_process") return { execFileSync: git };
      if (name === "node:path") return path;
      if (name === "node:url") return { fileURLToPath };
      if (name === "node:sqlite") return { DatabaseSync: class {
        prepare() { return { get: () => row }; }
      } };
      throw Error("unexpected import " + name);
    },
    process: { argv: ["node", "agt_owned", "validation", repo, seed], on: () => {},
      stdout: { write: s => { output += s; } }, exit: () => { throw Error("exit"); } },
    console: { error: s => { error += s; } },
    URL,
  };
  try { vm.runInNewContext(source[1], sandbox); }
  catch (e) { if (e.message !== "exit") throw e; }
  return { output, error };
}

test("owned local bare origin and unchanged refs are readable", () => {
  const result = probe();
  assert.equal(result.error, "");
  const state = JSON.parse(result.output);
  assert.equal(state.agent_id, "agt_owned");
  assert.equal(state.checkout, "/root/.loom/worktrees/source-repo/agt_owned");
  assert.deepEqual(JSON.parse(JSON.stringify(state.remoteRefs)),
    { origin: ["abc refs/heads/main", "abc refs/heads/main"] });
  assert.deepEqual(JSON.parse(JSON.stringify(state.remoteURLs)),
    { origin: ["/workspace/source-repo-origin.git", "/workspace/source-repo-origin.git"] });
});

for (const [name, options, category] of [
  ["foreign fetch remote", { fetchURL: "/workspace/foreign.git" }, "git-remote-not-owned"],
  ["foreign push remote", { pushURL: "/workspace/foreign.git" }, "git-remote-not-owned"],
  ["network fetch remote", { fetchURL: "https://example.invalid/repo.git" }, "git-remote-not-local"],
  ["network push remote", { pushURL: "https://example.invalid/repo.git" }, "git-remote-not-local"],
  ["file URL query", { fetchURL: "file:///workspace/source-repo-origin.git?foreign=1" }, "git-remote-url-unsafe"],
  ["nonbare origin", { nonBare: true }, "origin-not-owned-bare-repo"],
  ["foreign source origin", { sourceOrigin: "/workspace/foreign.git" }, "origin-not-owned-bare-repo"],
  ["foreign reviewer", { row: { name: "someone-else" } }, "foreign-reviewer-agent"],
]) {
  test(`git-state rejects ${name}`, () => {
    const result = probe(options);
    assert.equal(result.output, "");
    assert.equal(result.error, `policy git-state: ${category}`);
  });
}
