import test from "node:test";
import assert from "node:assert/strict";

import { nextBrowserDefinition } from "./browser-registry.mjs";

test("allocates the next isolated browser with deterministic loopback ports", () => {
  const browser = nextBrowserDefinition([
    { id: "app-a" },
    { id: "app-b" },
  ]);

  assert.deepEqual(browser, {
    id: "app-c",
    name: "Browser 3",
    livePort: 63080,
    cdpPort: 63222,
    webdriverPort: 63224,
    apiPort: 63101,
    mediaPort: 56200,
    epoch: 0,
  });
});

test("does not reuse a browser slot after a gap", () => {
  const browser = nextBrowserDefinition([
    { id: "app-a" },
    { id: "app-c" },
  ]);

  assert.equal(browser.id, "app-d");
  assert.equal(browser.cdpPort, 64222);
});

test("caps the throwaway POC before its port layout becomes ambiguous", () => {
  assert.throws(
    () => nextBrowserDefinition(Array.from({ length: 8 }, (_, index) => ({ id: `app-${String.fromCharCode(97 + index)}` }))),
    /supports at most 8 browser apps/,
  );
});
