import { describe, expect, it } from "vitest";

import {
  buildFolderGitDecorations,
  gitDecorationForStatus,
  resolveTreeDropMove,
} from "../gitDecorations";

describe("git decorations", () => {
  it("maps porcelain XY codes to decoration kinds", () => {
    expect(gitDecorationForStatus(" M")).toMatchObject({ kind: "modified" });
    expect(gitDecorationForStatus("A ")).toMatchObject({ kind: "added" });
    expect(gitDecorationForStatus("??")).toMatchObject({ kind: "added" });
    expect(gitDecorationForStatus(" D")).toMatchObject({ kind: "deleted" });
    expect(gitDecorationForStatus("UU")).toMatchObject({
      kind: "conflict",
      conflict: true,
    });
    expect(gitDecorationForStatus("AU")).toMatchObject({
      kind: "conflict",
      conflict: true,
    });
  });

  it("bubbles folder status dots and conflict state to parents", () => {
    const folders = buildFolderGitDecorations({
      "src/app/main.go": " M",
      "src/conflict.go": "UU",
    });

    expect(folders.get("src")).toEqual({ changed: true, conflict: true });
    expect(folders.get("src/app")).toEqual({
      changed: true,
      conflict: false,
    });
  });

  it("resolves valid tree drops and guards self or descendant drops", () => {
    expect(resolveTreeDropMove("src/a.go", "pkg")).toEqual({
      from: "src/a.go",
      to: "pkg/a.go",
    });
    expect(resolveTreeDropMove("src", "src")).toBeNull();
    expect(resolveTreeDropMove("src", "src/nested")).toBeNull();
    expect(resolveTreeDropMove("src/a.go", "src")).toBeNull();
  });
});
