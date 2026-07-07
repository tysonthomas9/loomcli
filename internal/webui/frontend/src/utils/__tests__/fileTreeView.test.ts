import { describe, expect, it } from "vitest";

import type { RepoInfo } from "@/api/workspace";

import { resolveFileTreeViewMode } from "../fileTreeView";

const repos: RepoInfo[] = [
  {
    name: "loomcli",
    path: "/tmp/loomcli",
    default_branch: "main",
    remote: "origin",
    groups: [],
  },
  {
    name: "nova-wt",
    path: "/tmp/loomcli/.worktrees/nova",
    default_branch: "main",
    remote: "origin",
    groups: [],
    is_linked_worktree: true,
  },
];

describe("resolveFileTreeViewMode", () => {
  it("uses agent worktree mode for task agents", () => {
    expect(resolveFileTreeViewMode("task", "loomcli", repos)).toEqual({
      useWorkspaceTree: false,
      repoLabel: null,
    });
  });

  it("uses workspace tree mode for lead agents", () => {
    expect(resolveFileTreeViewMode("lead", "loomcli", repos)).toEqual({
      useWorkspaceTree: true,
      repoLabel: "loomcli",
    });
  });

  it("uses workspace tree mode for orchestrator agents", () => {
    expect(resolveFileTreeViewMode("orchestrator", undefined, repos)).toEqual({
      useWorkspaceTree: true,
      repoLabel: "loomcli",
    });
  });

  it("falls back to the first primary repo when lead has no repo scope", () => {
    expect(resolveFileTreeViewMode("Lead", undefined, repos)).toEqual({
      useWorkspaceTree: true,
      repoLabel: "loomcli",
    });
  });
});
