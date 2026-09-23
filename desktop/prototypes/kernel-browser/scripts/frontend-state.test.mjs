import test from "node:test";
import assert from "node:assert/strict";

import { agentBrowserCommand, createQueuedActionState, mergeBrowserApps } from "./frontend-state.mjs";

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

test("agent browser command targets the selected isolated browser", () => {
  assert.equal(
    agentBrowserCommand("app-c"),
    "desktop/prototypes/kernel-browser/scripts/agent-browser-control.sh app-c snapshot -i",
  );
});

test("agent browser command rejects an invalid browser id", () => {
  assert.throws(() => agentBrowserCommand("../../other"), /invalid browser app id/);
});

test("browser discovery restores dynamic apps without duplicating initial apps", () => {
  const merged = mergeBrowserApps(
    [{ id: "app-a", name: "Research" }, { id: "app-b", name: "Operations" }],
    [{ id: "app-d", name: "Browser 4" }, { id: "app-a", name: "Research" }, { id: "app-c", name: "Browser 3" }],
  );

  assert.deepEqual(merged.map((app) => app.id), ["app-a", "app-b", "app-c", "app-d"]);
});
