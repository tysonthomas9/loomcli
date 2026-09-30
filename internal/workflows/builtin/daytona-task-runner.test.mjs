import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { after, before, describe, it } from "node:test";

// The daytona runner statically imports bundle-only packages (@daytona/sdk,
// @flue/runtime, @loom/sdk). To exercise the demo-mode gate (which returns
// before any of those are used at *runtime*) we stage stubs for those bare
// specifiers next to a copy of the runner, then import the copy. The module's
// default export calls defineWorkflow()/defineAgent() at eval time (flue HEAD
// requires every workflow to default-export a definition), so the @flue/runtime
// stub must provide those two as callables — the rest stay empty.
const here = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.join(here, "daytona-task-runner.ts");

let stageRoot;
let mod;
const savedEnv = {};
const ENV_KEYS = ["LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES", "DAYTONA_TASK_MODE", "LOOM_TASK_RUN_REQUEST_JSON"];

function stub(dir, relFile, contents = "export default {};\n") {
  const file = path.join(dir, relFile);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, contents);
}

before(async () => {
  for (const key of ENV_KEYS) {
    savedEnv[key] = process.env[key];
  }
  stageRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-daytona-stage-"));
  const nm = path.join(stageRoot, "node_modules");
  const daytona = path.join(nm, "@daytona", "sdk");
  const flue = path.join(nm, "@flue", "runtime");
  const loom = path.join(nm, "@loom", "sdk");
  stub(daytona, "index.js", "export const Daytona = function () {};\nexport default { Daytona };\n");
  fs.writeFileSync(path.join(daytona, "package.json"), JSON.stringify({ name: "@daytona/sdk", type: "module", main: "index.js" }));
  // defineAgent/defineWorkflow are invoked at module-eval time by the default
  // export; the test exercises the named exports, so trivial pass-throughs suffice.
  stub(flue, "index.js", "export const defineAgent = (fn) => ({ __agent: fn });\nexport const defineWorkflow = (def) => def;\n");
  stub(flue, "internal.js");
  fs.writeFileSync(path.join(flue, "package.json"), JSON.stringify({
    name: "@flue/runtime",
    type: "module",
    exports: { ".": "./index.js", "./internal": "./internal.js" },
  }));
  stub(loom, "runner.js", "export class TaskRunClient { static fromEnv() { throw new Error('stub'); } }\n");
  stub(loom, "runtime-adapters.js", [
    "export const createFlueTranscriptCollector = () => ({ entries: [], push() { return []; } });",
    "export const flueUsageToTaskUsage = () => ({});",
    "export const redactTranscriptEntries = (e) => e;",
    "export const serializeTranscriptJSONL = () => '';",
  ].join("\n") + "\n");
  fs.writeFileSync(path.join(loom, "package.json"), JSON.stringify({
    name: "@loom/sdk",
    type: "module",
    exports: { "./runner": "./runner.js", "./runtime-adapters": "./runtime-adapters.js" },
  }));

  const copy = path.join(stageRoot, "daytona-task-runner.ts");
  fs.copyFileSync(SOURCE, copy);
  mod = await import(pathToFileURL(copy).href);
});

after(() => {
  for (const key of ENV_KEYS) {
    if (savedEnv[key] === undefined) {
      delete process.env[key];
    } else {
      process.env[key] = savedEnv[key];
    }
  }
  try {
    fs.rmSync(stageRoot, { recursive: true, force: true });
  } catch {
    // best-effort cleanup
  }
});

function request(mode) {
  return { task_run_id: "tr-d", task_id: "T-d", runner: "daytona-task-runner", input: { mode } };
}

describe("daytona-task-runner PR requests", () => {
  for (const flag of ["openPullRequest", "stackedPullRequests"]) {
    it(`rejects ${flag} until the host can publish`, async () => {
      const payload = request("normal");
      payload.input[flag] = true;
      const out = await mod.run({ payload });
      assert.equal(out.status, "failed");
      assert.equal(out.errorClass, "host_publish_required");
    });
  }
});

