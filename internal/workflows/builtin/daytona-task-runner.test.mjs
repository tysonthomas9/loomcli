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
const ENV_KEYS = [
  "LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES",
  "DAYTONA_TASK_MODE",
  "LOOM_TASK_RUN_REQUEST_JSON",
  "DAYTONA_CREDENTIAL_FILE",
  "GITHUB_TOKEN_FILE",
  "LOOM_FLUE_AGENT_MODEL",
  "KEEP_DAYTONA_SANDBOX",
];

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
  stub(daytona, "index.js", [
    "const state = () => globalThis.__loomDaytonaTestState;",
    "export class Daytona {",
    "  constructor(config) { this.config = config; }",
    "  async create(input) { return state().createSandbox(input, this.config); }",
    "}",
    "export default { Daytona };",
  ].join("\n") + "\n");
  fs.writeFileSync(path.join(daytona, "package.json"), JSON.stringify({ name: "@daytona/sdk", type: "module", main: "index.js" }));
  stub(flue, "index.js", [
    "export const defineAgent = (fn) => ({ __agent: fn });",
    "export const defineWorkflow = (def) => def;",
    "export const createAgent = (fn) => ({ __agent: fn });",
    "export const createSandboxSessionEnv = (api, cwd) => ({ api, cwd });",
    "export const registerProvider = () => {};",
  ].join("\n") + "\n");
  stub(flue, "internal.js", [
    "const state = () => globalThis.__loomDaytonaTestState;",
    "export class InMemorySessionStore {}",
    "export const resolveModel = (model) => ({ provider: 'test-provider', model });",
    "export const createFlueContext = (options) => state().createFlueContext(options);",
  ].join("\n") + "\n");
  fs.writeFileSync(path.join(flue, "package.json"), JSON.stringify({
    name: "@flue/runtime",
    type: "module",
    exports: { ".": "./index.js", "./internal": "./internal.js" },
  }));
  stub(loom, "runner.js", [
    "export class TaskRunClient {",
    "  static fromEnv() {",
    "    const state = globalThis.__loomDaytonaTestState;",
    "    if (!state) throw new Error('stub');",
    "    return state.taskRunClientFromEnv();",
    "  }",
    "}",
  ].join("\n") + "\n");
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
  delete globalThis.__loomDaytonaTestState;
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

function runnerRequest(input = {}) {
  return {
    task_run_id: "tr-d",
    task_id: "T-d",
    runner: "daytona-task-runner",
    input: { repoUrl: "https://github.com/o/r.git", ...input },
  };
}

function makeRunnerState({ clientAvailable = true, promptError = "", invocationRuntimeMetadata = {} } = {}) {
  const state = {
    commands: [],
    invocationSpecs: [],
    promptCalls: 0,
    sessionOpens: 0,
    sessionOpensDuringCommand: 0,
    commandDepth: 0,
  };
  const commandResponse = (command) => {
    if (command.includes("rev-parse --short HEAD")) return { exitCode: 0, result: "abc123\n" };
    if (command.includes("diff --stat")) return { exitCode: 0, result: " file.txt | 1 +\n" };
    if (command.includes("diff --binary")) return { exitCode: 0, result: "diff --git a/file.txt b/file.txt\n+changed\n" };
    if (command.includes("process.env") && command.includes("filter")) return { exitCode: 0, result: "0\n" };
    return { exitCode: 0, result: "" };
  };
  state.sandbox = {
    id: "sandbox-test",
    getWorkDir: async () => "/work",
    delete: async () => {},
    fs: {},
    process: {
      executeCommand: async (command) => {
        state.commandDepth++;
        state.commands.push(command);
        try {
          await Promise.resolve();
          return commandResponse(command);
        } finally {
          state.commandDepth--;
        }
      },
    },
  };
  state.createSandbox = async () => state.sandbox;
  state.createFlueContext = (options) => {
    let eventCallback = () => {};
    return {
      setEventCallback(callback) {
        eventCallback = callback;
      },
      initializeRootHarness() {
        if (options.id.endsWith("-setup")) {
          return {
            shell: async (command) => {
              const result = await state.sandbox.process.executeCommand(command);
              return { stdout: result.result || "", stderr: "", exitCode: result.exitCode || 0 };
            },
          };
        }
        return {
          session: async () => ({
            prompt: async (prompt) => {
              state.promptCalls++;
              eventCallback({ type: "turn_request", purpose: "agent", input: { messages: [{ role: "user", content: prompt }] } });
              if (promptError) throw new Error(promptError);
              return { text: "done" };
            },
          }),
        };
      },
    };
  };

  const artifactClient = {
    declare: async (spec) => ({
      id: spec.id,
      upload: async () => {},
      finalize: async () => {},
    }),
  };
  state.client = {
    getTask: async () => ({ id: "T-d", title: "Test task" }),
    runtimeCredentials: {
      get: async ({ provider }) => ({ value: provider === "github" ? "github-token" : "daytona-token" }),
    },
    artifacts: artifactClient,
    agent: {
      exec: {
        invoke: async (spec) => {
          state.sessionOpens++;
          if (state.commandDepth > 0) state.sessionOpensDuringCommand++;
          state.invocationSpecs.push(spec);
          try {
            const response = await spec.invoke();
            return {
              response,
              invokeError: null,
              session: { id: "tr-d-a1-agent" },
              runtimeMetadata: invocationRuntimeMetadata,
            };
          } catch (error) {
            return {
              response: null,
              invokeError: error.message,
              session: { id: "tr-d-a1-agent" },
              runtimeMetadata: invocationRuntimeMetadata,
            };
          }
        },
      },
    },
  };
  state.taskRunClientFromEnv = () => {
    if (!clientAvailable) throw new Error("task-run API unavailable");
    return state.client;
  };
  return state;
}

