import { describe, expect, it } from "vitest";

import { rankQuickOpenPaths } from "../quickOpen";

describe("quick open ranking", () => {
  it("lets MRU dominate short queries", () => {
    const ranked = rankQuickOpenPaths(
      ["src/app.ts", "docs/api.md", "packages/archive.ts"],
      "ap",
      ["docs/api.md"],
    );

    expect(ranked[0]?.path).toBe("docs/api.md");
  });

  it("uses fuzzy subsequence matches for longer queries", () => {
    const ranked = rankQuickOpenPaths(
      ["src/fileBrowserStore.tsx", "src/boring.ts", "README.md"],
      "fbs",
      [],
    );

    expect(ranked.map((match) => match.path)).toContain(
      "src/fileBrowserStore.tsx",
    );
    expect(ranked.map((match) => match.path)).not.toContain("README.md");
  });
});
