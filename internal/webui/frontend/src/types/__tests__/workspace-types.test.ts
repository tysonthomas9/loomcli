/**
 * Unit tests for workspace-related type definitions.
 * Verifies the shape and fields of RepoInfo, WorkspaceAgentInfo,
 * WorkspaceData, and the cross_repo field on LoomAgentStatus.
 */

import { describe, it, expect } from "vitest";

import type {
  LoomAgentStatus,
  WorkspaceData,
  RepoInfo,
  WorkspaceAgentInfo,
  WorkspaceSummary,
} from "../index";

describe("LoomAgentStatus cross_repo field", () => {
  it("supports cross_repo as optional boolean", () => {
    const agent: LoomAgentStatus = {
      name: "nova",
      branch: "main",
      status: "ready",
      ahead: 0,
      behind: 0,
      cross_repo: true,
    };

    expect(agent.cross_repo).toBe(true);
  });

  it("cross_repo defaults to undefined when not set", () => {
    const agent: LoomAgentStatus = {
      name: "nova",
      branch: "main",
      status: "ready",
      ahead: 0,
      behind: 0,
    };

    expect(agent.cross_repo).toBeUndefined();
  });

  it("cross_repo can be false", () => {
    const agent: LoomAgentStatus = {
      name: "nova",
      branch: "main",
      status: "ready",
      ahead: 0,
      behind: 0,
      cross_repo: false,
    };

    expect(agent.cross_repo).toBe(false);
  });
});

describe("RepoInfo type", () => {
  it("has required fields: name, path, default_branch, remote, groups", () => {
    const repo: RepoInfo = {
      name: "api",
      path: "/home/user/workspace/api",
      default_branch: "main",
      remote: "origin",
      groups: [],
    };

    expect(repo.name).toBe("api");
    expect(repo.path).toBe("/home/user/workspace/api");
    expect(repo.default_branch).toBe("main");
    expect(repo.remote).toBe("origin");
    expect(repo.groups).toEqual([]);
  });

  it("supports optional source_repo_id field", () => {
    const repo: RepoInfo = {
      name: "api",
      path: "/home/user/workspace/api",
      default_branch: "main",
      remote: "origin",
      groups: [],
      source_repo_id: "repo-abc-123",
    };

    expect(repo.source_repo_id).toBe("repo-abc-123");
  });

  it("source_repo_id defaults to undefined when not set", () => {
    const repo: RepoInfo = {
      name: "api",
      path: "/home/user/workspace/api",
      default_branch: "main",
      remote: "origin",
      groups: [],
    };

    expect(repo.source_repo_id).toBeUndefined();
  });

  it("supports groups field as string array", () => {
    const repo: RepoInfo = {
      name: "api",
      path: "/home/user/workspace/api",
      default_branch: "main",
      remote: "origin",
      groups: ["backend", "core"],
    };

    expect(repo.groups).toEqual(["backend", "core"]);
    expect(repo.groups).toHaveLength(2);
  });

  it("groups can be an empty array", () => {
    const repo: RepoInfo = {
      name: "api",
      path: "/home/user/workspace/api",
      default_branch: "main",
      remote: "origin",
      groups: [],
    };

    expect(repo.groups).toEqual([]);
  });
});

describe("WorkspaceAgentInfo type", () => {
  it("has all required fields", () => {
    const agent: WorkspaceAgentInfo = {
      name: "nova",
      repos: ["api", "frontend"],
      repo_groups: ["backend", "frontend"],
      cross_repo: true,
    };

    expect(agent.name).toBe("nova");
    expect(agent.repos).toEqual(["api", "frontend"]);
    expect(agent.repo_groups).toEqual(["backend", "frontend"]);
    expect(agent.cross_repo).toBe(true);
  });

  it("supports single-repo agent (cross_repo false)", () => {
    const agent: WorkspaceAgentInfo = {
      name: "falcon",
      repos: ["api"],
      repo_groups: ["backend"],
      cross_repo: false,
    };

    expect(agent.repos).toHaveLength(1);
    expect(agent.cross_repo).toBe(false);
  });

  it("supports agent with empty repos and groups", () => {
    const agent: WorkspaceAgentInfo = {
      name: "idle-agent",
      repos: [],
      repo_groups: [],
      cross_repo: false,
    };

    expect(agent.repos).toEqual([]);
    expect(agent.repo_groups).toEqual([]);
  });
});

