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
