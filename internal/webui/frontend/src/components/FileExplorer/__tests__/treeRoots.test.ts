import { describe, expect, it } from "vitest";

import type {
  FileCheckout,
  RepoInfo,
  WorkspaceAgentInfo,
} from "@/api/workspace";

import { buildFileTreeSections } from "../treeRoots";

function repo(name: string, groups: string[] = []): RepoInfo {
  return {
    name,
    path: `/tmp/${name}`,
    default_branch: "main",
    remote: "origin",
    groups,
  };
}

function agent(partial: Partial<WorkspaceAgentInfo>): WorkspaceAgentInfo {
  return {
    name: partial.name ?? "agent-a",
    repos: partial.repos ?? [],
    repo_groups: partial.repo_groups ?? [],
    cross_repo: partial.cross_repo ?? false,
  };
}

function checkout(item: FileCheckout): FileCheckout {
  return item;
}

describe("treeRoots", () => {
  it("flattens a single-repo agent and rolls up its badge", () => {
    const sections = buildFileTreeSections({
      mode: "workspace",
      agents: [agent({ name: "local-coder", repos: ["source-repo"] })],
      repos: [repo("source-repo")],
      checkouts: [
        checkout({
          kind: "agent",
          agent: "local-coder",
          repo: "source-repo",
          exists: true,
          change_count: 3,
        }),
      ],
    });

    const agentRoot = sections[0]?.roots[0];
    expect(agentRoot).toMatchObject({
      kind: "agent",
      label: "local-coder",
      secondary: "source-repo",
      changeCount: 3,
      flattenedRef: {
        scope: "agent",
        target: "local-coder",
        repo: "source-repo",
      },
      children: [],
    });
  });

  it("renders multi-repo agents with repo checkout children", () => {
    const sections = buildFileTreeSections({
      mode: "workspace",
      agents: [
        agent({
          name: "local-planner",
          repos: ["source-repo", "docs-repo"],
          cross_repo: true,
        }),
      ],
      repos: [repo("source-repo"), repo("docs-repo")],
      checkouts: [
        checkout({
          kind: "agent",
          agent: "local-planner",
          repo: "source-repo",
          exists: true,
          change_count: 1,
        }),
        checkout({
          kind: "agent",
          agent: "local-planner",
          repo: "docs-repo",
          exists: false,
          change_count: 0,
        }),
      ],
    });

    const agentRoot = sections[0]?.roots[0];
    expect(agentRoot).toMatchObject({
      kind: "agent",
      changeCount: 1,
    });
    expect(agentRoot).not.toHaveProperty("flattenedRef");
    expect(agentRoot?.kind === "agent" ? agentRoot.children : []).toEqual([
      expect.objectContaining({
        label: "source-repo",
        exists: true,
        changeCount: 1,
        ref: {
          scope: "agent",
          target: "local-planner",
          repo: "source-repo",
        },
      }),
      expect.objectContaining({
        label: "docs-repo",
        exists: false,
        ref: {
          scope: "agent",
          target: "local-planner",
          repo: "docs-repo",
        },
      }),
    ]);
  });

  it("does not roll up unavailable checkout change counts", () => {
    const sections = buildFileTreeSections({
      mode: "workspace",
      agents: [
        agent({
          name: "local-planner",
          repos: ["source-repo", "docs-repo"],
          cross_repo: true,
        }),
      ],
      repos: [repo("source-repo"), repo("docs-repo")],
      checkouts: [
        checkout({
          kind: "agent",
          agent: "local-planner",
          repo: "source-repo",
          exists: true,
          change_count: 2,
        }),
        checkout({
          kind: "agent",
          agent: "local-planner",
          repo: "docs-repo",
          exists: true,
          change_count: 5,
          status_error: true,
        }),
      ],
    });

    const agentRoot = sections[0]?.roots[0];
    expect(agentRoot).toMatchObject({
      kind: "agent",
      changeCount: 2,
    });
    expect(
      agentRoot?.kind === "agent" ? agentRoot.children[1] : undefined,
    ).toMatchObject({
      label: "docs-repo",
      exists: true,
      changeCount: 0,
    });
  });

  it("expands repo groups and includes shared repo and workspace sections", () => {
    const sections = buildFileTreeSections({
      mode: "workspace",
      agents: [agent({ name: "group-agent", repo_groups: ["docs"] })],
      repos: [repo("docs-repo", ["docs"])],
      checkouts: [
        checkout({
          kind: "repo",
          repo: "docs-repo",
          exists: true,
          change_count: 2,
        }),
      ],
    });

    expect(sections.map((section) => section.id)).toEqual([
      "agents",
      "repos",
      "workspace",
    ]);
    expect(sections[0]?.roots[0]).toMatchObject({
      kind: "agent",
      secondary: "docs-repo",
    });
    expect(sections[1]?.roots[0]).toMatchObject({
      kind: "checkout",
      label: "docs-repo",
      changeCount: 2,
      ref: { scope: "repo", target: "docs-repo" },
    });
  });

  it("agent mode returns only the selected agent checkout roots", () => {
    const sections = buildFileTreeSections({
      mode: "agent",
      agentName: "atlas",
      agents: [
        agent({ name: "atlas", repos: ["source-repo"] }),
        agent({ name: "nova", repos: ["docs-repo"] }),
      ],
      repos: [repo("source-repo"), repo("docs-repo")],
      checkouts: [
        checkout({
          kind: "agent",
          agent: "atlas",
          repo: "source-repo",
          exists: true,
          change_count: 1,
        }),
        checkout({
          kind: "agent",
          agent: "nova",
          repo: "docs-repo",
          exists: true,
          change_count: 3,
        }),
        checkout({
          kind: "repo",
          repo: "source-repo",
          exists: true,
          change_count: 5,
        }),
      ],
    });

    expect(sections.map((section) => section.id)).toEqual(["agents"]);
    expect(sections[0]?.roots).toHaveLength(1);
    expect(sections[0]?.roots[0]).toMatchObject({
      kind: "agent",
      label: "atlas",
      secondary: "source-repo",
      changeCount: 1,
    });
  });
});