describe("WorkspaceSummary type", () => {
  it("has all required fields", () => {
    const summary: WorkspaceSummary = {
      name: "my-workspace",
      path: "/home/user/workspace",
      active: true,
      repo_count: 3,
    };

    expect(summary.name).toBe("my-workspace");
    expect(summary.path).toBe("/home/user/workspace");
    expect(summary.active).toBe(true);
    expect(summary.repo_count).toBe(3);
  });

  it("supports inactive workspace", () => {
    const summary: WorkspaceSummary = {
      name: "other-workspace",
      path: "/home/user/other",
      active: false,
      repo_count: 1,
    };

    expect(summary.active).toBe(false);
  });
});

describe("WorkspaceData type", () => {
  it("has required fields: name, path, repos, groups, agents, workspaces", () => {
    const workspace: WorkspaceData = {
      name: "my-workspace",
      path: "/home/user/workspace",
      repos: [
        {
          name: "api",
          path: "/home/user/workspace/api",
          default_branch: "main",
          remote: "origin",
          groups: [],
        },
      ],
      groups: [],
      agents: [],
      workspaces: [],
    };

    expect(workspace.name).toBe("my-workspace");
    expect(workspace.path).toBe("/home/user/workspace");
    expect(workspace.repos).toHaveLength(1);
    expect(workspace.groups).toEqual([]);
    expect(workspace.agents).toEqual([]);
    expect(workspace.workspaces).toEqual([]);
  });

  it("supports groups field", () => {
    const workspace: WorkspaceData = {
      name: "my-workspace",
      path: "/home/user/workspace",
      repos: [],
      groups: ["backend", "frontend", "infra"],
      agents: [],
      workspaces: [],
    };

    expect(workspace.groups).toEqual(["backend", "frontend", "infra"]);
  });

  it("supports agents field", () => {
    const workspace: WorkspaceData = {
      name: "my-workspace",
      path: "/home/user/workspace",
      repos: [],
      groups: [],
      agents: [
        {
          name: "nova",
          repos: ["api"],
          repo_groups: ["backend"],
          cross_repo: false,
        },
        {
          name: "falcon",
          repos: ["api", "frontend"],
          repo_groups: ["backend", "frontend"],
          cross_repo: true,
        },
      ],
      workspaces: [],
    };

    expect(workspace.agents).toHaveLength(2);
    expect(workspace.agents[0].name).toBe("nova");
    expect(workspace.agents[1].cross_repo).toBe(true);
  });

  it("supports workspaces field with summaries", () => {
    const workspace: WorkspaceData = {
      name: "my-workspace",
      path: "/home/user/workspace",
      repos: [],
      groups: [],
      agents: [],
      workspaces: [
        { name: "my-workspace", path: "/home/user/workspace", active: true, repo_count: 2 },
        { name: "other", path: "/home/user/other", active: false, repo_count: 1 },
      ],
    };

    expect(workspace.workspaces).toHaveLength(2);
    expect(workspace.workspaces[0].active).toBe(true);
    expect(workspace.workspaces[1].active).toBe(false);
  });

  it("supports full workspace with repos, groups, agents, and workspaces", () => {
    const workspace: WorkspaceData = {
      name: "multi-repo-workspace",
      path: "/home/user/workspace",
      repos: [
        {
          name: "api",
          path: "/home/user/workspace/api",
          default_branch: "main",
          remote: "origin",
          source_repo_id: "repo-1",
          groups: ["backend"],
        },
        {
          name: "frontend",
          path: "/home/user/workspace/frontend",
          default_branch: "main",
          remote: "origin",
          source_repo_id: "repo-2",
          groups: ["frontend"],
        },
      ],
      groups: ["backend", "frontend"],
      agents: [
        {
          name: "nova",
          repos: ["api"],
          repo_groups: ["backend"],
          cross_repo: false,
        },
      ],
      workspaces: [
        { name: "multi-repo-workspace", path: "/home/user/workspace", active: true, repo_count: 2 },
      ],
    };

    expect(workspace.repos).toHaveLength(2);
    expect(workspace.groups).toHaveLength(2);
    expect(workspace.agents).toHaveLength(1);
    expect(workspace.workspaces).toHaveLength(1);
    expect(workspace.repos[0].source_repo_id).toBe("repo-1");
    expect(workspace.repos[0].groups).toEqual(["backend"]);
  });
});