describe("daytona-task-runner demo-mode gate (design §4.5)", () => {
  for (const mode of ["e2e-smoke", "slack-pr-chain"]) {
    it(`fails closed for ${mode} when LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES is unset`, async () => {
      delete process.env.LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES;
      const out = await mod.run({ payload: request(mode) });
      assert.equal(out.status, "failed");
      assert.equal(out.exitCode, 1);
      assert.equal(out.errorClass, "daytona_demo_mode_disabled");
    });

    it(`fails closed for ${mode} when the flag is not exactly "1"`, async () => {
      process.env.LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES = "true";
      const out = await mod.run({ payload: request(mode) });
      assert.equal(out.errorClass, "daytona_demo_mode_disabled");
    });

    it(`fails closed for ${mode} when request input tries to enable demo modes`, async () => {
      delete process.env.LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES;
      const payload = request(mode);
      payload.input.enableDemoModes = true;
      payload.input.enableDaytonaDemoModes = true;
      const out = await mod.run({ payload });
      assert.equal(out.errorClass, "daytona_demo_mode_disabled");
    });
  }

  it("does NOT fire the demo gate when the flag is '1' (proceeds past the gate)", async () => {
    process.env.LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES = "1";
    const out = await mod.run({ payload: request("e2e-smoke") });
    // With the gate open the runner proceeds and fails later (codex auth / no
    // sandbox in this stubbed env) — but never with the demo-disabled class.
    assert.notEqual(out.errorClass, "daytona_demo_mode_disabled");
  });

  it("does NOT fire the demo gate for a normal (non-demo) mode", async () => {
    delete process.env.LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES;
    const out = await mod.run({ payload: request("") });
    assert.notEqual(out.errorClass, "daytona_demo_mode_disabled");
  });
});

