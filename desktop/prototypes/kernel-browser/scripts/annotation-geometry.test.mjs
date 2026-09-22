import test from "node:test";
import assert from "node:assert/strict";

import { annotationGeometry } from "./annotation-geometry.mjs";

test("converts refreshed viewport bounds to document screenshot coordinates", () => {
  assert.deepEqual(
    annotationGeometry(
      { x: 20, y: 30, width: 200, height: 80 },
      { pageLeft: 5, pageTop: 500 },
    ),
    {
      bounds: { x: 20, y: 30, width: 200, height: 80 },
      documentBounds: { x: 25, y: 530, width: 200, height: 80 },
    },
  );
});
