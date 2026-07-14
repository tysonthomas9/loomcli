import { describe, expect, it } from "vitest";

import {
  mapWorkspaceIndexPathToCheckout,
  tabIdentityKey,
} from "../fileExplorerRefs";

describe("fileExplorerRefs", () => {
  it("maps worktree index paths to agent checkout refs", () => {
    const mapped = mapWorkspaceIndexPathToCheckout(
      "worktrees/source-repo/local-coder/src/main.ts",
      [{ scope: "agent", target: "local-coder", repo: "source-repo" }],
    );

    expect(mapped).toEqual({
      ref: { scope: "agent", target: "local-coder", repo: "source-repo" },
      path: "src/main.ts",
    });
    expect(tabIdentityKey(mapped)).toBe(
      "agent:local-coder:source-repo\u001fsrc/main.ts",
    );
  });

  it("maps shared repo index paths to repo checkout refs", () => {
    expect(
      mapWorkspaceIndexPathToCheckout("source-repo/README.md", [
        { scope: "repo", target: "source-repo" },
      ]),
    ).toEqual({
      ref: { scope: "repo", target: "source-repo" },
      path: "README.md",
    });
  });

  it("falls back to workspace paths when no checkout prefix matches", () => {
    expect(
      mapWorkspaceIndexPathToCheckout(".loom/config.json", [
        { scope: "repo", target: "source-repo" },
      ]),
    ).toEqual({
      ref: { scope: "workspace" },
      path: ".loom/config.json",
    });
  });

  it("uses a flattened single-agent ref when that is the known checkout", () => {
    expect(
      mapWorkspaceIndexPathToCheckout(
        "worktrees/source-repo/local-coder/README.md",
        [{ scope: "agent", target: "local-coder" }],
      ),
    ).toEqual({
      ref: { scope: "agent", target: "local-coder" },
      path: "README.md",
    });
  });
});
