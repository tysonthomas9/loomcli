import test from "node:test";
import assert from "node:assert/strict";

import { keyParams, pointerParams } from "./input-bridge.mjs";

const browserGeometry = {
  screen: { width: 1920, height: 1080 },
  window: { outerWidth: 1920, outerHeight: 1080, innerWidth: 1920, innerHeight: 937, screenX: 0, screenY: 0 },
};

test("maps the visible streamed desktop through letterboxing and browser chrome", () => {
  assert.deepEqual(
    pointerParams(
      {
        type: "mouseMoved",
        surfaceX: 328.55,
        surfaceY: 237.05,
        surfaceWidth: 932,
        surfaceHeight: 570,
        button: "none",
      },
      browserGeometry,
    ),
    { type: "mouseMoved", x: 677, y: 298, button: "none" },
  );
});

test("preserves click and wheel details", () => {
  assert.deepEqual(
    pointerParams(
      { type: "mousePressed", surfaceX: 466, surfaceY: 237.05, surfaceWidth: 932, surfaceHeight: 570, button: "left", clickCount: 1 },
      browserGeometry,
    ),
    { type: "mousePressed", x: 960, y: 298, button: "left", clickCount: 1 },
  );

  assert.deepEqual(
    pointerParams(
      { type: "mouseWheel", surfaceX: 466, surfaceY: 237.05, surfaceWidth: 932, surfaceHeight: 570, deltaX: 3, deltaY: -12 },
      browserGeometry,
    ),
    { type: "mouseWheel", x: 960, y: 298, button: "none", deltaX: 3, deltaY: -12 },
  );
});

test("preserves held-button state while dragging", () => {
  assert.deepEqual(
    pointerParams(
      { type: "mouseMoved", surfaceX: 466, surfaceY: 237.05, surfaceWidth: 932, surfaceHeight: 570, button: "left", buttons: 1, modifiers: 8 },
      browserGeometry,
    ),
    { type: "mouseMoved", x: 960, y: 298, button: "left", buttons: 1, modifiers: 8 },
  );
});

test("clamps a held drag and release to the page edge", () => {
  assert.deepEqual(
    pointerParams(
      { type: "mouseMoved", surfaceX: 466, surfaceY: 10, surfaceWidth: 932, surfaceHeight: 570, button: "left", buttons: 1 },
      browserGeometry,
    ),
    { type: "mouseMoved", x: 960, y: 0, button: "left", buttons: 1 },
  );
  assert.deepEqual(
    pointerParams(
      { type: "mouseReleased", surfaceX: 466, surfaceY: 10, surfaceWidth: 932, surfaceHeight: 570, button: "left", buttons: 0 },
      browserGeometry,
    ),
    { type: "mouseReleased", x: 960, y: 0, button: "left" },
  );
});

test("rejects invalid surface coordinates and event types", () => {
  assert.throws(
    () => pointerParams({ type: "mouseMoved", surfaceX: 2, surfaceY: 0, surfaceWidth: 1, surfaceHeight: 1 }, browserGeometry),
    /pointer surface coordinates/,
  );
  assert.throws(
    () => pointerParams({ type: "touchStart", surfaceX: 0, surfaceY: 0, surfaceWidth: 1, surfaceHeight: 1 }, browserGeometry),
    /unsupported pointer event/,
  );
});

test("ignores clicks on letterboxing or browser chrome", () => {
  assert.equal(
    pointerParams({ type: "mousePressed", surfaceX: 466, surfaceY: 10, surfaceWidth: 932, surfaceHeight: 570, button: "left" }, browserGeometry),
    null,
  );
  assert.equal(
    pointerParams({ type: "mousePressed", surfaceX: 466, surfaceY: 60, surfaceWidth: 932, surfaceHeight: 570, button: "left" }, browserGeometry),
    null,
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
