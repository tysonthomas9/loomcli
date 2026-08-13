import assert from "node:assert/strict";
import test from "node:test";
import { terminalRuntimeFailure } from "./runtime-status.ts";

test("terminalRuntimeFailure surfaces a recorded failed runtime", () => {
  const message = terminalRuntimeFailure({
    healthy: false,
    runtime: {
      status: "failed",
      error: "loom serve exited before becoming healthy: exit status 1",
    },
  });

  assert.equal(
    message,
    "loom serve exited before becoming healthy: exit status 1",
  );
});

test("terminalRuntimeFailure keeps starting and stopped states pollable", () => {
  assert.equal(
    terminalRuntimeFailure({
      healthy: false,
      runtime: { status: "starting", error: "connection refused" },
    }),
    "",
  );
  assert.equal(
    terminalRuntimeFailure({
      healthy: false,
      runtime: { status: "stopped" },
    }),
    "",
  );
});

test("terminalRuntimeFailure ignores only the pre-start failed snapshot", () => {
  const stale = {
    status: "failed",
    pid: 98801,
    serve_pid: 98803,
    started_at: "2026-08-13T15:20:26Z",
    updated_at: "2026-08-13T15:20:49Z",
    error: "old generation failed",
  };

  assert.equal(
    terminalRuntimeFailure({ healthy: false, runtime: stale }, stale),
    "",
  );
  assert.equal(
    terminalRuntimeFailure(
      {
        healthy: false,
        runtime: {
          ...stale,
          pid: 99901,
          serve_pid: 99903,
          started_at: "2026-08-13T15:25:00Z",
          updated_at: "2026-08-13T15:25:20Z",
          error: "replacement generation failed",
        },
      },
      stale,
    ),
    "replacement generation failed",
  );
});