describe("sandboxLeakProbeCommand covers the full widened provider-cred set", () => {
  // The name list is NOT hand-copied here. It is read from the vendored canonical
  // artifact (internal/driver/testdata/sensitive-env-names.json, mirrored
  // byte-for-byte from meta-harness's contract/sensitive-env-names.json), so a
  // cred added to the contract — or to env.go's widened LOCAL-runner env, which
  // internal/driver/sensitive_env_contract_test.go holds equal to the artifact's
  // provider_credentials — must be enumerated by the probe too or this test fails.
  //
  // scripts/test-builtin-workflows.sh copies these tests into a temp staging dir
  // (so the bare @flue/runtime / @daytona/sdk specifiers resolve), which puts the
  // repo out of reach of a path relative to import.meta.url. That script exports
  // LOOM_REPO_ROOT for exactly this; the relative path is the fallback for running
  // `node --test` in-tree.
  const repoRoot = process.env.LOOM_REPO_ROOT
    ? path.resolve(process.env.LOOM_REPO_ROOT)
    : path.join(here, "../../..");
  const artifactPath = path.join(
    repoRoot,
    "internal/driver/testdata/sensitive-env-names.json",
  );
  let contract;
  try {
    contract = JSON.parse(fs.readFileSync(artifactPath, "utf8"));
  } catch (err) {
    throw new Error(
      `cannot read the vendored sensitive-env-name contract at ` +
        `${artifactPath} (${err.message}). ` +
        `It is vendored from meta-harness; restore it with ` +
        `scripts/sync-sensitive-env-names.sh --to <this repo> there.`,
    );
  }
  const PROBE_CRED_NAMES = [
    ...contract.runner_infra,
    ...contract.provider_credentials,
  ];

  it("enumerates every widened provider-credential name", () => {
    const cmd = mod.sandboxLeakProbeCommand();
    assert.equal(typeof cmd, "string");
    assert.ok(PROBE_CRED_NAMES.length > 0, "contract must not be empty");
    // The probe builds each env name from a name-parts array joined with "_" at
    // runtime, and the whole node script is wrapped by shellQuote(), which
    // escapes every single quote as '\''. Reconstruct the part-array literal
    // through the same escaping so we assert against the real command text.
    const shellQuoteInner = (s) => String(s).replace(/'/g, "'\\''");
    for (const name of PROBE_CRED_NAMES) {
      const partsLiteral = "[" + name.split("_").map((p) => `'${p}'`).join(",") + "]";
      assert.ok(
        cmd.includes(shellQuoteInner(partsLiteral)),
        `probe command must reference ${name} (${partsLiteral})`,
      );
    }
  });

  it("enumerates no names beyond the contract", () => {
    // An EXTRA stray name in the probe must fail too, not just a missing one.
    const cmd = mod.sandboxLeakProbeCommand();
    const emitted = cmd.match(/\[(?:'\\''[A-Z0-9]+'\\'',?)+\]/g) || [];
    assert.equal(
      emitted.length,
      PROBE_CRED_NAMES.length,
      `probe emits ${emitted.length} name(s); the contract declares ` +
        `${PROBE_CRED_NAMES.length}`,
    );
  });
});

describe("daytona-task-runner stack lineage parity (Stage 5)", () => {
  it("uses the injected lineage carrier as the canonical branch + base", () => {
    const req = {
      task_run_id: "tr-1",
      task_id: "T-B",
      input: {
        openPullRequest: true,
        lineage: { stackId: "epic:E", baseRef: "loom/stack/epic-E/T-A", outputBranch: "loom/stack/epic-E/T-B" },
      },
    };
    const plan = mod.deliveryPlan(req, { id: "T-B" }, "tr-1");
    assert.equal(plan.stacked, true, "lineage carrier forces stacked mode");
    assert.equal(plan.openPullRequest, true);
    assert.equal(plan.branch, "loom/stack/epic-E/T-B", "branch must be the canonical output branch");
    assert.equal(plan.baseBranch, "loom/stack/epic-E/T-A", "base must be the predecessor branch from the carrier");
    assert.equal(plan.stackId, "epic:E");
  });

  it("ignores a malformed lineage carrier (no outputBranch) and keeps legacy naming", () => {
    const req = { task_run_id: "tr-2", task_id: "T-C", input: { openPullRequest: true, lineage: { stackId: "epic:E" } } };
    const plan = mod.deliveryPlan(req, { id: "T-C" }, "tr-2");
    assert.notEqual(plan.branch, "", "still produces a branch");
    assert.ok(!plan.branch.startsWith("loom/stack/"), "no carrier => legacy taskBranchName, not canonical");
  });

  it("cloneCommand does a full clone (no --depth 1) so base SHAs are real", () => {
    const cmd = mod.cloneCommand("https://github.com/o/r.git", "/work/repo", "loom/stack/epic-E/T-A", "fixture-token");
    assert.ok(!cmd.includes("--depth"), "stacked clone must not be shallow: " + cmd);
    assert.ok(cmd.includes("clone"), "still a clone");
    assert.ok(cmd.includes("--branch"), "clones the predecessor base branch");
    assert.ok(!cmd.includes("fixture-token") && !cmd.includes("AUTHORIZATION"), "clone command contains no credential");
    assert.throws(() => mod.cloneCommand("https://user:fixture-token@github.com/o/r.git", "/work/repo", ""), /credentials/);
  });
});

describe("remote sandbox capture", () => {
  it("keeps two commits and edits while excluding secrets and ignored files", () => {
    const root = fs.mkdtempSync(path.join(stageRoot, "capture-"));
    const source = path.join(root, "source");
    const provider = path.join(root, "provider.git");
    const task = path.join(root, "task");
    fs.mkdirSync(source);
    const env = {
      ...process.env,
      GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null",
      GIT_AUTHOR_NAME: "Loom", GIT_AUTHOR_EMAIL: "loom@localhost",
      GIT_COMMITTER_NAME: "Loom", GIT_COMMITTER_EMAIL: "loom@localhost",
    };
    const git = (cwd, ...args) => execFileSync("git", args, { cwd, env, encoding: "utf8" }).trim();
    git(root, "init", "--bare", "-q", "-b", "main", provider);
    git(source, "init", "-q", "-b", "main");
    fs.writeFileSync(path.join(source, "readme"), "base\n");
    fs.writeFileSync(path.join(source, ".gitignore"), "*.log\n");
    git(source, "add", ".");
    git(source, "commit", "-qm", "base");
    const base = git(source, "rev-parse", "HEAD");
    git(source, "remote", "add", "origin", provider);
    git(source, "push", "-q", "origin", "main");
    execFileSync("sh", ["-c", mod.cloneCommand(provider, task, "")], { cwd: root, env });
    assert.equal(git(task, "config", "remote.origin.pushurl"), "loom-no-push://task-copy");
    assert.equal(git(task, "config", "--get-all", "credential.helper"), "");
    assert.throws(() => git(task, "push", "origin", "HEAD:refs/heads/direct"));
    assert.equal(mod.sandboxGitEnv({ GIT_CONFIG_PARAMETERS: "'credential.helper=unsafe'" }).GIT_CONFIG_PARAMETERS, "");
    assert.equal(mod.sandboxGitEnv().GIT_TERMINAL_PROMPT, "0");
    for (const name of ["one", "two"]) {
      fs.writeFileSync(path.join(task, name), name);
      git(task, "add", name);
      git(task, "commit", "-qm", name);
    }
    fs.writeFileSync(path.join(task, "readme"), "edited\n");
    fs.writeFileSync(path.join(task, ".env"), "SECRET=fixture\n");
    fs.writeFileSync(path.join(task, "scratch.log"), "ignored\n");
    const tokenPath = path.join(root, "scoped-token");
    fs.writeFileSync(tokenPath, "fixture-scoped-token");
    const ref = "refs/loom/ws/W/attempt/run-a1/capture";
    const input = { repo: task, workspace: "W", attempt: "run-a1", ref, proxyURL: provider, tokenPath };
    const output = execFileSync("node", ["-e", mod.remoteCaptureScript(), "--", JSON.stringify(input)], { env, encoding: "utf8" });
    const capture = JSON.parse(output);
    assert.equal(capture.complete, false);
    assert.equal(capture.retained, true);
    assert.ok(capture.entries.some((entry) => entry.path === ".env" && entry.class === "secret_suspect"));
    assert.ok(capture.entries.some((entry) => entry.path === "scratch.log" && entry.class === "listed"));
    assert.equal(git(provider, "rev-parse", ref), capture.captureSha);
    assert.equal(git(task, "rev-list", "--count", `${base}..${capture.captureSha}`), "3");
    assert.equal(git(task, "show", `${capture.captureSha}:readme`), "edited");
    assert.ok(!git(task, "ls-tree", "-r", "--name-only", capture.captureSha).includes(".env"));
    assert.equal(fs.existsSync(tokenPath), false);
  });

  it("reattaches a retained sandbox and retries with a fresh scoped token", async () => {
    const priorFetch = globalThis.fetch;
    const priorAPI = process.env.LOOM_TASK_RUN_API_URL;
    process.env.LOOM_TASK_RUN_API_URL = "http://loom.test";
    let tokenCount = 0;
    let pushCount = 0;
    let finalized = 0;
    const uploaded = [];
    const sandbox = {
      fs: { uploadFile: async (content, file) => uploaded.push({ content: String(content), file }) },
      process: { executeCommand: async (command) => {
        assert.ok(command.includes("capture.git"));
        pushCount++;
        return { exitCode: 0, result: JSON.stringify({
          captureSha: "a".repeat(40), treeHash: "b".repeat(40), complete: true,
          pushError: pushCount === 1 ? "connection refused" : "",
        }) };
      } },
    };
    const provider = { get: async (sandboxId) => {
      assert.equal(sandboxId, "retained-sandbox");
      return sandbox;
    } };
    globalThis.fetch = async (url) => {
      if (url.endsWith("/capture-token")) {
        tokenCount++;
        return { ok: true, json: async () => ({ token: `scoped-${tokenCount}`, attempt: "run-a1", ref: "refs/loom/ws/W/attempt/run-a1/capture" }) };
      }
      if (url.endsWith("/capture-pending")) {
        return { ok: false, json: async () => ({ message: "proxy has no retained pack" }) };
      }
      if (url.endsWith("/capture-finalize")) {
        finalized++;
        return { ok: true, json: async () => ({ changeId: "change", revision: 1 }) };
      }
      throw new Error("unexpected capture operation: " + url);
    };
    try {
      const input = { request: { workspace_key: "W", task_run_id: "run" }, repoUrl: "provider", repoDir: "/repo", baseSha: "c".repeat(40) };
      await assert.rejects(mod.retryRetainedCapture(await provider.get("retained-sandbox"), input, []), /proxy has no retained pack/);
      const recovered = await mod.retryRetainedCapture(await provider.get("retained-sandbox"), input, []);
      assert.equal(recovered.changeId, "change");
      assert.deepEqual(uploaded.map((item) => item.content), ["scoped-1", "scoped-2"]);
      assert.equal(pushCount, 2);
      assert.equal(finalized, 1);
    } finally {
      globalThis.fetch = priorFetch;
      if (priorAPI === undefined) delete process.env.LOOM_TASK_RUN_API_URL;
      else process.env.LOOM_TASK_RUN_API_URL = priorAPI;
    }
  });
});
