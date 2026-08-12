import { describe, expect, it } from "vitest";

import { argPreview, argPreviewFromJSON, truncate } from "../toolPreview";

describe("truncate", () => {
  it("leaves short values intact", () => {
    expect(truncate("short", 10)).toBe("short");
  });

  it("elides at the limit", () => {
    expect(truncate("abcdef", 4)).toBe("abc…");
  });
});

describe("argPreview", () => {
  it("returns nothing for absent or non-object input", () => {
    expect(argPreview(undefined)).toBe("");
    expect(argPreview(null)).toBe("");
    expect(argPreview(42)).toBe("");
  });

  it("previews a raw string input", () => {
    expect(argPreview("ls -la")).toBe("ls -la");
  });

  it("prefers the most specific key", () => {
    expect(argPreview({ command: "ls", file_path: "src/a.ts" })).toBe(
      "src/a.ts",
    );
  });

  it("falls back to nothing when no salient key is present", () => {
    expect(argPreview({ unrelated: "value" })).toBe("");
  });
});

describe("argPreviewFromJSON", () => {
  it("previews the salient key of serialized JSON", () => {
    expect(argPreviewFromJSON('{"command":"git diff"}')).toBe("git diff");
  });

  it("previews raw text that is not JSON", () => {
    expect(argPreviewFromJSON("git   diff\nHEAD")).toBe("git diff HEAD");
  });

  it("falls back to the raw text when JSON carries no salient key", () => {
    expect(argPreviewFromJSON('{"other":1}')).toBe('{"other":1}');
  });

  it("falls back to the raw text when JSON does not parse", () => {
    expect(argPreviewFromJSON("{not json")).toBe("{not json");
  });

  it("returns nothing for empty input", () => {
    expect(argPreviewFromJSON(undefined)).toBe("");
    expect(argPreviewFromJSON("   ")).toBe("");
  });
});