async function runWithState(state, payload) {
  globalThis.__loomDaytonaTestState = state;
  process.env.LOOM_FLUE_AGENT_MODEL = "test-provider/test-model";
  process.env.DAYTONA_CREDENTIAL_FILE = path.join(stageRoot, "daytona-key");
  fs.writeFileSync(process.env.DAYTONA_CREDENTIAL_FILE, "daytona-token\n", { mode: 0o600 });
  try {
    return await mod.run({ payload });
  } finally {
    delete globalThis.__loomDaytonaTestState;
  }
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

describe("daytona-task-runner agent invoke boundary (LOOMCLI-136)", () => {
  it("runs real clone/checkout/diff command paths without opening command sessions", async () => {
    const state = makeRunnerState();
    const out = await runWithState(state, runnerRequest({ openPullRequest: true }));

    assert.equal(out.status, "failed", "the fake stops honestly at PR publication after diff collection");
    assert.ok(
      state.commands.some((command) => command.includes(" clone")),
      `run() must execute clone through Daytona; class=${out.errorClass} message=${out.errorMessage}`,
    );
    assert.ok(state.commands.some((command) => command.includes("checkout -B")), "run() must execute checkout through Daytona");
    assert.ok(state.commands.some((command) => command.includes("diff --binary")), "run() must execute diff through Daytona");
    assert.equal(state.sessionOpensDuringCommand, 0, "deterministic executeCommand calls must never open a session");
    assert.equal(state.sessionOpens, 1, "the single prompt is the only session open in the full run path");
    assert.equal(state.promptCalls, 1);
    assert.equal(state.invocationSpecs.length, 1);
    assert.equal(state.invocationSpecs[0].invocationKey, "agent");
    assert.equal(state.invocationSpecs[0].backend, "codex");
  });

  it("propagates degraded invocation metadata into a failure result", async () => {
    const state = makeRunnerState({
      promptError: "prompt failed",
      invocationRuntimeMetadata: {
        observability_degraded: "true",
        observability_degraded_code: "artifact_upload_failed",
      },
    });
    const out = await runWithState(state, runnerRequest());

    assert.equal(out.status, "failed");
    assert.equal(out.errorClass, "daytona_agent_invoke_failed");
    assert.equal(out.runtimeMetadata.observability_degraded, "true");
    assert.equal(out.runtimeMetadata.observability_degraded_code, "artifact_upload_failed");
  });

  it("runs the prompt directly and surfaces an inline patch when the task-run client is unavailable", async () => {
    const state = makeRunnerState({ clientAvailable: false });
    const out = await runWithState(state, runnerRequest());

    assert.equal(out.status, "completed");
    assert.equal(state.promptCalls, 1, "work must still run without the task-run API");
    assert.equal(state.sessionOpens, 0, "the null-client path cannot open an AgentSession");
    assert.match(out.patch, /diff --git/);
    assert.equal(out.runtimeMetadata.observability_degraded, "true");
    assert.equal(out.runtimeMetadata.observability_degraded_code, "taskrunapi_unavailable");
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
