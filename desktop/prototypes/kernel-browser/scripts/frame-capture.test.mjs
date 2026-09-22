import test from "node:test";
import assert from "node:assert/strict";
import { captureFrame } from "./frame-capture.mjs";

test("captures a bounded JPEG frame for WebKit-safe rendering", async () => {
  const calls = [];
  const result = await captureFrame(async (method, params) => {
    calls.push({ method, params });
    return { data: "encoded-frame" };
  });

  assert.deepEqual(calls, [{
    method: "Page.captureScreenshot",
    params: { format: "jpeg", quality: 70, fromSurface: true },
  }]);
  assert.deepEqual(result, { mimeType: "image/jpeg", image: "encoded-frame" });
});
