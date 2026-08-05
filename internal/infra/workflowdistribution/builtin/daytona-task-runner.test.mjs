import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { after, before, beforeEach, describe, it } from "node:test";

const here = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.join(here, "daytona-task-runner.ts");

let stageRoot;
let mod;
let sdk;

function stub(dir, relFile, contents) {
  const file = path.join(dir, relFile);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, contents);
}

before(async () => {
  stageRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-daytona-stage-"));
  const flue = path.join(stageRoot, "node_modules", "@flue", "runtime");
  stub(
    flue,
    "index.js",
    "export const defineAgent = (fn) => ({ __agent: fn });\nexport const defineWorkflow = (def) => def;\n",
  );
  fs.writeFileSync(
    path.join(flue, "package.json"),
    JSON.stringify({ name: "@flue/runtime", type: "module", main: "index.js" }),
  );

  const loom = path.join(stageRoot, "node_modules", "@loom", "sdk");
  stub(loom, "runner.js", `
export const DaytonaProviderSchemaV1 = "daytona-task-run-execution.v1";
export class LoomAPIError extends Error {
  constructor(message, options = {}) {
    super(message);
    this.code = options.code || "";
    this.status = options.status || 0;
  }
}
let current;
export function __setClient(value) { current = value; }
export class TaskRunClient {
  static fromEnv() {
    if (current instanceof Error) throw current;
    return current;
  }
}
`);
  fs.writeFileSync(
    path.join(loom, "package.json"),
    JSON.stringify({
      name: "@loom/sdk",
      type: "module",
      exports: { "./runner": "./runner.js" },
    }),
  );
  const copy = path.join(stageRoot, "daytona-task-runner.ts");
  fs.copyFileSync(SOURCE, copy);
  sdk = await import(pathToFileURL(path.join(loom, "runner.js")).href);
  mod = await import(pathToFileURL(copy).href);
});

after(() => {
  fs.rmSync(stageRoot, { recursive: true, force: true });
});

beforeEach(() => {
  sdk.__setClient({
    getTask: async () => ({ id: "T-d", title: "Test Daytona broker" }),
    daytona: {
      execute: async () => ({
        schemaVersion: sdk.DaytonaProviderSchemaV1,
        status: "completed",
        exitCode: 0,
        usage: {},
        sandbox: { provider: "daytona", id: "sandbox-opaque" },
      }),
    },
  });
});

function request(mode = "") {
  return {
    task_run_id: "tr-d",
    task_id: "T-d",
    runner: "daytona-task-runner",
    input: {
      repoUrl: "https://github.com/octocat/Hello-World.git",
      mode,
    },
  };
}

describe("daytona-task-runner opaque-provider boundary", () => {
  it("submits one strict secret-free intent and maps the opaque receipt", async () => {
    let intent;
    sdk.__setClient({
      getTask: async () => ({ id: "T-d", title: "Implement the focused change" }),
      daytona: {
        execute: async (input) => {
          intent = input;
          return {
            schemaVersion: sdk.DaytonaProviderSchemaV1,
            status: "completed",
            exitCode: 0,
            logs: "done",
            usage: { inputTokens: 2, outputTokens: 1 },
            sandbox: {
              provider: "daytona",
              id: "sandbox-opaque",
              workDir: "/home/daytona",
              cwd: "/tmp/loom-daytona-task-repo",
              repoRef: "abc",
            },
            patch: { content: "diff --git a/a b/a\n", baseRef: "main", headSha: "abc" },
          };
        },
      },
    });

    const out = await mod.run({ payload: request() });
    assert.equal(out.status, "completed");
    assert.equal(out.runtimeMetadata.runtime_strategy, "host-opaque-provider-broker");
    assert.equal(out.runtimeMetadata.sandbox_id, "sandbox-opaque");
    assert.equal(out.runtimeMetadata.daytona_workdir, "/home/daytona");
    assert.equal(out.runtimeMetadata.daytona_repo_dir, "/tmp/loom-daytona-task-repo");
    assert.equal(out.patch, "diff --git a/a b/a\n");
    assert.deepEqual(Object.keys(intent).sort(), [
      "backend",
      "baseRef",
      "delivery",
      "mode",
      "model",
      "repositoryUrl",
      "schemaVersion",
      "taskPrompt",
    ]);
    assert.equal("credentials" in intent, false);
    assert.equal("env" in intent, false);
    assert.equal("headers" in intent, false);
    assert.deepEqual(Object.keys(intent.delivery).sort(), [
      "baseBranch",
      "draft",
      "openPullRequest",
      "outputBranch",
    ]);
  });

  it("maps a lease refusal without reflecting credential-shaped request input", async () => {
    const sentinel = "phase5-credential-sentinel";
    sdk.__setClient({
      getTask: async () => ({ id: "T-d", title: "Test" }),
      daytona: {
        execute: async () => {
          throw new sdk.LoomAPIError("lease denied", { code: "lease_denied", status: 401 });
        },
      },
    });
    const payload = request();
    payload.input.credentials = { daytona: sentinel, github: sentinel };
    payload.input.env = { DAYTONA_API_KEY: sentinel };

    const out = await mod.run({ payload });
    assert.equal(out.errorClass, "daytona_provider_lease_denied");
    assert.equal(JSON.stringify(out).includes(sentinel), false);
  });

  it("fails closed when the TaskRun client or repository intent is unavailable", async () => {
    sdk.__setClient(new Error("TaskRun facade missing"));
    const unavailable = await mod.run({ payload: request() });
    assert.equal(unavailable.errorClass, "daytona_task_context_failed");

    sdk.__setClient({
      getTask: async () => ({ id: "T-d" }),
      daytona: { execute: async () => assert.fail("broker should not be called") },
    });
    const missingRepo = await mod.run({ payload: { ...request(), input: {} } });
    assert.equal(missingRepo.errorClass, "daytona_repo_url_missing");
  });
});
