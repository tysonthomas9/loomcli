import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { after, afterEach, before, beforeEach, describe, it } from "node:test";

import { run } from "./session-eval-task-runner.ts";

const ENV_KEYS = [
  "LOOM_TASK_RUN_API_URL", "LOOM_WORKSPACE", "LOOM_TASK_RUN_ID", "LOOM_TASK_ID",
  "LOOM_TASK_RUN_NODE_ID", "LOOM_TASK_RUN_LEASE_ID", "LOOM_TASK_RUN_LEASE_TOKEN",
  "LOOM_TASK_RUN_FENCING_TOKEN", "LOOM_TASK_RUN_REQUEST_JSON", "PATH",
];

const FAKE_CODEX = `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
if (args[0] === "--version") process.exit(0);
const output = args[args.indexOf("--output-last-message") + 1];
fs.writeFileSync(output, JSON.stringify({ scores: {} }));
process.stdout.write(JSON.stringify({ type: "turn.completed", usage: { input_tokens: 70, output_tokens: 30 } }) + "\\n");
`;

let apiURL;
let requests;
let tmp;
let saved;
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
      requests.push({ url: requestURL.pathname, body });
      const reply = (payload) => {
        return new Response(JSON.stringify(payload), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      };
      if (requestURL.pathname.endsWith("/session-open")) return reply({ sessionId: "judge-session-1", attempt: 1 });
      if (requestURL.pathname.endsWith("/artifact-declare")) return reply({ artifactId: body.artifactId, durableStatus: "declared" });
      if (requestURL.pathname.includes("/artifacts/") && requestURL.pathname.endsWith("/content")) return reply({ artifactId: "transcript", durableStatus: "uploaded" });
      if (requestURL.pathname.endsWith("/artifact-finalize")) return reply({ artifactId: body.artifactId, durableStatus: "finalized" });
      if (requestURL.pathname.endsWith("/session-close")) return reply({ sessionId: body.sessionId, status: body.status });
      if (requestURL.pathname.endsWith("/heartbeat")) return reply({ status: "running" });
      return new Response("not found", { status: 404 });
  };
  apiURL = "http://task-run-api.test";
});

after(() => {
  globalThis.fetch = savedFetch;
});

beforeEach(() => {
  saved = Object.fromEntries(ENV_KEYS.map((key) => [key, process.env[key]]));
  requests = [];
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), "loom-session-eval-test-"));
  const bin = path.join(tmp, "bin");
  fs.mkdirSync(bin);
  const codex = path.join(bin, "codex");
  fs.writeFileSync(codex, FAKE_CODEX, { mode: 0o755 });
  process.env.PATH = bin + path.delimiter + saved.PATH;
  process.env.LOOM_TASK_RUN_API_URL = apiURL;
  process.env.LOOM_WORKSPACE = "WS";
  process.env.LOOM_TASK_RUN_ID = "judge-run-1";
  process.env.LOOM_TASK_ID = "session-eval-target";
  process.env.LOOM_TASK_RUN_NODE_ID = "node-1";
  process.env.LOOM_TASK_RUN_LEASE_ID = "lease-1";
  process.env.LOOM_TASK_RUN_LEASE_TOKEN = "lease-token";
  process.env.LOOM_TASK_RUN_FENCING_TOKEN = "1";
});

afterEach(() => {
  for (const key of ENV_KEYS) {
    if (saved[key] === undefined) delete process.env[key];
    else process.env[key] = saved[key];
  }
  fs.rmSync(tmp, { recursive: true, force: true });
});

describe("session-eval-task-runner task-plane sessions", () => {
  it("keeps deterministic preflight session-free", async () => {
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({ input: { kind: "session_eval_preflight" } });

    const out = await run();

    assert.equal(out.status, "completed");
    assert.equal(requests.filter((request) => request.url.endsWith("/session-open")).length, 0);
  });

  it("opens a judge session with linkage metadata and carries actual usage", async () => {
    process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
      task_run_id: "judge-run-1",
      input: {
        kind: "session_eval_judge", backend: "codex", model: "gpt-test",
        sessionId: "judged-session-1", promptVersion: "v1", judgeInput: "transcript",
      },
    });

    const out = await run();

    assert.equal(out.status, "completed");
    const open = requests.find((request) => request.url.endsWith("/session-open"));
    assert.deepEqual(open.body, {
      invocationKey: "judge", backend: "codex", model: "gpt-test", kind: "judge",
      metadata: { judged_session_id: "judged-session-1", judge_prompt_version: "v1" },
    });
    assert.equal(out.runtimeMetadata.judge_session_id, "judge-session-1");
    assert.deepEqual(JSON.parse(out.runtimeMetadata.eval_cost), { total_tokens: 100 });
    const close = requests.find((request) => request.url.endsWith("/session-close"));
    assert.equal(close.body.status, "completed");
  });
});
