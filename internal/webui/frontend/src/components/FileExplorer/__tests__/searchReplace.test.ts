import { describe, expect, it } from "vitest";

import {
  applyReplacement,
  createReplacementPreview,
  parseGlobText,
} from "../searchReplace";

describe("search replace helpers", () => {
  it("parses comma and newline separated globs", () => {
    expect(parseGlobText("src/*.go, docs/*.md\nREADME.md")).toEqual([
      "src/*.go",
      "docs/*.md",
      "README.md",
    ]);
  });

  it("applies literal case-insensitive replacements", () => {
    expect(
      applyReplacement("Needle needle", {
        query: "needle",
        replacement: "thread",
        regex: false,
        caseSensitive: false,
      }),
    ).toBe("thread thread");
  });

  it("creates per-file diff previews", () => {
    const preview = createReplacementPreview("src/main.go", "old\nsame", {
      query: "old",
      replacement: "new",
      regex: false,
      caseSensitive: true,
    });

    expect(preview?.path).toBe("src/main.go");
    expect(preview?.after).toBe("new\nsame");
    expect(preview?.diffLines).toEqual(["- old", "+ new"]);
  });
});
