import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
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
  // Must mirror env.go trustedLocalProviderCredentials. If a new cred is added
  // to the LOCAL-runner env, the probe must enumerate it too or this test fails.
  const PROBE_CRED_NAMES = [
    "DAYTONA_API_KEY",
    "GITHUB_TOKEN",
    "GH_TOKEN",
    "CODEX_HOME",
    "LOOM_TASK_RUN_LEASE_TOKEN",
    "LOOM_DRIVER_TASK_RUNNER_CMD_JSON",
    "ANTHROPIC_API_KEY",
    "OPENAI_API_KEY",
    "CODEX_API_KEY",
    "GEMINI_API_KEY",
    "GOOGLE_API_KEY",
    "GOOGLE_APPLICATION_CREDENTIALS",
    "CURSOR_API_KEY",
  ];

  it("enumerates every widened provider-credential name", () => {
    const cmd = mod.sandboxLeakProbeCommand();
    assert.equal(typeof cmd, "string");
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
});

describe("daytona-task-runner stack lineage parity (Stage 5)", () => {
  it("uses a frozen pull_request plan without the legacy flag", () => {
    const req = {
      task_run_id: "tr-policy",
      task_id: "T-POLICY",
      input: { deliveryPlan: { plan_id: "plan-1", requirement: "pull_request" } },
    };
    const plan = mod.deliveryPlan(req, { id: "T-POLICY" }, "tr-policy");
    assert.equal(plan.openPullRequest, true);
  });

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
    const cmd = mod.cloneCommand("https://github.com/o/r.git", "/work/repo", "loom/stack/epic-E/T-A", "");
    assert.ok(!cmd.includes("--depth"), "stacked clone must not be shallow: " + cmd);
    assert.ok(cmd.includes("clone"), "still a clone");
    assert.ok(cmd.includes("--branch"), "clones the predecessor base branch");
  });
});
