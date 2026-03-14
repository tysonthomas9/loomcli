/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceContext hook and WorkspaceProvider.
 * Follows useAgentContext test pattern: mock underlying hook, test provider and helpers.
 */

import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, it, expect, vi } from "vitest";

import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";

import { WorkspaceProvider, useWorkspaceContext } from "../useWorkspaceContext";
import { useWorkspace } from "../useWorkspace";
import type { UseWorkspaceReturn } from "../useWorkspace";

// Mock useWorkspace so we don't trigger real polling
vi.mock("../useWorkspace", () => ({
  useWorkspace: vi.fn(),
}));
const mockUseWorkspace = vi.mocked(useWorkspace);

/**
 * Helper to create a mock UseWorkspaceReturn.
 */
function setupMockWorkspace(overrides?: Partial<UseWorkspaceReturn>): void {
  const defaultReturn: UseWorkspaceReturn = {
    workspace: {
      name: "test-workspace",
      path: "/home/user/workspace",
      repos: [],
      groups: [],
      agents: [],
      workspaces: [],
    },
    repos: [],
    groups: [],
    agents: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
    ...overrides,
  };
  mockUseWorkspace.mockReturnValue(defaultReturn);
}

/**
 * Helper to create a mock RepoInfo.
 */
function createMockRepo(overrides?: Partial<RepoInfo>): RepoInfo {
  return {
    name: "api",
    path: "/home/user/workspace/api",
    default_branch: "main",
    remote: "origin",
    groups: [],
    ...overrides,
  };
}

/**
 * Helper to create a mock WorkspaceAgentInfo.
 */
function createMockAgent(
  overrides?: Partial<WorkspaceAgentInfo>,
): WorkspaceAgentInfo {
  return {
    name: "nova",
    repos: ["api"],
    repo_groups: ["backend"],
    cross_repo: false,
    ...overrides,
  };
}

describe("useWorkspaceContext", () => {
  describe("outside provider", () => {
    it("returns safe defaults without throwing", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.workspace).toBeNull();
      expect(result.current.repos).toEqual([]);
      expect(result.current.groups).toEqual([]);
      expect(result.current.agents).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("getRepoByName returns undefined outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.getRepoByName("anything")).toBeUndefined();
    });

    it("getReposByGroup returns empty array outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.getReposByGroup("backend")).toEqual([]);
    });

    it("getAgentByName returns undefined outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.getAgentByName("nova")).toBeUndefined();
    });

    it("refetch is a no-op function outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      // Should not throw
      expect(() => result.current.refetch()).not.toThrow();
    });
  });

  describe("inside provider", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return <WorkspaceProvider>{children}</WorkspaceProvider>;
    }

    it("provides repos from useWorkspace", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.repos).toHaveLength(2);
      expect(result.current.repos[0].name).toBe("api");
      expect(result.current.repos[1].name).toBe("frontend");
    });

    it("provides groups from useWorkspace", () => {
      setupMockWorkspace({ groups: ["backend", "frontend", "infra"] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.groups).toEqual(["backend", "frontend", "infra"]);
    });

    it("provides agents from useWorkspace", () => {
      const agents = [
        createMockAgent({ name: "nova" }),
        createMockAgent({ name: "falcon", cross_repo: true }),
      ];
      setupMockWorkspace({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.agents).toHaveLength(2);
      expect(result.current.agents[0].name).toBe("nova");
      expect(result.current.agents[1].name).toBe("falcon");
    });

    it("provides isLoading from useWorkspace", () => {
      setupMockWorkspace({ isLoading: true });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.isLoading).toBe(true);
    });

    it("provides error from useWorkspace", () => {
      setupMockWorkspace({ error: "Network error" });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.error).toBe("Network error");
    });

    it("passes pollInterval of 60000 to useWorkspace", () => {
      setupMockWorkspace();

      renderHook(() => useWorkspaceContext(), { wrapper });

      expect(mockUseWorkspace).toHaveBeenCalledWith({
        pollInterval: 60000,
      });
    });
  });

  describe("getRepoByName", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return <WorkspaceProvider>{children}</WorkspaceProvider>;
    }

    it("finds correct repo by name", () => {
      const repos = [
        createMockRepo({ name: "api", source_repo_id: "repo-1" }),
        createMockRepo({ name: "frontend", source_repo_id: "repo-2" }),
        createMockRepo({ name: "backend", source_repo_id: "repo-3" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      const found = result.current.getRepoByName("frontend");
      expect(found).toBeDefined();
      expect(found!.name).toBe("frontend");
      expect(found!.source_repo_id).toBe("repo-2");
    });

    it("returns undefined for unknown repo name", () => {
      const repos = [createMockRepo({ name: "api" })];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getRepoByName("unknown")).toBeUndefined();
    });

    it("returns undefined when repos list is empty", () => {
      setupMockWorkspace({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getRepoByName("api")).toBeUndefined();
    });
  });

  describe("getReposByGroup", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return <WorkspaceProvider>{children}</WorkspaceProvider>;
    }

    it("filters correctly by group", () => {
      const repos = [
        createMockRepo({ name: "api", groups: ["backend"] }),
        createMockRepo({
          name: "frontend",
          groups: ["frontend"],
        }),
        createMockRepo({
          name: "gateway",
          groups: ["backend", "infra"],
        }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      const backendRepos = result.current.getReposByGroup("backend");
      expect(backendRepos).toHaveLength(2);
      expect(backendRepos.map((r) => r.name)).toEqual(["api", "gateway"]);
    });

    it("returns empty array for unknown group", () => {
      const repos = [createMockRepo({ name: "api", groups: ["backend"] })];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getReposByGroup("nonexistent")).toEqual([]);
    });

    it("returns empty array when no repos have groups", () => {
      const repos = [
        createMockRepo({ name: "api", groups: undefined }),
        createMockRepo({ name: "frontend", groups: undefined }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getReposByGroup("backend")).toEqual([]);
    });

    it("returns repos that belong to multiple groups", () => {
      const repos = [
        createMockRepo({
          name: "gateway",
          groups: ["backend", "infra"],
        }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // Should appear in both groups
      expect(result.current.getReposByGroup("backend")).toHaveLength(1);
      expect(result.current.getReposByGroup("infra")).toHaveLength(1);
    });
  });

  describe("getAgentByName", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return <WorkspaceProvider>{children}</WorkspaceProvider>;
    }

    it("finds correct agent by name", () => {
      const agents = [
        createMockAgent({ name: "nova", cross_repo: false }),
        createMockAgent({ name: "falcon", cross_repo: true }),
      ];
      setupMockWorkspace({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      const found = result.current.getAgentByName("falcon");
      expect(found).toBeDefined();
      expect(found!.name).toBe("falcon");
      expect(found!.cross_repo).toBe(true);
    });

    it("returns undefined for unknown agent name", () => {
      const agents = [createMockAgent({ name: "nova" })];
      setupMockWorkspace({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getAgentByName("unknown-agent")).toBeUndefined();
    });

    it("returns undefined when agents list is empty", () => {
      setupMockWorkspace({ agents: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getAgentByName("nova")).toBeUndefined();
    });
  });
});
