import test from "node:test";
import assert from "node:assert/strict";

import { keyParams, pointerParams } from "./input-bridge.mjs";

test("maps normalized pointer coordinates into the remote viewport", () => {
  assert.deepEqual(
    pointerParams(
      { type: "mouseMoved", x: 0.25, y: 0.75, button: "none" },
      { width: 1920, height: 1080 },
    ),
    { type: "mouseMoved", x: 480, y: 810, button: "none" },
  );
});

test("preserves click and wheel details", () => {
  assert.deepEqual(
    pointerParams(
      { type: "mousePressed", x: 0.5, y: 0.5, button: "left", clickCount: 1 },
      { width: 1440, height: 900 },
    ),
    { type: "mousePressed", x: 720, y: 450, button: "left", clickCount: 1 },
  );

  assert.deepEqual(
    pointerParams(
      { type: "mouseWheel", x: 0.5, y: 0.25, deltaX: 3, deltaY: -12 },
      { width: 1440, height: 900 },
    ),
    { type: "mouseWheel", x: 720, y: 225, button: "none", deltaX: 3, deltaY: -12 },
  );
});

test("rejects invalid coordinates and event types", () => {
  assert.throws(
    () => pointerParams({ type: "mouseMoved", x: 1.1, y: 0 }, { width: 1, height: 1 }),
    /normalized pointer coordinates/,
  );
  assert.throws(
    () => pointerParams({ type: "touchStart", x: 0, y: 0 }, { width: 1, height: 1 }),
    /unsupported pointer event/,
  );
});

test("maps browser keyboard events into CDP key events", () => {
  assert.deepEqual(
    keyParams({ type: "keyDown", key: "A", code: "KeyA", text: "A", modifiers: 8 }),
    { type: "keyDown", key: "A", code: "KeyA", text: "A", modifiers: 8 },
  );
  assert.deepEqual(
    keyParams({ type: "keyUp", key: "A", code: "KeyA", modifiers: 8 }),
    { type: "keyUp", key: "A", code: "KeyA", modifiers: 8 },
  );
});

test("rejects malformed keyboard events", () => {
  assert.throws(() => keyParams({ type: "keypress", key: "a", code: "KeyA" }), /unsupported key event/);
  assert.throws(() => keyParams({ type: "keyDown", key: "a", code: "KeyA", modifiers: 32 }), /keyboard modifiers/);
});
