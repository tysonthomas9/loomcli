import { describe, expect, it } from "vitest";

import { parseSessionDiff } from "../SessionDiffViewer";

describe("parseSessionDiff", () => {
  it("splits multi-file patches and preserves per-file status and stats", () => {
    const files = parseSessionDiff(
      [
        "diff --git a/created.txt b/created.txt",
        "new file mode 100644",
        "--- /dev/null",
        "+++ b/created.txt",
        "@@ -0,0 +1,2 @@",
        "+one",
        "+two",
        "diff --git a/removed.txt b/removed.txt",
        "deleted file mode 100644",
        "--- a/removed.txt",
        "+++ /dev/null",
        "@@ -1 +0,0 @@",
        "-gone",
      ].join("\n"),
    );

    expect(files).toHaveLength(2);
    expect(files[0]).toMatchObject({
      path: "created.txt",
      status: "A",
      patch: { additions: 2, deletions: 0 },
    });
    expect(files[1]).toMatchObject({
      path: "removed.txt",
      status: "D",
      patch: { additions: 0, deletions: 1 },
    });
  });

  it("recognizes renamed and binary files", () => {
    const files = parseSessionDiff(
      [
        "diff --git a/old.bin b/new.bin",
        "similarity index 100%",
        "rename from old.bin",
        "rename to new.bin",
        "Binary files a/old.bin and b/new.bin differ",
      ].join("\n"),
    );

    expect(files[0]).toMatchObject({
      path: "new.bin",
      status: "R",
      patch: { is_binary: true },
    });
  });
});
