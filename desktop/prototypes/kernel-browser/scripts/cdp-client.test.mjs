import test from "node:test";
import assert from "node:assert/strict";

import { chooseVisiblePage, createPendingCommands } from "./cdp-client.mjs";

test("chooses the visible HTTP page rather than the first background target", async () => {
  const targets = [
    { targetId: "background", type: "page", url: "http://fixture/a" },
    { targetId: "foreground", type: "page", url: "http://fixture/b" },
  ];
  const selected = await chooseVisiblePage(targets, async (target) => target.targetId === "foreground");
  assert.equal(selected.targetId, "foreground");
});

test("chooses a visible initial browser page so navigation can bootstrap", async () => {
  const targets = [{ targetId: "new-tab", type: "page", url: "chrome://newtab/" }];
  const selected = await chooseVisiblePage(targets, async () => true);
  assert.equal(selected.targetId, "new-tab");
});

test("rejects every pending CDP command when the socket closes", async () => {
  const pending = createPendingCommands();
  const command = pending.add(7, 1_000);
  pending.rejectAll(new Error("CDP websocket closed"));
  await assert.rejects(command, /closed/);
  assert.equal(pending.size(), 0);
});

test("times out a CDP command that never receives a response", async () => {
  const pending = createPendingCommands();
  await assert.rejects(pending.add(9, 5), /timed out/);
  assert.equal(pending.size(), 0);
});
