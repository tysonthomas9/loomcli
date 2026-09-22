import test from "node:test";
import assert from "node:assert/strict";

import { createQueuedActionState } from "./frontend-state.mjs";

test("queued action keeps its app and epoch across tab changes", () => {
  const state = createQueuedActionState();
  state.queue("app-a", 0);
  state.select("app-b");
  state.select("app-a");

  assert.deepEqual(state.current(), { appId: "app-a", epoch: 0 });
});

test("takeover does not refresh a queued action", () => {
  const state = createQueuedActionState();
  state.queue("app-a", 0);
  state.setEpoch("app-a", 1);

  assert.deepEqual(state.current(), { appId: "app-a", epoch: 0 });
});
