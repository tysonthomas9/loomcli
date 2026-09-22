import test from "node:test";
import assert from "node:assert/strict";
import { humanInputAdvancesEpoch } from "./control-epoch.mjs";

test("meaningful human input advances control while passive pointer events do not", () => {
  assert.equal(humanInputAdvancesEpoch({ type: "mouseMoved" }), false);
  assert.equal(humanInputAdvancesEpoch({ type: "mouseReleased" }), false);
  assert.equal(humanInputAdvancesEpoch({ type: "mousePressed" }), true);
  assert.equal(humanInputAdvancesEpoch({ type: "mouseWheel" }), true);
  assert.equal(humanInputAdvancesEpoch({ type: "keyDown" }), true);
  assert.equal(humanInputAdvancesEpoch({ type: "keyUp" }), false);
});
