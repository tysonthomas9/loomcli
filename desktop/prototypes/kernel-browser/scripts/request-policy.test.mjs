import test from "node:test";
import assert from "node:assert/strict";

import { originAllowed } from "./request-policy.mjs";

test("permits the POC origins and rejects other browser origins", () => {
  assert.equal(originAllowed(undefined), true);
  assert.equal(originAllowed("http://127.0.0.1:1421"), true);
  assert.equal(originAllowed("tauri://localhost"), true);
  assert.equal(originAllowed("https://attacker.example"), false);
});
